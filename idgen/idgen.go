// Package idgen generates stable, time-sortable, collision-resistant
// identifiers for domain entities (PHASES.md Phase 01, "Stable IDs" /
// "Stable visual element IDs").
//
// IDs follow the ULID layout (https://github.com/ulid/spec): a 48-bit
// millisecond timestamp followed by 80 bits of randomness, the 128-bit
// result encoded as 26 characters of Crockford's Base32 with two leading
// zero padding bits (so the first character is always in the range
// '0'-'7'). This gives IDs that sort lexicographically by creation time,
// remain unique even when many are generated within the same
// millisecond, and are interoperable with standard ULID decoders.
//
// This package has no dependency on domain or any other videofactory
// package, so any future package (events, decisions, generation
// requests, ...) can use it for ID generation without import cycles.
package idgen

import (
	"crypto/rand"
	"fmt"
	"io"
	"strings"
	"time"
)

// crockford is Crockford's Base32 alphabet: no I, L, O, U, to avoid
// visual ambiguity and accidental profanity.
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

const (
	timestampBytes = 6  // 48 bits
	randomBytes    = 10 // 80 bits
	totalBytes     = timestampBytes + randomBytes
	totalBits      = totalBytes * 8 // 128
	groupBits      = 5
	// paddingBits is the number of zero bits ULID conceptually prepends
	// to the 128-bit payload so it packs evenly into 5-bit symbols
	// (128+2 = 130 = 26*5). This is why a standard ULID's first
	// character is always in the range '0'-'7'.
	paddingBits   = 2
	encodedLength = (paddingBits + totalBits) / groupBits // 26
)

// randReader is the entropy source for the random portion of an ID. It
// is a package variable (rather than a hardcoded crypto/rand.Reader
// call) solely so tests can inject a failing reader to exercise the
// error path in New/newAt; production code must never reassign it.
var randReader io.Reader = rand.Reader

// New returns a new ULID: a time-sortable, 26-character identifier.
//
// It is safe for concurrent use. It panics if the operating system's
// cryptographically secure random source is unavailable, since a
// predictable or all-zero fallback would silently defeat the collision
// resistance this package promises (see newAt for the rationale).
func New() string {
	id, err := newAt(time.Now())
	if err != nil {
		panic(fmt.Sprintf("idgen: %v", err))
	}
	return id
}

func newAt(t time.Time) (string, error) {
	var buf [totalBytes]byte

	ms := uint64(t.UnixMilli())
	for i := 0; i < timestampBytes; i++ {
		shift := 8 * (timestampBytes - 1 - i)
		buf[i] = byte(ms >> shift)
	}

	// A failing CSPRNG must not silently produce a predictable or
	// all-zero random segment: two IDs generated in the same
	// millisecond would then collide deterministically, defeating the
	// uniqueness this package promises. Surface the failure instead.
	if _, err := io.ReadFull(randReader, buf[timestampBytes:]); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}

	return encode(buf), nil
}

// encode renders buf (128 bits, big-endian) as the 26-character
// Crockford Base32 ULID encoding: 2 leading zero padding bits followed
// by the 128 data bits, 5 bits per character.
func encode(buf [totalBytes]byte) string {
	var sb strings.Builder
	sb.Grow(encodedLength)

	// First symbol: 2 implicit zero padding bits + the first 3 data
	// bits, so its value is always in [0,7].
	sb.WriteByte(crockford[readBits(buf, 0, groupBits-paddingBits)])

	// Every subsequent symbol maps directly onto 5 data bits, offset by
	// -paddingBits relative to its position in the padded bit stream.
	for g := 1; g < encodedLength; g++ {
		sb.WriteByte(crockford[readBits(buf, g*groupBits-paddingBits, groupBits)])
	}

	return sb.String()
}

// readBits reads the n bits (n <= 8) starting at bit offset start from a
// big-endian bit sequence formed by buf (buf[0] is the most significant
// byte, its bit 7 is the most significant bit). Bits at or past
// len(buf)*8 are treated as zero.
func readBits(buf [totalBytes]byte, start, n int) byte {
	var v byte
	for i := 0; i < n; i++ {
		bitPos := start + i
		byteIdx := bitPos / 8
		var bit byte
		if byteIdx < len(buf) {
			bitInByte := 7 - bitPos%8
			bit = (buf[byteIdx] >> bitInByte) & 1
		}
		v = v<<1 | bit
	}
	return v
}
