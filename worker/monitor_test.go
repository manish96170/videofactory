package worker

import (
	"context"
	"errors"
	"fmt"
	"math"
	"testing"
)

func TestNativePSParserLiveFamilyAndZombies(t *testing.T) {
	group := ProcessGroup{ID: 12345}
	s, err := parsePS([]byte("10 12345 2 R\n11 12345 3 S+\n12 12345 99 Z\n13 99 100 R\n14 12345 4 T\n"), group)
	if err != nil || s.Live != 3 || s.Zombies != 1 || s.RSSBytes == nil || *s.RSSBytes != 9*1024 {
		t.Fatal(s, err)
	}
	s, err = parsePS([]byte("12 12345 99 Z\n"), group)
	if err != nil || s.RSSBytes != nil || s.Source != "" || s.Live != 0 || s.Zombies != 1 {
		t.Fatal(s, err)
	}
	for _, raw := range []string{"10 12345 -1 R", "10 12345 2 ?", "10 12345 2 R cmd secret", "bad 12345 2 R", fmt.Sprintf("10 12345 %d R", math.MaxInt64), fmt.Sprintf("10 12345 %d R\n11 12345 %d R", math.MaxInt64/1024, math.MaxInt64/1024)} {
		if _, err = parsePS([]byte(raw), group); !errors.Is(err, ErrMonitor) {
			t.Fatal(raw, err)
		}
	}
}

func TestNativeMonitorContextAndGroupValidation(t *testing.T) {
	m := NativeMonitor{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m.Sample(ctx, ProcessGroup{ID: 12345}); !errors.Is(err, context.Canceled) && platformSupported() {
		t.Fatal(err)
	}
	for _, group := range []ProcessGroup{{ID: 0}, {ID: -3}, {ID: 1}} {
		if _, err := m.Sample(context.Background(), group); !errors.Is(err, ErrMonitor) {
			t.Fatal(err)
		}
	}
	if _, err := m.Sample(nil, ProcessGroup{ID: 12345}); !errors.Is(err, ErrMonitor) {
		t.Fatal(err)
	}
}

func TestUnknownMeasurementNoFabricatedFacts(t *testing.T) {
	h := &CleanupHandle{}
	m := h.measurement(false)
	if m.FamilyRSS != nil || m.ProcessPeakRSS != nil || m.RunMs != nil || m.LoadMs != nil || m.UnloadMs != nil || m.TimingSource != "" {
		t.Fatal(m)
	}
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := validateSample(Sample{Live: 1}); !errors.Is(err, ErrMonitor) {
		t.Fatal(err)
	}
	if err := validateSample(Sample{}); err != nil {
		t.Fatal(err)
	}
}

func TestNativeQueryBufferBoundsAndCancels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b := &queryBuffer{cancel: cancel}
	if n, err := b.Write(make([]byte, 1<<20)); err != nil || n != 1<<20 {
		t.Fatal(n, err)
	}
	if _, err := b.Write([]byte{1}); !errors.Is(err, ErrMonitor) || !b.overflow || ctx.Err() == nil {
		t.Fatal(err, b.overflow, ctx.Err())
	}
	if len(b.data) != 1<<20 || cap(b.data) > 1<<20 {
		t.Fatal(len(b.data), cap(b.data))
	}
}
