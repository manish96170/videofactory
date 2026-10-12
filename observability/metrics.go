package observability

import (
	"math"
	"sort"
	"strings"
	"sync"
)

const (
	maxMetricNameBytes = 64
	maxLabelBytes      = 32
	maxLabels          = 8
	maxHistogramBounds = 32
	defaultMaxSeries   = 256
	hardMaxSeries      = 4096
)

// Label is a bounded low-cardinality classification. Use controlled enum-like
// names and values only; do not put IDs, prompts, paths, or secrets in labels.
type Label struct {
	Name  string
	Value string
}

// MetricsConfig bounds the number of distinct registered metric series. A
// zero MaxSeries selects the default of 256; the hard maximum is 4096.
type MetricsConfig struct {
	MaxSeries int
}

// Metrics is an in-process, concurrency-safe metric source. It retains only
// registered scalar series and exposes detached snapshots; it has no exporter.
type Metrics struct {
	mu        sync.Mutex
	maxSeries int
	series    map[string]*metricSeries
}

type metricKind uint8

const (
	kindCounter metricKind = iota + 1
	kindGauge
	kindHistogram
)

type metricSeries struct {
	name       string
	labels     []Label
	kind       metricKind
	counter    uint64
	gauge      float64
	bounds     []float64
	bucketHits []uint64 // one slot per inclusive upper bound, plus overflow
	count      uint64
	sum        float64
}

// Counter is a handle to a monotonic uint64 counter.
type Counter struct {
	owner  *Metrics
	series *metricSeries
}

// Gauge is a handle to a finite floating-point gauge.
type Gauge struct {
	owner  *Metrics
	series *metricSeries
}

// Histogram is a handle to a fixed-bound histogram.
type Histogram struct {
	owner  *Metrics
	series *metricSeries
}

// CounterSnapshot is one immutable-by-convention counter observation.
type CounterSnapshot struct {
	Name   string
	Labels []Label
	Value  uint64
}

// GaugeSnapshot is one immutable-by-convention gauge observation.
type GaugeSnapshot struct {
	Name   string
	Labels []Label
	Value  float64
}

// HistogramSnapshot contains fixed inclusive bounds and a final overflow
// bucket. BucketCounts has len(Bounds)+1 elements.
type HistogramSnapshot struct {
	Name         string
	Labels       []Label
	Bounds       []float64
	BucketCounts []uint64
	Count        uint64
	Sum          float64
}

// MetricsSnapshot is a detached snapshot with each metric kind sorted by name
// and canonical labels.
type MetricsSnapshot struct {
	Counters   []CounterSnapshot
	Gauges     []GaugeSnapshot
	Histograms []HistogramSnapshot
}

// NewMetrics creates an empty bounded metrics registry.
func NewMetrics(cfg MetricsConfig) (*Metrics, error) {
	if cfg.MaxSeries < 0 || cfg.MaxSeries > hardMaxSeries {
		return nil, ErrInvalid
	}
	if cfg.MaxSeries == 0 {
		cfg.MaxSeries = defaultMaxSeries
	}
	return &Metrics{maxSeries: cfg.MaxSeries, series: make(map[string]*metricSeries)}, nil
}

// NewLabel creates a bounded token label. Label names and values are limited
// to ASCII letters, digits, underscore, hyphen, and dot (with a letter first).
func NewLabel(name, value string) (Label, error) {
	if !validToken(name, maxLabelBytes) || !validToken(value, maxLabelBytes) {
		return Label{}, ErrInvalid
	}
	return Label{Name: name, Value: value}, nil
}

// Counter registers or returns a counter series.
func (m *Metrics) Counter(name string, labels ...Label) (*Counter, error) {
	s, err := m.register(name, labels, kindCounter, nil)
	if err != nil {
		return nil, err
	}
	return &Counter{owner: m, series: s}, nil
}

// Gauge registers or returns a gauge series.
func (m *Metrics) Gauge(name string, labels ...Label) (*Gauge, error) {
	s, err := m.register(name, labels, kindGauge, nil)
	if err != nil {
		return nil, err
	}
	return &Gauge{owner: m, series: s}, nil
}

// Histogram registers or returns a histogram with strictly increasing finite
// bounds. Bounds are inclusive; one additional overflow bucket is maintained.
func (m *Metrics) Histogram(name string, bounds []float64, labels ...Label) (*Histogram, error) {
	if len(bounds) > maxHistogramBounds {
		return nil, ErrLimit
	}
	for i, bound := range bounds {
		if math.IsNaN(bound) || math.IsInf(bound, 0) || (i > 0 && bound <= bounds[i-1]) {
			return nil, ErrInvalid
		}
	}
	s, err := m.register(name, labels, kindHistogram, bounds)
	if err != nil {
		return nil, err
	}
	return &Histogram{owner: m, series: s}, nil
}

func (m *Metrics) register(name string, labels []Label, kind metricKind, bounds []float64) (*metricSeries, error) {
	if m == nil || !validToken(name, maxMetricNameBytes) || len(labels) > maxLabels {
		return nil, ErrInvalid
	}
	owned := append([]Label(nil), labels...)
	for _, label := range owned {
		if !validToken(label.Name, maxLabelBytes) || !validToken(label.Value, maxLabelBytes) {
			return nil, ErrInvalid
		}
	}
	sort.Slice(owned, func(i, j int) bool {
		if owned[i].Name == owned[j].Name {
			return owned[i].Value < owned[j].Value
		}
		return owned[i].Name < owned[j].Name
	})
	for i := 1; i < len(owned); i++ {
		if owned[i-1].Name == owned[i].Name {
			return nil, ErrInvalid
		}
	}
	key := seriesKey(name, owned)
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing := m.series[key]; existing != nil {
		if existing.kind != kind || !floatSlicesEqual(existing.bounds, bounds) {
			return nil, ErrConflict
		}
		return existing, nil
	}
	if len(m.series) >= m.maxSeries {
		return nil, ErrLimit
	}
	s := &metricSeries{name: name, labels: owned, kind: kind}
	if kind == kindHistogram {
		s.bounds = append([]float64(nil), bounds...)
		s.bucketHits = make([]uint64, len(bounds)+1)
	}
	m.series[key] = s
	return s, nil
}

// Add increments the counter. Overflow is rejected without changing the value.
func (c *Counter) Add(delta uint64) error {
	if c == nil || c.owner == nil || c.series == nil || c.series.kind != kindCounter {
		return ErrInvalid
	}
	c.owner.mu.Lock()
	defer c.owner.mu.Unlock()
	if math.MaxUint64-c.series.counter < delta {
		return ErrLimit
	}
	c.series.counter += delta
	return nil
}

// Set replaces the gauge with a finite value.
func (g *Gauge) Set(value float64) error {
	if g == nil || g.owner == nil || g.series == nil || g.series.kind != kindGauge || math.IsNaN(value) || math.IsInf(value, 0) {
		return ErrInvalid
	}
	g.owner.mu.Lock()
	g.series.gauge = value
	g.owner.mu.Unlock()
	return nil
}

// Observe records one finite observation. Overflow or a non-finite resulting
// sum is rejected without partially updating the histogram.
func (h *Histogram) Observe(value float64) error {
	if h == nil || h.owner == nil || h.series == nil || h.series.kind != kindHistogram || math.IsNaN(value) || math.IsInf(value, 0) {
		return ErrInvalid
	}
	h.owner.mu.Lock()
	defer h.owner.mu.Unlock()
	if h.series.count == math.MaxUint64 {
		return ErrLimit
	}
	sum := h.series.sum + value
	if math.IsInf(sum, 0) || math.IsNaN(sum) {
		return ErrLimit
	}
	bucket := sort.SearchFloat64s(h.series.bounds, value)
	if h.series.bucketHits[bucket] == math.MaxUint64 {
		return ErrLimit
	}
	h.series.count++
	h.series.sum = sum
	h.series.bucketHits[bucket]++
	return nil
}

// Snapshot returns a detached, deterministically ordered view of all series.
func (m *Metrics) Snapshot() MetricsSnapshot {
	if m == nil {
		return MetricsSnapshot{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := MetricsSnapshot{}
	for _, s := range m.series {
		switch s.kind {
		case kindCounter:
			out.Counters = append(out.Counters, CounterSnapshot{Name: s.name, Labels: cloneLabels(s.labels), Value: s.counter})
		case kindGauge:
			out.Gauges = append(out.Gauges, GaugeSnapshot{Name: s.name, Labels: cloneLabels(s.labels), Value: s.gauge})
		case kindHistogram:
			out.Histograms = append(out.Histograms, HistogramSnapshot{
				Name: s.name, Labels: cloneLabels(s.labels), Bounds: append([]float64(nil), s.bounds...),
				BucketCounts: append([]uint64(nil), s.bucketHits...), Count: s.count, Sum: s.sum,
			})
		}
	}
	sort.Slice(out.Counters, func(i, j int) bool {
		return snapshotLess(out.Counters[i].Name, out.Counters[i].Labels, out.Counters[j].Name, out.Counters[j].Labels)
	})
	sort.Slice(out.Gauges, func(i, j int) bool {
		return snapshotLess(out.Gauges[i].Name, out.Gauges[i].Labels, out.Gauges[j].Name, out.Gauges[j].Labels)
	})
	sort.Slice(out.Histograms, func(i, j int) bool {
		return snapshotLess(out.Histograms[i].Name, out.Histograms[i].Labels, out.Histograms[j].Name, out.Histograms[j].Labels)
	})
	return out
}

func cloneLabels(labels []Label) []Label { return append([]Label(nil), labels...) }

func seriesKey(name string, labels []Label) string {
	var b strings.Builder
	b.WriteString(name)
	for _, label := range labels {
		b.WriteByte(0)
		b.WriteString(label.Name)
		b.WriteByte('=')
		b.WriteString(label.Value)
	}
	return b.String()
}

func snapshotLess(aName string, aLabels []Label, bName string, bLabels []Label) bool {
	if aName != bName {
		return aName < bName
	}
	limit := len(aLabels)
	if len(bLabels) < limit {
		limit = len(bLabels)
	}
	for i := 0; i < limit; i++ {
		if aLabels[i].Name != bLabels[i].Name {
			return aLabels[i].Name < bLabels[i].Name
		}
		if aLabels[i].Value != bLabels[i].Value {
			return aLabels[i].Value < bLabels[i].Value
		}
	}
	return len(aLabels) < len(bLabels)
}

func floatSlicesEqual(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
