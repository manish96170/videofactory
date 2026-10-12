package artifact_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/manish96170/videofactory/artifact"
	"github.com/manish96170/videofactory/domain"
)

func pointer[T any](v T) *T { return &v }

func digest(s string) artifact.Digest { return artifact.Digest(sha256.Sum256([]byte(s))) }

func minimal() artifact.KeyInput {
	return artifact.KeyInput{
		Quality: domain.QualityDraft,
		Shot:    artifact.ShotInput{Duration: 2 * time.Second},
		Render:  artifact.RenderInput{Revision: "renderer-v1", Width: 640, Height: 360, FPS: 24},
	}
}

func fixture() artifact.KeyInput {
	in := minimal()
	in.Shot.Parameters = artifact.Parameters{
		Strings:  map[string]string{"prompt": "a scene", "style": "flat"},
		Integers: map[string]int64{"steps": 12, "frames": 48},
		Numbers:  map[string]float64{"strength": 0.5, "guidance": 7.5},
		Booleans: map[string]bool{"loop": true, "transparent": false},
	}
	in.Shot.Primitives = []artifact.PrimitiveInput{
		{Type: domain.PrimitiveTypeBackground, Parameters: artifact.Parameters{Strings: map[string]string{"color": "#000000"}}},
		{Type: domain.PrimitiveTypeText, Parameters: artifact.Parameters{
			Strings:  map[string]string{"text": "hello", "color": "#ffffff"},
			Integers: map[string]int64{"fontSize": 48},
			Numbers:  map[string]float64{"bounds.x": 0.25},
			Booleans: map[string]bool{"outline": false},
		}},
	}
	in.Render.Parameters = artifact.Parameters{
		Strings:  map[string]string{"codec": "h264", "pixel-format": "yuv420p"},
		Integers: map[string]int64{"crf": 18, "threads": 1},
		Numbers:  map[string]float64{"scale": 1, "gamma": 2.2},
		Booleans: map[string]bool{"dither": true, "interlace": false},
	}
	in.Sources = map[string]artifact.Digest{"font": digest("font bytes"), "image": digest("image bytes")}
	in.Dependencies = map[string]artifact.Digest{"foreground": digest("foreground clip"), "background": digest("background clip")}
	in.Seed = pointer("0")
	in.Provider = &artifact.Revision{Name: "provider", Revision: "v1"}
	in.Model = &artifact.Revision{Name: "model", Revision: "v2"}
	in.Profile = &artifact.Revision{Name: "profile", Revision: "v3"}
	return in
}

func key(t *testing.T, in artifact.KeyInput) artifact.Key {
	t.Helper()
	k, err := artifact.BuildKey(in)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestKeyCanonicalSHA256Vector(t *testing.T) {
	// Independent vector: packed big-endian typed fields, including empty
	// collection counts and four absent optional facts. Not derived by BuildKey.
	const want = "ccd8397598a2238a1e7782574acd6c55afc38e491b275ff56afec8442bd6718b"
	got := key(t, minimal())
	if string(got) != want {
		t.Fatalf("canonical encoding drift: %s, want %s", got, want)
	}
	decoded, err := hex.DecodeString(string(got))
	if err != nil || len(decoded) != sha256.Size {
		t.Fatalf("invalid SHA-256 key: %s", got)
	}
}

func orderedMap[V any](in map[string]V, descending bool) map[string]V {
	// Explicitly construct both insertion orders rather than hoping a map range
	// or a fixture literal happens to give them different orders.
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	// A tiny insertion sort keeps this helper independent of production sorting.
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] > keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	out := make(map[string]V, len(in))
	for i := range keys {
		j := i
		if !descending {
			j = len(keys) - 1 - i
		}
		out[keys[j]] = in[keys[j]]
	}
	return out
}

func orderedParameters(p artifact.Parameters, descending bool) artifact.Parameters {
	return artifact.Parameters{Strings: orderedMap(p.Strings, descending), Integers: orderedMap(p.Integers, descending),
		Numbers: orderedMap(p.Numbers, descending), Booleans: orderedMap(p.Booleans, descending)}
}

func TestKeyMapInsertionOrder(t *testing.T) {
	a := fixture()
	b := fixture()
	for i, in := range []*artifact.KeyInput{&a, &b} {
		descending := i == 1
		in.Shot.Parameters = orderedParameters(in.Shot.Parameters, descending)
		in.Render.Parameters = orderedParameters(in.Render.Parameters, descending)
		for j := range in.Shot.Primitives {
			in.Shot.Primitives[j].Parameters = orderedParameters(in.Shot.Primitives[j].Parameters, descending)
		}
		in.Sources = orderedMap(in.Sources, descending)
		in.Dependencies = orderedMap(in.Dependencies, descending)
	}
	want := key(t, a)
	for i := 0; i < 50; i++ {
		if got := key(t, b); got != want {
			t.Fatalf("insertion/range order changed key: %s != %s", got, want)
		}
	}
}

func TestKeyPixelSensitivity(t *testing.T) {
	cases := map[string]func(*artifact.KeyInput){
		"quality":           func(in *artifact.KeyInput) { in.Quality = domain.QualityFinal },
		"duration":          func(in *artifact.KeyInput) { in.Shot.Duration++ },
		"shot string":       func(in *artifact.KeyInput) { in.Shot.Parameters.Strings["prompt"] += "!" },
		"shot integer":      func(in *artifact.KeyInput) { in.Shot.Parameters.Integers["steps"]++ },
		"shot number":       func(in *artifact.KeyInput) { in.Shot.Parameters.Numbers["strength"] = 0.6 },
		"shot boolean":      func(in *artifact.KeyInput) { in.Shot.Parameters.Booleans["loop"] = false },
		"primitive type":    func(in *artifact.KeyInput) { in.Shot.Primitives[1].Type = domain.PrimitiveTypeSubtitle },
		"primitive text":    func(in *artifact.KeyInput) { in.Shot.Primitives[1].Parameters.Strings["text"] += "!" },
		"primitive integer": func(in *artifact.KeyInput) { in.Shot.Primitives[1].Parameters.Integers["fontSize"]++ },
		"primitive bounds":  func(in *artifact.KeyInput) { in.Shot.Primitives[1].Parameters.Numbers["bounds.x"] = 0.5 },
		"primitive boolean": func(in *artifact.KeyInput) { in.Shot.Primitives[1].Parameters.Booleans["outline"] = true },
		"primitive order": func(in *artifact.KeyInput) {
			in.Shot.Primitives[0], in.Shot.Primitives[1] = in.Shot.Primitives[1], in.Shot.Primitives[0]
		},
		"primitive count": func(in *artifact.KeyInput) { in.Shot.Primitives = in.Shot.Primitives[:1] },
		"render revision": func(in *artifact.KeyInput) { in.Render.Revision = "renderer-v2" },
		"width":           func(in *artifact.KeyInput) { in.Render.Width++ },
		"height":          func(in *artifact.KeyInput) { in.Render.Height++ },
		"fps":             func(in *artifact.KeyInput) { in.Render.FPS = 23.976 },
		"render string":   func(in *artifact.KeyInput) { in.Render.Parameters.Strings["codec"] = "h265" },
		"render integer":  func(in *artifact.KeyInput) { in.Render.Parameters.Integers["crf"]++ },
		"render number":   func(in *artifact.KeyInput) { in.Render.Parameters.Numbers["gamma"] = 2.4 },
		"render boolean":  func(in *artifact.KeyInput) { in.Render.Parameters.Booleans["dither"] = false },
		"source bytes":    func(in *artifact.KeyInput) { in.Sources["font"] = digest("new font bytes") },
		"source binding": func(in *artifact.KeyInput) {
			in.Sources["font"], in.Sources["image"] = in.Sources["image"], in.Sources["font"]
		},
		"dependency bytes": func(in *artifact.KeyInput) { in.Dependencies["foreground"] = digest("new clip bytes") },
		"dependency binding": func(in *artifact.KeyInput) {
			in.Dependencies["foreground"], in.Dependencies["background"] = in.Dependencies["background"], in.Dependencies["foreground"]
		},
		"dependency removal":     func(in *artifact.KeyInput) { delete(in.Dependencies, "background") },
		"seed":                   func(in *artifact.KeyInput) { *in.Seed = "1" },
		"seed applicability":     func(in *artifact.KeyInput) { in.Seed = nil },
		"provider identity":      func(in *artifact.KeyInput) { in.Provider.Name += "-other" },
		"provider revision":      func(in *artifact.KeyInput) { in.Provider.Revision = "v2" },
		"provider applicability": func(in *artifact.KeyInput) { in.Provider = nil },
		"model identity":         func(in *artifact.KeyInput) { in.Model.Name += "-other" },
		"model revision":         func(in *artifact.KeyInput) { in.Model.Revision = "v3" },
		"model applicability":    func(in *artifact.KeyInput) { in.Model = nil },
		"profile identity":       func(in *artifact.KeyInput) { in.Profile.Name += "-other" },
		"profile revision":       func(in *artifact.KeyInput) { in.Profile.Revision = "v4" },
		"profile applicability":  func(in *artifact.KeyInput) { in.Profile = nil },
	}
	want := key(t, fixture())
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			in := fixture()
			change(&in)
			if got := key(t, in); got == want {
				t.Fatal("pixel-affecting change reused key")
			}
		})
	}
}

func TestKeyCanonicalEquivalencesAndTypeBoundaries(t *testing.T) {
	a, b := minimal(), minimal()
	b.Sources = map[string]artifact.Digest{}
	b.Dependencies = map[string]artifact.Digest{}
	b.Shot.Primitives = []artifact.PrimitiveInput{}
	b.Shot.Parameters = artifact.Parameters{Strings: map[string]string{}, Integers: map[string]int64{}, Numbers: map[string]float64{}, Booleans: map[string]bool{}}
	b.Render.Parameters = b.Shot.Parameters
	if key(t, a) != key(t, b) {
		t.Fatal("nil and empty collections differ")
	}
	a.Shot.Parameters.Numbers = map[string]float64{"offset": 0}
	b.Shot.Parameters.Numbers = map[string]float64{"offset": math.Copysign(0, -1)}
	if key(t, a) != key(t, b) {
		t.Fatal("signed zeros differ")
	}
	params := []artifact.Parameters{
		{Strings: map[string]string{"value": "1"}},
		{Integers: map[string]int64{"value": 1}},
		{Numbers: map[string]float64{"value": 1}},
		{Booleans: map[string]bool{"value": true}},
		{Integers: map[string]int64{"value": 1 << 53}},
		{Integers: map[string]int64{"value": 1<<53 + 1}},
		{Integers: map[string]int64{"value": -1}},
		{Strings: map[string]string{"a": "bc"}},
		{Strings: map[string]string{"ab": "c"}},
		{Strings: map[string]string{"a": "b\x00c"}},
		{Strings: map[string]string{"a\x00b": "c"}},
	}
	seen := make(map[artifact.Key]bool)
	for i, p := range params {
		in := minimal()
		in.Shot.Parameters = p
		k := key(t, in)
		if seen[k] {
			t.Fatalf("ambiguous typed or length-prefixed encoding at %d", i)
		}
		seen[k] = true
	}
	// Identical bytes in different input namespaces cannot collapse.
	a, b = minimal(), minimal()
	a.Sources = map[string]artifact.Digest{"input": digest("bytes")}
	b.Dependencies = map[string]artifact.Digest{"input": digest("bytes")}
	if key(t, a) == key(t, b) {
		t.Fatal("source and dependency namespaces collapsed")
	}
}

func TestKeyOperationalMetadataIsOutsideProjection(t *testing.T) {
	// A caller can decode a pixel projection accompanied by operational fields.
	// Those fields have no representation in KeyInput. The API consumes only the
	// explicit typed projection, never hashes this original JSON/envelope.
	pixels, err := json.Marshal(fixture())
	if err != nil {
		t.Fatal(err)
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(pixels, &envelope); err != nil {
		t.Fatal(err)
	}
	for _, revision := range []string{"first", "second"} {
		for _, field := range []string{"ID", "ProjectID", "ShotID", "RequestID", "OutputPath", "CreatedAt", "UpdatedAt", "DurationMs", "PeakMemoryMB", "Measurement", "Events", "Feedback"} {
			envelope[field], _ = json.Marshal(revision)
		}
		encoded, err := json.MarshalIndent(envelope, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		var in artifact.KeyInput
		if err := json.Unmarshal(encoded, &in); err != nil {
			t.Fatal(err)
		}
		if got, want := key(t, in), key(t, fixture()); got != want {
			t.Fatalf("operational metadata changed key: %s != %s", got, want)
		}
	}
}

func TestKeyOwnership(t *testing.T) {
	original := fixture()
	want := fixture() // Independent literal oracle; not a production Clone.
	snapshot := original.Clone()
	k := key(t, original)
	if !reflect.DeepEqual(original, want) {
		t.Fatal("BuildKey mutated input")
	}
	mutate := func(in *artifact.KeyInput) {
		for _, p := range []*artifact.Parameters{&in.Shot.Parameters, &in.Render.Parameters, &in.Shot.Primitives[1].Parameters} {
			for name := range p.Strings {
				p.Strings[name] = "changed"
			}
			for name := range p.Integers {
				p.Integers[name] = 99
			}
			for name := range p.Numbers {
				p.Numbers[name] = 99
			}
			for name := range p.Booleans {
				p.Booleans[name] = !p.Booleans[name]
			}
		}
		in.Shot.Primitives[0].Type = domain.PrimitiveTypeImage
		in.Shot.Primitives[0].Parameters.Strings["color"] = "#ff0000"
		in.Sources["font"] = digest("mutated")
		in.Dependencies["foreground"] = digest("mutated")
		*in.Seed = "99"
		in.Provider.Revision = "changed"
		in.Model.Name = "changed"
		in.Profile.Revision = "changed"
	}
	mutate(&original)
	if !reflect.DeepEqual(snapshot, want) || key(t, snapshot) != k {
		t.Fatal("input mutation reached snapshot or returned key")
	}
	original = fixture()
	snapshot = original.Clone()
	mutate(&snapshot)
	if !reflect.DeepEqual(original, want) || key(t, original) != k {
		t.Fatal("output snapshot mutation reached input")
	}
	// Even mutable byte views obtained by callers cannot mutate a Key string.
	view := []byte(k)
	view[0] ^= 1
	if k != key(t, fixture()) {
		t.Fatal("returned key aliases mutable bytes")
	}
}

func TestKeyRejectsMalformedInput(t *testing.T) {
	cases := map[string]func(*artifact.KeyInput){
		"quality":                   func(in *artifact.KeyInput) { in.Quality = "unknown" },
		"zero duration":             func(in *artifact.KeyInput) { in.Shot.Duration = 0 },
		"negative duration":         func(in *artifact.KeyInput) { in.Shot.Duration = -1 },
		"revision":                  func(in *artifact.KeyInput) { in.Render.Revision = " " },
		"width":                     func(in *artifact.KeyInput) { in.Render.Width = 0 },
		"height":                    func(in *artifact.KeyInput) { in.Render.Height = -1 },
		"fps zero":                  func(in *artifact.KeyInput) { in.Render.FPS = 0 },
		"fps nan":                   func(in *artifact.KeyInput) { in.Render.FPS = math.NaN() },
		"fps inf":                   func(in *artifact.KeyInput) { in.Render.FPS = math.Inf(1) },
		"shot number":               func(in *artifact.KeyInput) { in.Shot.Parameters.Numbers["strength"] = math.NaN() },
		"render number":             func(in *artifact.KeyInput) { in.Render.Parameters.Numbers["scale"] = math.Inf(-1) },
		"primitive number":          func(in *artifact.KeyInput) { in.Shot.Primitives[1].Parameters.Numbers["bounds.x"] = math.Inf(1) },
		"primitive type":            func(in *artifact.KeyInput) { in.Shot.Primitives[0].Type = " " },
		"blank setting":             func(in *artifact.KeyInput) { in.Shot.Parameters.Strings[" "] = "secret" },
		"ambiguous setting":         func(in *artifact.KeyInput) { in.Render.Parameters.Numbers["codec"] = 1 },
		"missing source digest":     func(in *artifact.KeyInput) { in.Sources["font"] = artifact.Digest{} },
		"missing dependency digest": func(in *artifact.KeyInput) { in.Dependencies["foreground"] = artifact.Digest{} },
		"blank source role":         func(in *artifact.KeyInput) { in.Sources[" "] = digest("bytes") },
		"blank dependency role":     func(in *artifact.KeyInput) { in.Dependencies[""] = digest("bytes") },
		"empty seed":                func(in *artifact.KeyInput) { *in.Seed = "" },
		"provider name":             func(in *artifact.KeyInput) { in.Provider.Name = "" },
		"provider revision":         func(in *artifact.KeyInput) { in.Provider.Revision = " " },
		"model name":                func(in *artifact.KeyInput) { in.Model.Name = "" },
		"model revision":            func(in *artifact.KeyInput) { in.Model.Revision = " " },
		"profile name":              func(in *artifact.KeyInput) { in.Profile.Name = "" },
		"profile revision":          func(in *artifact.KeyInput) { in.Profile.Revision = " " },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			in := fixture()
			change(&in)
			got, err := artifact.BuildKey(in)
			if !errors.Is(err, artifact.ErrInvalidInput) || got != "" {
				t.Fatalf("got key %q, error %v", got, err)
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatal("error disclosed input value")
			}
		})
	}
	if k, err := artifact.BuildKey(artifact.KeyInput{}); k != "" || !errors.Is(err, artifact.ErrInvalidInput) {
		t.Fatalf("zero input: %q, %v", k, err)
	}
}

func TestKeyConcurrentStableInputs(t *testing.T) {
	in := fixture()
	want := key(t, in)
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				got, err := artifact.BuildKey(in)
				if err != nil || got != want {
					t.Errorf("concurrent key: %q, %v", got, err)
					return
				}
			}
		}()
	}
	wg.Wait()
}
