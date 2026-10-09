package idgen

import (
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func TestNewLengthAndAlphabet(t *testing.T) {
	id := New()
	if len(id) != encodedLength {
		t.Fatalf("len(New()) = %d, want %d", len(id), encodedLength)
	}
	for _, c := range id {
		if !strings.ContainsRune(crockford, c) {
			t.Fatalf("New() contains character %q outside the Crockford Base32 alphabet", c)
		}
	}
}

func TestNewFirstCharacterIsWithinULIDRange(t *testing.T) {
	// A standard ULID's first character encodes 2 zero padding bits
	// plus the 3 most significant bits of the 48-bit timestamp, so it
	// must always be one of '0'-'7' (i.e. index 0-7 in crockford, which
	// is itself ordered '0'-'9' first).
	for i := 0; i < 1000; i++ {
		id := New()
		if id[0] < '0' || id[0] > '7' {
			t.Fatalf("New()[0] = %q, want a character in '0'-'7' (ULID padding invariant)", id[0])
		}
	}
}

func TestNewIsUniqueAcrossManyCalls(t *testing.T) {
	const n = 10000
	seen := make(map[string]struct{}, n)
	for i := 0; i < n; i++ {
		id := New()
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate ID generated: %s", id)
		}
		seen[id] = struct{}{}
	}
}

func TestNewAtIsLexicographicallySortableByTime(t *testing.T) {
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	a, err := newAt(base)
	if err != nil {
		t.Fatalf("newAt: %v", err)
	}
	b, err := newAt(base.Add(time.Millisecond))
	if err != nil {
		t.Fatalf("newAt: %v", err)
	}
	c, err := newAt(base.Add(time.Second))
	if err != nil {
		t.Fatalf("newAt: %v", err)
	}

	if !(a < b && b < c) {
		t.Fatalf("expected a < b < c, got a=%s b=%s c=%s", a, b, c)
	}
}

func TestNewAtSameMillisecondDiffersByRandomness(t *testing.T) {
	fixed := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	a, err := newAt(fixed)
	if err != nil {
		t.Fatalf("newAt: %v", err)
	}
	b, err := newAt(fixed)
	if err != nil {
		t.Fatalf("newAt: %v", err)
	}
	if a == b {
		t.Fatalf("expected two IDs generated at the same millisecond to differ due to randomness, got identical IDs %s", a)
	}
	// With standard ULID padding, the 48-bit timestamp plus the 2
	// padding bits occupy exactly 50 bits = the first 10 characters,
	// with zero bits of randomness mixed in; randomness starts at
	// character 11. So two IDs minted in the same millisecond must
	// share their first 10 characters exactly.
	if a[:10] != b[:10] {
		t.Fatalf("expected identical-millisecond IDs to share a 10-character timestamp prefix: %s vs %s", a[:10], b[:10])
	}
}

// TestEncodeKnownVector pins the encoding against a hand-computed
// vector, so a future refactor of encode/readBits can't silently drift
// away from standard ULID bit packing without failing a test.
//
// buf (48-bit timestamp = 0x0000_0000_0001, i.e. ms=1; 80-bit
// randomness = all zero) should encode, 5 bits at a time over the
// padded 130-bit stream (2 zero pad bits + 128 data bits), to:
//
//	bits: 00 000000000000000000000000000000000000000001 000...0 (80 zero random bits)
//	groups of 5, as Crockford digits: all zero except the group
//	covering the single set timestamp bit.
func TestEncodeKnownVector(t *testing.T) {
	var buf [totalBytes]byte
	buf[5] = 1 // timestamp = 1ms, all other timestamp bytes zero, random all zero

	got := encode(buf)
	if len(got) != encodedLength {
		t.Fatalf("len(encode(buf)) = %d, want %d", len(got), encodedLength)
	}
	// Every character must be '0' except the last character of the
	// timestamp-only region, which carries the single set bit. The
	// timestamp is 48 bits; with 2 padding bits, the timestamp spans
	// padded bit positions 2-49, i.e. characters 0-9 (0-indexed,
	// 10 characters covering bits 0-49). Bit 49 (the last timestamp
	// bit, value 1) falls in character 9 (bits 45-49).
	for i, c := range got {
		if i == 9 {
			continue // checked separately below
		}
		if c != '0' {
			t.Fatalf("encode(buf)[%d] = %q, want '0' (buf has only the lowest timestamp bit set)", i, c)
		}
	}
	if got[9] != '1' {
		t.Fatalf("encode(buf)[9] = %q, want '1' (the lowest timestamp bit)", got[9])
	}
}

func TestNewPropagatesEntropyFailure(t *testing.T) {
	original := randReader
	defer func() { randReader = original }()

	randReader = failingReader{}

	_, err := newAt(time.Now())
	if err == nil {
		t.Fatalf("expected newAt to return an error when the entropy source fails")
	}
}

func TestNewPanicsOnEntropyFailure(t *testing.T) {
	original := randReader
	defer func() { randReader = original }()
	randReader = failingReader{}

	defer func() {
		if recover() == nil {
			t.Fatalf("expected New() to panic when the entropy source fails")
		}
	}()
	New()
}

type failingReader struct{}

func (failingReader) Read(p []byte) (int, error) {
	return 0, errors.New("simulated entropy failure")
}

var _ io.Reader = failingReader{}

func TestReadBitsKnownVector(t *testing.T) {
	var buf [totalBytes]byte
	buf[0] = 0b10110100 // first 5 bits: 10110 = 0x16 = 22

	got := readBits(buf, 0, 5)
	if got != 22 {
		t.Fatalf("readBits(0,5) = %d, want 22", got)
	}

	// Bits 5-9 span buf[0] (last 3 bits: 100) and buf[1] (first 2 bits,
	// which are zero here): 100 00 = 0b10000 = 16.
	got = readBits(buf, 5, 5)
	if got != 16 {
		t.Fatalf("readBits(5,5) = %d, want 16", got)
	}
}

func TestReadBitsPastEndOfBufferIsZero(t *testing.T) {
	var buf [totalBytes]byte
	for i := range buf {
		buf[i] = 0xFF
	}
	// Request bits entirely beyond len(buf)*8; must not panic and must
	// return 0.
	got := readBits(buf, totalBytes*8, 5)
	if got != 0 {
		t.Fatalf("readBits past end of buffer = %d, want 0", got)
	}
}
