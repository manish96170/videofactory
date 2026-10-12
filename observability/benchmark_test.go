package observability

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// Benchmarks use fixed IDs, labels, bounds, and timestamps. They measure
// in-process fixture overhead only; they do not invoke a model, provider,
// pipeline, cache store, network, or wall-clock-dependent sink.
func BenchmarkLoggerEmit(b *testing.B) {
	logger, err := NewLogger(LoggerConfig{
		Sink:  LogSinkFunc(func(LogRecord) error { return nil }),
		Clock: func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
	})
	if err != nil {
		b.Fatal(err)
	}
	ctx, err := WithTrace(context.Background(), TraceContext{
		TraceID: "0123456789abcdef0123456789abcdef",
		SpanID:  "0123456789abcdef",
	})
	if err != nil {
		b.Fatal(err)
	}
	fields := []Field{IntField("attempt", 1), BoolField("cache_hit", true)}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := logger.Emit(ctx, LevelInfo, "cache.lookup", fields...); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMetricsCounterAddParallel(b *testing.B) {
	metrics, err := NewMetrics(MetricsConfig{MaxSeries: 1})
	if err != nil {
		b.Fatal(err)
	}
	counter, err := metrics.Counter("pipeline.shots")
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if err := counter.Add(1); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkMetricsHistogramObserve(b *testing.B) {
	metrics, err := NewMetrics(MetricsConfig{MaxSeries: 1})
	if err != nil {
		b.Fatal(err)
	}
	histogram, err := metrics.Histogram("pipeline.render_ms", []float64{10, 25, 50, 100, 250})
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := histogram.Observe(float64(i%251) + 0.5); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMetricsSnapshot(b *testing.B) {
	const seriesCount = 64
	metrics, err := NewMetrics(MetricsConfig{MaxSeries: seriesCount * 3})
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < seriesCount; i++ {
		label := Label{Name: "class", Value: fmt.Sprintf("class-%02d", i)}
		counter, err := metrics.Counter("pipeline.completed", label)
		if err != nil {
			b.Fatal(err)
		}
		gauge, err := metrics.Gauge("pipeline.active", label)
		if err != nil {
			b.Fatal(err)
		}
		histogram, err := metrics.Histogram("cache.lookup_ms", []float64{1, 5, 20}, label)
		if err != nil {
			b.Fatal(err)
		}
		_ = counter.Add(uint64(i))
		_ = gauge.Set(float64(i))
		_ = histogram.Observe(float64(i % 20))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = metrics.Snapshot()
	}
}
