package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manish96170/videofactory/domain"
)

func TestStringProp(t *testing.T) {
	props := map[string]any{"text": "hello"}
	v, err := stringProp(props, "text")
	if err != nil || v != "hello" {
		t.Fatalf("stringProp = %q, %v", v, err)
	}

	if _, err := stringProp(props, "missing"); err == nil {
		t.Fatalf("expected error for missing key")
	}
	if _, err := stringProp(map[string]any{"text": 5}, "text"); err == nil {
		t.Fatalf("expected error for wrong type")
	}
}

func TestStringPropOr(t *testing.T) {
	v, err := stringPropOr(map[string]any{}, "color", "#FFFFFF")
	if err != nil || v != "#FFFFFF" {
		t.Fatalf("stringPropOr default = %q, %v", v, err)
	}

	v, err = stringPropOr(map[string]any{"color": "#000000"}, "color", "#FFFFFF")
	if err != nil || v != "#000000" {
		t.Fatalf("stringPropOr present = %q, %v", v, err)
	}

	if _, err := stringPropOr(map[string]any{"color": 5}, "color", "#FFFFFF"); err == nil {
		t.Fatalf("expected error for wrong type")
	}
}

func TestFloatPropOr(t *testing.T) {
	v, err := floatPropOr(map[string]any{}, "fontSize", 48)
	if err != nil || v != 48 {
		t.Fatalf("floatPropOr default = %v, %v", v, err)
	}
	v, err = floatPropOr(map[string]any{"fontSize": float64(60)}, "fontSize", 48)
	if err != nil || v != 60 {
		t.Fatalf("floatPropOr float64 = %v, %v", v, err)
	}
	v, err = floatPropOr(map[string]any{"fontSize": 60}, "fontSize", 48)
	if err != nil || v != 60 {
		t.Fatalf("floatPropOr int = %v, %v", v, err)
	}
	if _, err := floatPropOr(map[string]any{"fontSize": "big"}, "fontSize", 48); err == nil {
		t.Fatalf("expected error for wrong type")
	}
}

func TestParseHexColor(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"#000000", "0x000000", false},
		{"000000", "0x000000", false},
		{"#FF00AA", "0xFF00AA", false},
		{"", "", true},
		{"#FFF", "", true},
		{"not-a-color", "", true},
	}
	for _, tc := range cases {
		got, err := parseHexColor(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseHexColor(%q): expected error", tc.in)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("parseHexColor(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
		}
	}
}

func TestBoundsPropVariants(t *testing.T) {
	// absent
	b, err := boundsProp(map[string]any{}, "bounds")
	if err != nil || b != nil {
		t.Fatalf("absent bounds = %+v, %v", b, err)
	}

	// domain.Bounds value
	b, err = boundsProp(map[string]any{"bounds": domain.Bounds{X: 0.1, Y: 0.2, Width: 0.3, Height: 0.4}}, "bounds")
	if err != nil || b == nil || *b != (domain.Bounds{X: 0.1, Y: 0.2, Width: 0.3, Height: 0.4}) {
		t.Fatalf("domain.Bounds value = %+v, %v", b, err)
	}

	// *domain.Bounds pointer
	ptr := &domain.Bounds{X: 0.5, Y: 0.5, Width: 0.1, Height: 0.1}
	b, err = boundsProp(map[string]any{"bounds": ptr}, "bounds")
	if err != nil || b != ptr {
		t.Fatalf("*domain.Bounds value = %+v, %v", b, err)
	}

	// map[string]any (JSON-decoded shape)
	b, err = boundsProp(map[string]any{"bounds": map[string]any{"x": 0.0, "y": 0.0, "width": 1.0, "height": 1.0}}, "bounds")
	if err != nil || b == nil || *b != (domain.Bounds{X: 0, Y: 0, Width: 1, Height: 1}) {
		t.Fatalf("map bounds = %+v, %v", b, err)
	}

	// map missing a field
	if _, err := boundsProp(map[string]any{"bounds": map[string]any{"x": 0.0}}, "bounds"); err == nil {
		t.Fatalf("expected error for incomplete bounds map")
	}

	// wrong type entirely
	if _, err := boundsProp(map[string]any{"bounds": "nope"}, "bounds"); err == nil {
		t.Fatalf("expected error for wrong bounds type")
	}
}

func TestValidateBounds(t *testing.T) {
	valid := []domain.Bounds{
		{X: 0, Y: 0, Width: 1, Height: 1},
		{X: 0.5, Y: 0.5, Width: 0.5, Height: 0.5},
	}
	for _, b := range valid {
		if err := validateBounds(b); err != nil {
			t.Errorf("validateBounds(%+v) = %v, want nil", b, err)
		}
	}

	invalid := []domain.Bounds{
		{X: 0, Y: 0, Width: 0, Height: 1},
		{X: 0, Y: 0, Width: 1, Height: 0},
		{X: -0.1, Y: 0, Width: 0.5, Height: 0.5},
		{X: 0.6, Y: 0, Width: 0.5, Height: 0.5}, // exceeds frame
	}
	for _, b := range invalid {
		if err := validateBounds(b); err == nil {
			t.Errorf("validateBounds(%+v) = nil, want an error", b)
		}
	}
}

func TestBoundsToPixelBox(t *testing.T) {
	settings := Settings{Width: 1920, Height: 1080}

	x, y, w, h := boundsToPixelBox(nil, settings)
	if x != 0 || y != 0 || w != 1920 || h != 1080 {
		t.Fatalf("fullscreen box = %d,%d,%d,%d", x, y, w, h)
	}

	b := domain.Bounds{X: 0.5, Y: 0.5, Width: 0.25, Height: 0.25}
	x, y, w, h = boundsToPixelBox(&b, settings)
	if x != 960 || y != 540 {
		t.Fatalf("position = %d,%d, want 960,540", x, y)
	}
	if w%2 != 0 || h%2 != 0 {
		t.Fatalf("expected even width/height for yuv420p, got %d,%d", w, h)
	}
}

func TestWriteTempTextFileRoundTrip(t *testing.T) {
	// This is the regression coverage for the bug ADR-016's amendment
	// documents: a prior escapeDrawtext()-based "text='...'" approach
	// parsed without error for text containing an apostrophe, but
	// silently rendered empty text. Writing content through a file
	// (verified against real ffmpeg in render_test.go's
	// TestRenderShotText) sidesteps that whole class of bug: there is
	// no filtergraph text escaping left to get wrong.
	text := "Video Factory: it's a test!\nwith a second line too"
	path, cleanup, err := writeTempTextFile(text)
	if err != nil {
		t.Fatalf("writeTempTextFile: %v", err)
	}
	defer cleanup()

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != text {
		t.Fatalf("file content = %q, want %q", got, text)
	}
	if err := checkFilterGraphSafePath(path); err != nil {
		t.Fatalf("checkFilterGraphSafePath(%q): %v", path, err)
	}

	cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected cleanup to remove the temp file, stat error = %v", err)
	}
}

func TestPrepareSafeFontFileCopiesContentAndCleansUp(t *testing.T) {
	original := writeFixturePNG(t) // any existing file works for this test
	wantData, err := os.ReadFile(original)
	if err != nil {
		t.Fatalf("ReadFile(original): %v", err)
	}

	safePath, cleanup, err := prepareSafeFontFile(original)
	if err != nil {
		t.Fatalf("prepareSafeFontFile: %v", err)
	}
	defer cleanup()

	gotData, err := os.ReadFile(safePath)
	if err != nil {
		t.Fatalf("ReadFile(safePath): %v", err)
	}
	if string(gotData) != string(wantData) {
		t.Fatalf("copied content does not match original")
	}
	if err := checkFilterGraphSafePath(safePath); err != nil {
		t.Fatalf("checkFilterGraphSafePath(%q): %v", safePath, err)
	}

	cleanup()
	if _, err := os.Stat(safePath); !os.IsNotExist(err) {
		t.Fatalf("expected cleanup to remove the temp font directory, stat error = %v", err)
	}
}

func TestPrepareSafeFontFileNeutralizesSpecialCharactersInOriginalPath(t *testing.T) {
	// The original path (potentially caller-controlled, e.g. from
	// Settings.FontPath) contains filtergraph-special characters;
	// prepareSafeFontFile must never embed it directly, only a path it
	// generated itself.
	dir := t.TempDir()
	evilPath := filepath.Join(dir, "evil;name[v1].ttf")
	if err := os.WriteFile(evilPath, []byte("not a real font, just bytes"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	safePath, cleanup, err := prepareSafeFontFile(evilPath)
	if err != nil {
		t.Fatalf("prepareSafeFontFile: %v", err)
	}
	defer cleanup()

	if err := checkFilterGraphSafePath(safePath); err != nil {
		t.Fatalf("checkFilterGraphSafePath(%q): %v", safePath, err)
	}
	if strings.ContainsAny(safePath, ";[]") {
		t.Fatalf("expected the safe path to not contain the original's special characters, got %q", safePath)
	}
}

func TestCheckFilterGraphSafePathRejectsSpecialCharacters(t *testing.T) {
	for _, bad := range []string{"a:b", "a'b", `a\b`, "a;b", "a,b", "a[b", "a]b"} {
		if err := checkFilterGraphSafePath(bad); err == nil {
			t.Errorf("checkFilterGraphSafePath(%q) = nil, want an error", bad)
		}
	}
	if err := checkFilterGraphSafePath("/tmp/videofactory-font-abc123/font.ttf"); err != nil {
		t.Errorf("checkFilterGraphSafePath(a normal temp path) = %v, want nil", err)
	}
}
