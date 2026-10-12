package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/manish96170/videofactory/tui"
)

func runTest(args []string, deps Dependencies) (int, string, string) {
	var stdout, stderr bytes.Buffer
	deps.Stdout, deps.Stderr = &stdout, &stderr
	code := Run(context.Background(), args, deps)
	return code, stdout.String(), stderr.String()
}

func TestHelpVersionAndBuildMetadata(t *testing.T) {
	for _, args := range [][]string{nil, {"help"}, {"-h"}, {"--help"}, {"tui", "--help"}, {"config", "validate", "-h"}} {
		code, out, diagnostic := runTest(args, Dependencies{})
		if code != ExitOK || diagnostic != "" || !strings.Contains(out, "config validate --config PATH") {
			t.Fatalf("%v: %d %q %q", args, code, out, diagnostic)
		}
	}
	for _, arg := range []string{"version", "--version"} {
		code, out, diagnostic := runTest([]string{arg}, Dependencies{Build: BuildInfo{
			Version: "v1.2.3+test", Commit: "abc123", BuildDate: "2026-10-12T00:00:00Z",
		}})
		if code != ExitOK || diagnostic != "" {
			t.Fatalf("version: %d %q", code, diagnostic)
		}
		for _, want := range []string{"videofactory v1.2.3+test\n", "commit: abc123\n", "built: 2026-10-12T00:00:00Z\n", "go: " + runtime.Version(), "platform: " + runtime.GOOS + "/" + runtime.GOARCH} {
			if !strings.Contains(out, want) {
				t.Fatalf("missing %q: %q", want, out)
			}
		}
	}
	_, out, _ := runTest([]string{"version"}, Dependencies{})
	if !strings.HasPrefix(out, "videofactory dev\ncommit: unknown\nbuilt: unknown\n") {
		t.Fatalf("defaults: %q", out)
	}
	_, out, _ = runTest([]string{"version"}, Dependencies{Build: BuildInfo{
		Version: "injected\x1b[31m", Commit: strings.Repeat("x", 129), BuildDate: "line\nbreak",
	}})
	if !strings.HasPrefix(out, "videofactory invalid\ncommit: invalid\nbuilt: invalid\n") {
		t.Fatalf("unsafe metadata: %q", out)
	}
}

func TestInvalidUsageHasNoSideEffectsOrArgumentEcho(t *testing.T) {
	for _, args := range [][]string{
		{"private-secret"}, {"--private-secret"}, {"generate"},
		{"help", "private-secret"}, {"version", "private-secret"},
		{"config"}, {"config", "private-secret"}, {"config", "validate"},
		{"config", "validate", "--config"}, {"config", "validate", "--config="},
		{"tui", "private-secret"}, {"tui", "--private-secret"},
		{"tui", "--config", "private-secret", "extra"},
		{"tui", "--config=private-secret", "--config=another"},
		{"tui", "--config= "}, {"--config=private-secret", "tui"},
	} {
		code, out, diagnostic := runTest(args, Dependencies{})
		if code != ExitUsage || out != "" || diagnostic == "" || strings.Contains(diagnostic, "private-secret") {
			t.Fatalf("%v: %d %q %q", args, code, out, diagnostic)
		}
	}
}

func TestConfigAndSnapshotCommands(t *testing.T) {
	snapshot := tui.New().Snapshot()
	snapshot.Status = tui.Status{Code: "ready", Text: "injected state\x1b"}
	snapshot.Views[tui.TabShots].Items = []tui.Item{{ID: "shot-1", Label: "One"}}
	before := tui.Render(snapshot)
	path := writeConfig(t, `{"version":1,"tab":"shots"}`)
	for _, args := range [][]string{{"config", "validate", "--config", path}, {"config", "validate", "--config=" + path}} {
		code, out, diagnostic := runTest(args, Dependencies{Snapshot: snapshot})
		if code != ExitOK || out != "configuration valid\n" || diagnostic != "" {
			t.Fatalf("validate: %d %q %q", code, out, diagnostic)
		}
	}
	code, out, diagnostic := runTest([]string{"tui", "--config", path}, Dependencies{Snapshot: snapshot})
	if code != ExitOK || diagnostic != "" || !strings.Contains(out, "Tabs: Chat Plan [Shots]") || !strings.Contains(out, `text="injected state\u001b"`) {
		t.Fatalf("tui: %d %q %q", code, out, diagnostic)
	}
	if tui.Render(snapshot) != before || snapshot.Focused != tui.TabChat {
		t.Fatal("CLI mutated caller snapshot")
	}
	code, out, diagnostic = runTest([]string{"tui", "--config", writeConfig(t, `{"version":1}`)}, Dependencies{Snapshot: snapshot})
	if code != ExitOK || diagnostic != "" || out != before {
		t.Fatalf("omitted tab changed focus: %d %q %q", code, out, diagnostic)
	}
	for _, path := range []string{writeConfig(t, `{"secret":"private-secret"}`), "missing-private-secret"} {
		for _, args := range [][]string{{"tui", "--config", path}, {"config", "validate", "--config", path}} {
			code, out, diagnostic := runTest(args, Dependencies{Snapshot: snapshot})
			if code != ExitFailure || out != "" || diagnostic == "" || strings.Contains(diagnostic, "private-secret") {
				t.Fatalf("config failure: %d %q %q", code, out, diagnostic)
			}
		}
	}
}

func TestNoEnvironmentDiscoveryOrExpansion(t *testing.T) {
	path := writeConfig(t, `{"version":1,"tab":"logs"}`)
	for _, key := range []string{"VIDEOFACTORY_CONFIG", "HOME", "XDG_CONFIG_HOME"} {
		t.Setenv(key, path)
	}
	snapshot := tui.New().Snapshot()
	code, out, diagnostic := runTest([]string{"tui"}, Dependencies{Snapshot: snapshot})
	if code != ExitOK || diagnostic != "" || out != tui.Render(snapshot) {
		t.Fatalf("ambient config used: %d %q %q", code, out, diagnostic)
	}
	code, out, _ = runTest([]string{"config", "validate", "--config", "$VIDEOFACTORY_CONFIG"}, Dependencies{})
	if code != ExitFailure || out != "" {
		t.Fatal("expanded environment variable")
	}
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

func TestCancellationAndOutputFailures(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, ctx := range []context.Context{nil, ctx} {
		var out bytes.Buffer
		if code := Run(ctx, []string{"tui"}, Dependencies{Stdout: &out}); code != ExitFailure || out.Len() != 0 {
			t.Fatalf("cancellation: code=%d out=%q", code, out.String())
		}
	}
	for _, writer := range []io.Writer{
		writerFunc(func([]byte) (int, error) { return 0, errors.New("private-secret") }),
		writerFunc(func(p []byte) (int, error) { return len(p) - 1, nil }),
	} {
		var diagnostic bytes.Buffer
		if code := Run(context.Background(), []string{"help"}, Dependencies{Stdout: writer, Stderr: &diagnostic}); code != ExitFailure || diagnostic.String() != "cli: output failed\n" {
			t.Fatalf("writer failure: %d %q", code, diagnostic.String())
		}
	}
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	if code := Run(ctx, []string{"help"}, Dependencies{Stdout: writerFunc(func(p []byte) (int, error) {
		cancel()
		return len(p), nil
	})}); code != ExitFailure {
		t.Fatal("late cancellation accepted")
	}
}

func TestConcurrentRunsAreIndependent(t *testing.T) {
	snapshot := tui.New().Snapshot()
	want := tui.Render(snapshot)
	args := []string{"tui"}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			code, out, diagnostic := runTest(args, Dependencies{Snapshot: snapshot})
			if code != ExitOK || out != want || diagnostic != "" {
				t.Errorf("concurrent run: %d %q %q", code, out, diagnostic)
			}
		}()
	}
	wg.Wait()
	if !reflect.DeepEqual(args, []string{"tui"}) {
		t.Fatal("mutated arguments")
	}
}
