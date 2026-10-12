package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseConfigStrictSchema(t *testing.T) {
	for _, input := range []string{
		`{"version":1}`, ` {"tab":"shots","version":1} `,
	} {
		got, err := ParseConfig([]byte(input))
		if err != nil || got.Version != 1 {
			t.Fatalf("ParseConfig(%q) = %+v, %v", input, got, err)
		}
	}
	for _, input := range []string{
		``, `{}`, `null`, `[]`, `1`, `{"tab":"shots"}`,
		`{"version":0}`, `{"version":2}`, `{"version":1.0}`,
		`{"version":"1"}`, `{"version":null}`, `{"version":true}`,
		`{"version":1,"tab":null}`, `{"version":1,"tab":""}`,
		`{"version":1,"tab":"Shots"}`, `{"version":1,"tab":" shots"}`,
		`{"version":1,"tab":"unknown"}`, `{"version":1,"tab":1}`,
		`{"Version":1}`, `{"version":1,"Tab":"shots"}`,
		`{"version":1,"secret":"do-not-echo"}`,
		`{"version":1,"version":1}`, `{"version":1,"tab":"chat","tab":"shots"}`,
		`{"version":1,"\u0076ersion":1}`,
		`{"version":1} {"version":1}`, `{"version":1} null`, `{"version":1} garbage`,
		`{"version":1,}`, `{"version":1`, `{"version":1]`,
		"{\"version\":1,\"tab\":\"\xff\"}",
		strings.Repeat(" ", MaxConfigBytes) + `{"version":1}`,
	} {
		t.Run(input[:min(len(input), 60)], func(t *testing.T) {
			got, err := ParseConfig([]byte(input))
			if !errors.Is(err, ErrConfig) || got != (Config{}) {
				t.Fatalf("invalid input accepted: %+v, %v", got, err)
			}
		})
	}
	for _, tab := range []string{"chat", "plan", "shots", "timeline", "assets", "models", "logs"} {
		got, err := ParseConfig([]byte(`{"version":1,"tab":"` + tab + `"}`))
		if err != nil || got.Tab != tab {
			t.Fatalf("tab %s: %+v %v", tab, got, err)
		}
	}
	boundary := `{"version":1}`
	boundary += strings.Repeat(" ", MaxConfigBytes-len(boundary))
	if _, err := ParseConfig([]byte(boundary)); err != nil {
		t.Fatalf("exact limit: %v", err)
	}
}

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "explicit config.json")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadConfigExplicitBoundedAndSafeErrors(t *testing.T) {
	path := writeConfig(t, `{"version":1,"tab":"logs"}`)
	got, err := LoadConfig(path)
	if err != nil || got != (Config{Version: 1, Tab: "logs"}) {
		t.Fatalf("load = %+v %v", got, err)
	}
	for _, path := range []string{
		"", " ", "bad\x00path", t.TempDir(),
		filepath.Join(t.TempDir(), "private-secret-path"),
		writeConfig(t, strings.Repeat("x", MaxConfigBytes+1)),
	} {
		got, err := LoadConfig(path)
		if !errors.Is(err, ErrConfigRead) || got != (Config{}) {
			t.Fatalf("read failure = %+v %v", got, err)
		}
		if strings.Contains(err.Error(), "private-secret-path") {
			t.Fatal("path echoed")
		}
	}
	if _, err := LoadConfig(writeConfig(t, `{"secret":"do-not-echo"}`)); !errors.Is(err, ErrConfig) {
		t.Fatalf("decode failure = %v", err)
	}
}
