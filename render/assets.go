package render

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

// checkAssetFile validates that path exists and is a regular,
// non-empty file before we hand it to ffmpeg, so a missing/invalid
// asset produces a clear error from this package instead of an opaque
// ffmpeg failure.
func checkAssetFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("asset file %q: %w", path, err)
	}
	if info.IsDir() {
		return fmt.Errorf("asset file %q is a directory, not a file", path)
	}
	if info.Size() == 0 {
		return fmt.Errorf("asset file %q is empty", path)
	}
	return nil
}

// filterGraphSafePath matches a path containing none of the characters
// that are special in ffmpeg's filtergraph description syntax
// (':', '\”, '\\', ';', ',', '[', ']'), so it can be embedded directly
// into a filter_complex string with no escaping at all. Go's
// os.CreateTemp/os.MkdirTemp paths are composed of the OS temp
// directory plus a simple alphanumeric random suffix, so they satisfy
// this in practice; the check exists so a pathological environment
// (an unusual TMPDIR) fails loudly instead of silently producing an
// unsafe filtergraph string. See DECISIONS.md ADR-016's amendment for
// why caller-controlled strings (text content, FontPath) are kept out
// of the filtergraph text entirely rather than escaped in place.
var filterGraphSafePath = regexp.MustCompile(`^[^:'\\;,\[\]]+$`)

func checkFilterGraphSafePath(path string) error {
	if !filterGraphSafePath.MatchString(path) {
		return fmt.Errorf("render: generated temp path %q contains characters unsafe to embed in an ffmpeg filtergraph (check TMPDIR); refusing to proceed", path)
	}
	return nil
}

// writeTempTextFile writes text to a new temp file for use with
// drawtext's "textfile" option, which (unlike "text") reads its
// content from a file and therefore needs no text-content escaping at
// all.
func writeTempTextFile(text string) (path string, cleanup func(), err error) {
	f, err := os.CreateTemp("", "videofactory-text-*.txt")
	if err != nil {
		return "", nil, fmt.Errorf("create temp text file: %w", err)
	}
	cleanup = func() { os.Remove(f.Name()) }

	if err := checkFilterGraphSafePath(f.Name()); err != nil {
		cleanup()
		return "", nil, err
	}

	if _, err := f.WriteString(text); err != nil {
		f.Close()
		cleanup()
		return "", nil, fmt.Errorf("write temp text file: %w", err)
	}
	if err := f.Close(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("close temp text file: %w", err)
	}
	return f.Name(), cleanup, nil
}

// prepareSafeFontFile copies the font at fontPath to a new temp file
// whose path is guaranteed safe to embed directly into an ffmpeg
// filtergraph string (checkFilterGraphSafePath), regardless of what
// fontPath itself looks like. This neutralizes filtergraph-injection
// risk from a caller-controlled FontPath by construction (copy to a
// known-safe location) rather than by escaping the original string.
func prepareSafeFontFile(fontPath string) (path string, cleanup func(), err error) {
	data, err := os.ReadFile(fontPath)
	if err != nil {
		return "", nil, fmt.Errorf("read font file %q: %w", fontPath, err)
	}

	dir, err := os.MkdirTemp("", "videofactory-font-*")
	if err != nil {
		return "", nil, fmt.Errorf("create temp font directory: %w", err)
	}
	cleanup = func() { os.RemoveAll(dir) }

	safePath := filepath.Join(dir, "font.ttf")
	if err := checkFilterGraphSafePath(safePath); err != nil {
		cleanup()
		return "", nil, err
	}

	if err := os.WriteFile(safePath, data, 0o644); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("write temp font file: %w", err)
	}
	return safePath, cleanup, nil
}
