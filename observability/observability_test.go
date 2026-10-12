package observability

import (
	"context"
	"errors"
	"math"
	"reflect"
	"sync"
	"testing"
	"time"
)

type captureSink struct {
	mu      sync.Mutex
	records []LogRecord
	err     error
}

func (s *captureSink) WriteLog(record LogRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, record)
	return s.err
}

func (s *captureSink) snapshot() []LogRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]LogRecord(nil), s.records...)
}

func TestLoggerWritesBoundedStructuredRecordAndTrace(t *testing.T) {
	t.Parallel()
	sink := &captureSink{}
	wantAt := time.Date(2026, 10, 12, 8, 9, 10, 11, time.FixedZone("local", -7*60*60))
	logger, err := NewLogger(LoggerConfig{Sink: sink, Clock: func() time.Time { return wantAt }})
	if err != nil {
		t.Fatal(err)
	}
	trace := TraceContext{TraceID: "0123456789abcdef0123456789abcdef", SpanID: "0123456789abcdef"}
	ctx, err := WithTrace(context.Background(), trace)
	if err != nil {
		t.Fatal(err)
	}
	fields := []Field{IntField("attempt", 2), FloatField("elapsed_ms", 1.5), BoolField("cache_hit", true)}
	if err := logger.Emit(ctx, LevelInfo, "shot.completed", fields...); err != nil {
		t.Fatal(err)
	}
	records := sink.snapshot()
	if len(records) != 1 {
		t.Fatalf("records=%d, want 1", len(records))
	}
	record := records[0]
	if !record.At.Equal(wantAt) || record.At.Location() != time.UTC {
		t.Fatalf("timestamp=%v, want UTC equivalent of %v", record.At, wantAt)
	}
	if record.Level != LevelInfo || record.Code != "shot.completed" || record.Trace != trace {
		t.Fatalf("record metadata = %#v", record)
	}
	if len(record.Fields) != 3 || record.Fields[0].Key() != "attempt" {
		t.Fatalf("fields = %#v", record.Fields)
	}
	if value, ok := record.Fields[0].Int64(); !ok || value != 2 {
		t.Fatalf("integer field = %d, %v", value, ok)
	}
	if value, ok := record.Fields[1].Float64(); !ok || value != 1.5 {
		t.Fatalf("float field = %v, %v", value, ok)
	}
	if value, ok := record.Fields[2].Bool(); !ok || !value {
		t.Fatalf("bool field = %v, %v", value, ok)
	}
	// The sink receives a record detached from the caller's field slice.
	fields[0] = IntField("attempt", 99)
	if got := sink.snapshot()[0].Fields[0]; got.Key() != "attempt" {
		t.Fatal("caller mutation changed emitted field key")
	} else if value, ok := got.Int64(); !ok || value != 2 {
		t.Fatalf("caller mutation changed emitted field value: %d, %v", value, ok)
	}
}

func TestLoggerRejectsUnboundedOrInvalidRecords(t *testing.T) {
	t.Parallel()
	sink := &captureSink{}
	logger, err := NewLogger(LoggerConfig{Sink: sink, MaxFields: 2})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		level  Level
		code   string
		fields []Field
	}{
		{name: "empty code", level: LevelInfo},
		{name: "free form code", level: LevelInfo, code: "prompt with payload"},
		{name: "invalid level", level: 99, code: "safe.code"},
		{name: "too many fields", level: LevelInfo, code: "safe.code", fields: []Field{IntField("one", 1), IntField("two", 2), IntField("three", 3)}},
		{name: "duplicate fields", level: LevelInfo, code: "safe.code", fields: []Field{IntField("one", 1), BoolField("one", true)}},
		{name: "invalid field key", level: LevelInfo, code: "safe.code", fields: []Field{IntField("raw payload", 1)}},
		{name: "nonfinite field", level: LevelInfo, code: "safe.code", fields: []Field{FloatField("ratio", math.NaN())}},
		{name: "unset field", level: LevelInfo, code: "safe.code", fields: []Field{{}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := logger.Emit(context.Background(), tt.level, tt.code, tt.fields...); !errors.Is(err, ErrInvalid) {
				t.Fatalf("Emit() error = %v, want ErrInvalid", err)
			}
		})
	}
	if got := len(sink.snapshot()); got != 0 {
		t.Fatalf("invalid records reached sink: %d", got)
	}
}

func TestLoggerDoesNotExposeSinkError(t *testing.T) {
	t.Parallel()
	secretLike := errors.New("credential=not-for-logs")
	sink := &captureSink{err: secretLike}
	logger, err := NewLogger(LoggerConfig{Sink: sink})
	if err != nil {
		t.Fatal(err)
	}
	err = logger.Emit(context.Background(), LevelWarn, "worker.failed")
	if !errors.Is(err, ErrSink) || errors.Is(err, secretLike) || err.Error() != ErrSink.Error() {
		t.Fatalf("Emit() error = %v, want safe ErrSink only", err)
	}
}

func TestLoggerConcurrentEmitToSafeInjectedSink(t *testing.T) {
	sink := &captureSink{}
	logger, err := NewLogger(LoggerConfig{
		Sink:  sink,
		Clock: func() time.Time { return time.Unix(7, 0).UTC() },
	})
	if err != nil {
		t.Fatal(err)
	}
	const workers = 8
	const perWorker = 250
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for n := 0; n < perWorker; n++ {
				if err := logger.Emit(context.Background(), LevelDebug, "stress.record", IntField("sequence", int64(n))); err != nil {
					t.Errorf("Emit() error = %v", err)
					return
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	if got, want := len(sink.snapshot()), workers*perWorker; got != want {
		t.Fatalf("record count = %d, want %d", got, want)
	}
}

func TestNewLoggerValidatesDependenciesAndLimits(t *testing.T) {
	t.Parallel()
	var typedNil *captureSink
	for _, cfg := range []LoggerConfig{
		{},
		{Sink: typedNil},
		{Sink: LogSinkFunc(nil)},
		{Sink: &captureSink{}, MaxFields: -1},
		{Sink: &captureSink{}, MaxFields: hardMaxFields + 1},
	} {
		if _, err := NewLogger(cfg); !errors.Is(err, ErrInvalid) {
			t.Errorf("NewLogger(%+v) error = %v, want ErrInvalid", cfg, err)
		}
	}
}

func TestTraceContextValidationAndIsolation(t *testing.T) {
	t.Parallel()
	valid := TraceContext{TraceID: "11111111111111111111111111111111", SpanID: "2222222222222222"}
	ctx, err := WithTrace(context.Background(), valid)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := TraceFromContext(ctx)
	if !ok || got != valid {
		t.Fatalf("TraceFromContext = %#v, %v", got, ok)
	}
	if _, ok := TraceFromContext(context.Background()); ok {
		t.Fatal("unexpected trace on background context")
	}
	invalid := []TraceContext{
		{},
		{TraceID: "00000000000000000000000000000000", SpanID: valid.SpanID},
		{TraceID: "ABCDEF0123456789ABCDEF0123456789", SpanID: valid.SpanID},
		{TraceID: "0123456789abcdef0123456789abcdef", SpanID: "0000000000000000"},
		{TraceID: "0123456789abcdef0123456789abcdeg", SpanID: valid.SpanID},
	}
	for _, item := range invalid {
		if _, err := WithTrace(context.Background(), item); !errors.Is(err, ErrInvalid) {
			t.Errorf("WithTrace(%+v) error = %v", item, err)
		}
	}
	if _, err := WithTrace(nil, valid); !errors.Is(err, ErrInvalid) {
		t.Fatalf("WithTrace(nil) error = %v", err)
	}
}

func TestMetricsCountersGaugesHistogramsAndSnapshotOwnership(t *testing.T) {
	t.Parallel()
	metrics, err := NewMetrics(MetricsConfig{})
	if err != nil {
		t.Fatal(err)
	}
	region, _ := NewLabel("region", "local")
	mode, _ := NewLabel("mode", "draft")
	counter, err := metrics.Counter("cache.hits", region, mode)
	if err != nil {
		t.Fatal(err)
	}
	if err := counter.Add(3); err != nil {
		t.Fatal(err)
	}
	gauge, err := metrics.Gauge("workers.active")
	if err != nil {
		t.Fatal(err)
	}
	if err := gauge.Set(2.5); err != nil {
		t.Fatal(err)
	}
	histogram, err := metrics.Histogram("render.duration_ms", []float64{10, 25, 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []float64{3, 10, 24, 25, 101} {
		if err := histogram.Observe(value); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := metrics.Snapshot()
	if len(snapshot.Counters) != 1 || snapshot.Counters[0].Value != 3 {
		t.Fatalf("counter snapshot = %#v", snapshot.Counters)
	}
	if got := snapshot.Counters[0].Labels; !reflect.DeepEqual(got, []Label{{Name: "mode", Value: "draft"}, {Name: "region", Value: "local"}}) {
		t.Fatalf("canonical labels = %#v", got)
	}
	if len(snapshot.Gauges) != 1 || snapshot.Gauges[0].Value != 2.5 {
		t.Fatalf("gauge snapshot = %#v", snapshot.Gauges)
	}
	if len(snapshot.Histograms) != 1 {
		t.Fatalf("histogram snapshot = %#v", snapshot.Histograms)
	}
	gotHistogram := snapshot.Histograms[0]
	if gotHistogram.Count != 5 || gotHistogram.Sum != 163 || !reflect.DeepEqual(gotHistogram.BucketCounts, []uint64{2, 2, 0, 1}) {
		t.Fatalf("histogram snapshot = %#v", gotHistogram)
	}
	// Every exposed slice is detached from registry-owned storage.
	snapshot.Counters[0].Labels[0].Value = "mutated"
	snapshot.Histograms[0].Bounds[0] = 999
	snapshot.Histograms[0].BucketCounts[0] = 999
	again := metrics.Snapshot()
	if again.Counters[0].Labels[0].Value != "draft" || again.Histograms[0].Bounds[0] != 10 || again.Histograms[0].BucketCounts[0] != 2 {
		t.Fatalf("snapshot mutation escaped: %#v", again)
	}
}

func TestMetricsRejectInvalidDefinitionsAndBoundCardinality(t *testing.T) {
	t.Parallel()
	metrics, err := NewMetrics(MetricsConfig{MaxSeries: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := metrics.Counter("Invalid name"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid metric name error = %v", err)
	}
	if _, err := NewLabel("label", "contains space"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid label value error = %v", err)
	}
	if _, err := metrics.Histogram("bad.hist", []float64{2, 1}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unsorted bounds error = %v", err)
	}
	if _, err := metrics.Histogram("bad.hist", []float64{math.Inf(1)}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("infinite bounds error = %v", err)
	}
	labels := []Label{{Name: "b", Value: "two"}, {Name: "a", Value: "one"}}
	first, err := metrics.Counter("jobs.total", labels...)
	if err != nil {
		t.Fatal(err)
	}
	labels[0].Value = "mutated"
	second, err := metrics.Counter("jobs.total", Label{Name: "a", Value: "one"}, Label{Name: "b", Value: "two"})
	if err != nil || first.series != second.series {
		t.Fatalf("equivalent labels did not resolve to same series: %v", err)
	}
	if _, err := metrics.Gauge("jobs.total", Label{Name: "a", Value: "one"}, Label{Name: "b", Value: "two"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("type conflict error = %v", err)
	}
	if _, err := metrics.Histogram("jobs.total", []float64{1}, Label{Name: "a", Value: "one"}, Label{Name: "b", Value: "two"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("histogram type conflict error = %v", err)
	}
	if _, err := metrics.Counter("jobs.other"); !errors.Is(err, ErrLimit) {
		t.Fatalf("series cap error = %v", err)
	}
}

func TestHistogramDefinitionIsStable(t *testing.T) {
	t.Parallel()
	metrics, _ := NewMetrics(MetricsConfig{})
	first, err := metrics.Histogram("latency", []float64{1, 3})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := metrics.Histogram("latency", []float64{1, 4}); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed bounds error = %v", err)
	}
	if err := first.Observe(2); err != nil {
		t.Fatal(err)
	}
}

func TestMetricsRejectInvalidUpdatesWithoutMutation(t *testing.T) {
	t.Parallel()
	metrics, _ := NewMetrics(MetricsConfig{})
	counter, _ := metrics.Counter("updates.total")
	gauge, _ := metrics.Gauge("queue.depth")
	histogram, _ := metrics.Histogram("latency", []float64{1})
	if err := gauge.Set(math.NaN()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Set(NaN) error = %v", err)
	}
	if err := histogram.Observe(math.Inf(1)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Observe(+Inf) error = %v", err)
	}
	if err := counter.Add(1); err != nil {
		t.Fatal(err)
	}
	counter.series.counter = math.MaxUint64
	if err := counter.Add(1); !errors.Is(err, ErrLimit) {
		t.Fatalf("overflow Add error = %v", err)
	}
	if got := metrics.Snapshot().Counters[0].Value; got != math.MaxUint64 {
		t.Fatalf("overflowing Add mutated counter before reset: got %d, want %d", got, uint64(math.MaxUint64))
	}
	counter.series.counter = 1
	snapshot := metrics.Snapshot()
	if snapshot.Counters[0].Value != 1 || snapshot.Gauges[0].Value != 0 || snapshot.Histograms[0].Count != 0 {
		t.Fatalf("invalid updates changed registry: %#v", snapshot)
	}
}

func TestMetricsSnapshotOrderingIsStable(t *testing.T) {
	t.Parallel()
	metrics, _ := NewMetrics(MetricsConfig{})
	for _, label := range []string{"gamma", "alpha", "beta"} {
		item, err := NewLabel("state", label)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := metrics.Counter("events.total", item); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"alpha", "beta", "gamma"}
	for n := 0; n < 5; n++ {
		snapshot := metrics.Snapshot()
		got := make([]string, len(snapshot.Counters))
		for i, item := range snapshot.Counters {
			got[i] = item.Labels[0].Value
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("snapshot ordering = %v, want %v", got, want)
		}
	}
}

func TestMetricsConcurrentUpdatesAndSnapshots(t *testing.T) {
	metrics, err := NewMetrics(MetricsConfig{MaxSeries: 4})
	if err != nil {
		t.Fatal(err)
	}
	counter, _ := metrics.Counter("work.items")
	gauge, _ := metrics.Gauge("work.active")
	histogram, _ := metrics.Histogram("work.size", []float64{1, 4, 8})
	const workers = 8
	const iterations = 1200
	var wg sync.WaitGroup
	start := make(chan struct{})
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < iterations; i++ {
				if err := counter.Add(1); err != nil {
					t.Error(err)
					return
				}
				if err := histogram.Observe(2); err != nil {
					t.Error(err)
					return
				}
				if err := gauge.Set(float64(i % 7)); err != nil {
					t.Error(err)
					return
				}
				if i%127 == 0 {
					_ = metrics.Snapshot()
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	if err := gauge.Set(7); err != nil {
		t.Fatal(err)
	}
	snapshot := metrics.Snapshot()
	wantCount := uint64(workers * iterations)
	if snapshot.Counters[0].Value != wantCount || snapshot.Histograms[0].Count != wantCount || snapshot.Histograms[0].Sum != float64(wantCount*2) || snapshot.Gauges[0].Value != 7 {
		t.Fatalf("concurrent snapshot = %#v", snapshot)
	}
}

func TestMetricsConcurrentRegistrationNeverExceedsBound(t *testing.T) {
	metrics, err := NewMetrics(MetricsConfig{MaxSeries: 12})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var success, limited int
	var resultMu sync.Mutex
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := "series." + string(rune('a'+i%26))
			_, err := metrics.Counter(name)
			resultMu.Lock()
			defer resultMu.Unlock()
			switch err {
			case nil:
				success++
			case ErrLimit:
				limited++
			default:
				t.Errorf("Counter registration error = %v", err)
			}
		}(i)
	}
	wg.Wait()
	// There are repeated registration attempts, so success/limit counts depend
	// on interleaving. The registry must nevertheless remain at its hard cap.
	if success+limited != 100 || success < 12 || limited == 0 || len(metrics.Snapshot().Counters) != 12 {
		t.Fatalf("success=%d limited=%d snapshot=%d", success, limited, len(metrics.Snapshot().Counters))
	}
}

func TestTypedNilAndZeroValueSafety(t *testing.T) {
	t.Parallel()
	var nilMetrics *Metrics
	if _, err := nilMetrics.Counter("safe"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("nil metrics error = %v", err)
	}
	if got := nilMetrics.Snapshot(); len(got.Counters) != 0 {
		t.Fatalf("nil snapshot = %#v", got)
	}
	var nilCounter *Counter
	if err := nilCounter.Add(1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("nil counter error = %v", err)
	}
}
