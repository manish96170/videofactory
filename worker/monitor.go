package worker

import (
	"bytes"
	"context"
	"io"
	"math"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/manish96170/videofactory/resource"
)

type ProcessGroup struct{ ID int }

// Sample is a native observation, not a reply DTO. Live includes stopped or
// sleeping members that still hold resources; Zombies are separately counted.
// RSSBytes is nil when there are no live members (not a fabricated zero peak).
type Sample struct {
	Live     int
	Zombies  int
	RSSBytes *int64
	Source   resource.MeasurementSource
}

// Monitor callbacks run outside runner locks. Returned observations must remain
// immutable (including RSSBytes); implementations must not mutate them later.
type Monitor interface {
	Sample(context.Context, ProcessGroup) (Sample, error)
}

// NativeMonitor uses a bounded, direct ps query with no command strings or
// environment output. Zero QueryTimeout chooses 100ms. ps is a system dependency.
type NativeMonitor struct{ QueryTimeout time.Duration }

func (m NativeMonitor) Sample(ctx context.Context, group ProcessGroup) (Sample, error) {
	if ctx == nil || !validGroup(group) || m.QueryTimeout < 0 {
		return Sample{}, ErrMonitor
	}
	if err := ctx.Err(); err != nil {
		return Sample{}, err
	}
	if !platformSupported() {
		return Sample{}, ErrUnsupported
	}
	timeout := m.QueryTimeout
	if timeout == 0 {
		timeout = 100 * time.Millisecond
	}
	query, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(query, "ps", "-axo", "pid=,pgid=,rss=,stat=")
	cmd.WaitDelay = 100 * time.Millisecond
	buf := &queryBuffer{cancel: cancel}
	cmd.Stdout = buf
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil || query.Err() != nil || buf.overflow {
		return Sample{}, ErrMonitor
	}
	s, err := parsePS(buf.data, group)
	if ctx.Err() != nil {
		return Sample{}, ctx.Err()
	}
	return s, err
}

type queryBuffer struct {
	data     []byte
	overflow bool
	cancel   context.CancelFunc
}

func (b *queryBuffer) Write(p []byte) (int, error) {
	const max = 1 << 20
	if len(p) > max-len(b.data) {
		b.overflow = true
		b.cancel()
		return 0, ErrMonitor
	}
	b.data = appendCapture(b.data, p, max)
	return len(p), nil
}

func parsePS(data []byte, group ProcessGroup) (Sample, error) {
	var s Sample
	var total int64
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		fields := strings.Fields(string(line))
		if len(fields) == 0 {
			continue
		}
		if len(fields) != 4 {
			return Sample{}, ErrMonitor
		}
		pid, e1 := strconv.ParseInt(fields[0], 10, 64)
		pgid, e2 := strconv.ParseInt(fields[1], 10, 64)
		if e1 != nil || e2 != nil || pid <= 0 || pgid < 0 {
			return Sample{}, ErrMonitor
		}
		if pgid != int64(group.ID) {
			// ps is a system-wide snapshot; unrelated processes can have
			// platform-specific/unknown status and RSS representations. Only
			// worker-family rows are evidence for this monitor.
			continue
		}
		rss, e3 := strconv.ParseInt(fields[2], 10, 64)
		if e3 != nil || rss < 0 || len(fields[3]) == 0 {
			return Sample{}, ErrMonitor
		}
		state := fields[3][0]
		if state == 'Z' || state == 'X' {
			s.Zombies++
			continue
		}
		if !strings.ContainsRune("RSDTIWUPE", rune(state)) {
			return Sample{}, ErrMonitor
		}
		value, ok := normalizePeak(rss, true)
		if !ok || total > math.MaxInt64-value {
			return Sample{}, ErrMonitor
		}
		total += value
		s.Live++
	}
	if s.Live > 0 {
		s.RSSBytes = &total
		s.Source = resource.SourceNativeFamilyRSS
	}
	return s, nil
}

func validateSample(s Sample) error {
	if s.Live < 0 || s.Zombies < 0 {
		return ErrMonitor
	}
	if s.Live > 0 && (s.RSSBytes == nil || *s.RSSBytes < 0 || s.Source != resource.SourceNativeFamilyRSS) {
		return ErrMonitor
	}
	if s.RSSBytes != nil && (*s.RSSBytes < 0 || s.Source != resource.SourceNativeFamilyRSS) {
		return ErrMonitor
	}
	return nil
}
