package artifact

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/manish96170/videofactory/domain"
)

// ErrInvalidInput identifies incomplete or malformed key input.
var ErrInvalidInput = errors.New("artifact: invalid key input")

// Key is a lowercase, 64-character SHA-256 hex digest produced by BuildKey.
// Its value has no mutable backing storage exposed to callers.
type Key string

// Digest is a SHA-256 content digest, not a path, asset ID, or upstream cache
// key. The zero value means missing evidence and is rejected by BuildKey.
type Digest [sha256.Size]byte

// Parameters declares only resolved pixel-affecting scalar settings. Names are
// exact and case-sensitive, and must be unique across the four maps. Numeric
// kinds are distinct: integer 1, number 1, and string "1" have different keys.
// Nil and empty maps are equivalent. Numbers must be finite; -0 equals +0.
//
// Callers must use stable semantic names, never correlation IDs or paths, and
// explicitly resolve renderer/provider defaults. Structured inputs are flattened
// into named settings (for example "bounds.x"), not encoded as raw JSON strings.
type Parameters struct {
	Strings  map[string]string
	Integers map[string]int64
	Numbers  map[string]float64
	Booleans map[string]bool
}

// PrimitiveInput is the pixel projection of one primitive. Slice order is
// composition order and affects the key. Primitive IDs are intentionally absent.
type PrimitiveInput struct {
	Type       domain.PrimitiveType
	Parameters Parameters
}

// ShotInput contains resolved visual content. Duration is media duration, not
// generation wall time. Parameters holds shot-level pixel/generation settings;
// planning labels, indices, dependency IDs, and ownership IDs do not belong here.
type ShotInput struct {
	Duration   time.Duration
	Parameters Parameters
	Primitives []PrimitiveInput
}

// RenderInput declares actual output settings. Revision identifies the rendering
// implementation/toolchain behavior; change it when that behavior changes.
// Parameters includes applicable encode/composition settings. Quality does not
// imply a resolution or supply any defaults. Font bytes belong in Sources.
type RenderInput struct {
	Revision   string
	Width      int
	Height     int
	FPS        float64
	Parameters Parameters
}

// Revision identifies an applicable implementation/model/profile and its exact
// revision. Name is a semantic implementation identity, not a request/project ID.
type Revision struct {
	Name     string
	Revision string
}

// KeyInput is an explicit pixel-input projection, not an execution request.
// Sources and Dependencies map stable semantic input roles (e.g. "font" or
// "reference-frame") to content digests. Role-to-content associations affect the
// key; map insertion order does not. Do not use asset/shot IDs as roles. Use
// distinct roles for distinct ordered inputs, even when their bytes match.
// Dependencies must cover every direct artifact input; transitive identity is
// carried by those artifacts' bytes, independently of invalidation events.
//
// A non-nil Seed/Provider/Model/Profile explicitly declares that fact applicable
// to pixels; nil excludes it. No applicability is inferred from a model name or
// deterministic flag. Seed "0" is valid and differs from no seed. Source digests
// are supplied by the caller, never inferred from asset metadata or paths.
type KeyInput struct {
	Quality      domain.Quality
	Shot         ShotInput
	Render       RenderInput
	Sources      map[string]Digest
	Dependencies map[string]Digest
	Seed         *string
	Provider     *Revision
	Model        *Revision
	Profile      *Revision
}

// Clone returns an independent deep copy, including all maps, slices, and
// optional pointers. It does not validate or normalize the input.
func (in KeyInput) Clone() KeyInput {
	in.Shot.Parameters = in.Shot.Parameters.clone()
	if in.Shot.Primitives != nil {
		primitives := make([]PrimitiveInput, len(in.Shot.Primitives))
		for i, p := range in.Shot.Primitives {
			primitives[i] = PrimitiveInput{Type: p.Type, Parameters: p.Parameters.clone()}
		}
		in.Shot.Primitives = primitives
	}
	in.Render.Parameters = in.Render.Parameters.clone()
	in.Sources = cloneMap(in.Sources)
	in.Dependencies = cloneMap(in.Dependencies)
	in.Seed = clonePointer(in.Seed)
	in.Provider = clonePointer(in.Provider)
	in.Model = clonePointer(in.Model)
	in.Profile = clonePointer(in.Profile)
	return in
}

func (p Parameters) clone() Parameters {
	return Parameters{
		Strings: cloneMap(p.Strings), Integers: cloneMap(p.Integers),
		Numbers: cloneMap(p.Numbers), Booleans: cloneMap(p.Booleans),
	}
}

func cloneMap[V any](in map[string]V) map[string]V {
	if in == nil {
		return nil
	}
	out := make(map[string]V, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func clonePointer[T any](in *T) *T {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

// BuildKey snapshots and validates input, then hashes its versioned canonical
// typed projection. It neither retains nor mutates caller state. Errors return
// an empty key, wrap ErrInvalidInput, and do not echo input values.
//
// Encoding v1 uses a length-prefixed schema marker, fixed field order, big-endian
// 64-bit integers/IEEE-754 numbers, one-byte booleans/presence markers, byte-length
// prefixed strings, and counted collections. Map entries sort by raw string key;
// sequences preserve order. The encoding has no JSON/map-iteration dependency.
func BuildKey(input KeyInput) (Key, error) {
	in := input.Clone()
	if err := validate(in); err != nil {
		return "", err
	}
	var c canonical
	c.text("videofactory/artifact-key/v1")
	c.text(string(in.Quality))
	c.uint(uint64(in.Shot.Duration))
	c.parameters(in.Shot.Parameters)
	c.uint(uint64(len(in.Shot.Primitives)))
	for _, p := range in.Shot.Primitives {
		c.text(string(p.Type))
		c.parameters(p.Parameters)
	}
	c.text(in.Render.Revision)
	c.uint(uint64(in.Render.Width))
	c.uint(uint64(in.Render.Height))
	c.number(in.Render.FPS)
	c.parameters(in.Render.Parameters)
	c.digests(in.Sources)
	c.digests(in.Dependencies)
	c.boolean(in.Seed != nil)
	if in.Seed != nil {
		c.text(*in.Seed)
	}
	c.revision(in.Provider)
	c.revision(in.Model)
	c.revision(in.Profile)
	sum := sha256.Sum256(c.Bytes())
	return Key(hex.EncodeToString(sum[:])), nil
}

func validate(in KeyInput) error {
	if in.Quality != domain.QualityDraft && in.Quality != domain.QualityFinal {
		return invalid("quality")
	}
	if in.Shot.Duration <= 0 {
		return invalid("shot duration")
	}
	if blank(in.Render.Revision) || in.Render.Width <= 0 || in.Render.Height <= 0 ||
		in.Render.FPS <= 0 || !finite(in.Render.FPS) {
		return invalid("render settings")
	}
	if err := validateParameters(in.Shot.Parameters); err != nil {
		return err
	}
	if err := validateParameters(in.Render.Parameters); err != nil {
		return err
	}
	for _, p := range in.Shot.Primitives {
		if blank(string(p.Type)) {
			return invalid("primitive type")
		}
		if err := validateParameters(p.Parameters); err != nil {
			return err
		}
	}
	for _, digests := range []map[string]Digest{in.Sources, in.Dependencies} {
		for name, digest := range digests {
			if blank(name) || digest == (Digest{}) {
				return invalid("content digest")
			}
		}
	}
	if in.Seed != nil && blank(*in.Seed) {
		return invalid("seed")
	}
	for _, r := range []*Revision{in.Provider, in.Model, in.Profile} {
		if r != nil && (blank(r.Name) || blank(r.Revision)) {
			return invalid("applicable revision")
		}
	}
	return nil
}

func validateParameters(p Parameters) error {
	seen := make(map[string]bool)
	for _, names := range [][]string{sortedKeys(p.Strings), sortedKeys(p.Integers), sortedKeys(p.Numbers), sortedKeys(p.Booleans)} {
		for _, name := range names {
			if blank(name) || seen[name] {
				return invalid("parameter name")
			}
			seen[name] = true
		}
	}
	for _, n := range p.Numbers {
		if !finite(n) {
			return invalid("parameter number")
		}
	}
	return nil
}

func invalid(field string) error { return fmt.Errorf("%w: %s", ErrInvalidInput, field) }
func blank(s string) bool        { return strings.TrimSpace(s) == "" }
func finite(n float64) bool      { return !math.IsNaN(n) && !math.IsInf(n, 0) }

type canonical struct{ bytes.Buffer }

func (c *canonical) uint(n uint64) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], n)
	c.Write(b[:])
}

func (c *canonical) text(s string) {
	c.uint(uint64(len(s)))
	c.WriteString(s)
}

func (c *canonical) number(n float64) {
	if n == 0 {
		n = 0 // Normalize IEEE-754 negative zero.
	}
	c.uint(math.Float64bits(n))
}

func (c *canonical) boolean(v bool) {
	if v {
		c.WriteByte(1)
	} else {
		c.WriteByte(0)
	}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (c *canonical) parameters(p Parameters) {
	c.uint(uint64(len(p.Strings)))
	for _, k := range sortedKeys(p.Strings) {
		c.text(k)
		c.text(p.Strings[k])
	}
	c.uint(uint64(len(p.Integers)))
	for _, k := range sortedKeys(p.Integers) {
		c.text(k)
		c.uint(uint64(p.Integers[k]))
	}
	c.uint(uint64(len(p.Numbers)))
	for _, k := range sortedKeys(p.Numbers) {
		c.text(k)
		c.number(p.Numbers[k])
	}
	c.uint(uint64(len(p.Booleans)))
	for _, k := range sortedKeys(p.Booleans) {
		c.text(k)
		c.boolean(p.Booleans[k])
	}
}

func (c *canonical) digests(digests map[string]Digest) {
	c.uint(uint64(len(digests)))
	for _, k := range sortedKeys(digests) {
		c.text(k)
		d := digests[k]
		c.Write(d[:])
	}
}

func (c *canonical) revision(r *Revision) {
	c.boolean(r != nil)
	if r != nil {
		c.text(r.Name)
		c.text(r.Revision)
	}
}
