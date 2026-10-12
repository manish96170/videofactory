// Package cli provides a one-shot standard-library command shell. Dependencies
// are explicit; commands validate configuration or present a caller-owned TUI
// snapshot. It starts no workers, background tasks, or interactive terminal loop.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"runtime"
	"strings"

	"github.com/manish96170/videofactory/tui"
)

const (
	ExitOK      = 0
	ExitFailure = 1
	ExitUsage   = 2
)

// BuildInfo contains explicitly injected build metadata. Empty fields have
// deterministic development defaults; no clock, VCS, or environment is queried.
type BuildInfo struct {
	Version   string
	Commit    string
	BuildDate string
}

// Dependencies are borrowed for Run's duration. Callers must not mutate the
// snapshot concurrently; concurrent Runs need concurrency-safe output writers.
// Nil writers discard output. The zero snapshot is a valid empty projection.
type Dependencies struct {
	Stdout   io.Writer
	Stderr   io.Writer
	Build    BuildInfo
	Snapshot tui.Snapshot
}

const help = `Usage:
  videofactory help
  videofactory version
  videofactory config validate --config PATH
  videofactory tui [--config PATH]

version prints build metadata. --version is an alias.
help prints this usage. -h and --help are aliases.
tui prints one snapshot and exits.
--config reads only the explicitly named UTF-8 JSON file (maximum 64 KiB).
Configuration: {"version":1,"tab":"shots"}
version is required; tab is optional (chat, plan, shots, timeline, assets, models, logs).
`

// Run executes one command and returns a process exit code: 0 success, 1
// configuration/I/O/context failure, 2 invalid command usage. Error output uses
// fixed diagnostics rather than reflecting arguments, paths, or dependency errors.
func Run(ctx context.Context, args []string, deps Dependencies) int {
	out, diagnostic := deps.Stdout, deps.Stderr
	if out == nil {
		out = io.Discard
	}
	if diagnostic == nil {
		diagnostic = io.Discard
	}
	fail := func(code int, message string) int {
		_, _ = fmt.Fprintln(diagnostic, message)
		return code
	}
	if ctx == nil || ctx.Err() != nil {
		return fail(ExitFailure, "cli: command canceled or missing context")
	}
	write := func(text string) int {
		if ctx.Err() != nil {
			return fail(ExitFailure, "cli: command canceled")
		}
		n, err := io.WriteString(out, text)
		if err != nil || n != len(text) {
			return fail(ExitFailure, "cli: output failed")
		}
		if ctx.Err() != nil {
			return fail(ExitFailure, "cli: command canceled")
		}
		return ExitOK
	}
	if len(args) == 0 {
		return write(help)
	}
	switch args[0] {
	case "help", "-h", "--help":
		if len(args) != 1 {
			return fail(ExitUsage, "cli: help accepts no arguments")
		}
		return write(help)
	case "version", "--version":
		if len(args) != 1 {
			return fail(ExitUsage, "cli: version accepts no arguments")
		}
		return write(fmt.Sprintf("videofactory %s\ncommit: %s\nbuilt: %s\ngo: %s\nplatform: %s/%s\n",
			buildToken(deps.Build.Version, "dev"), buildToken(deps.Build.Commit, "unknown"),
			buildToken(deps.Build.BuildDate, "unknown"), runtime.Version(), runtime.GOOS, runtime.GOARCH))
	case "config", "tui":
		command := args[0]
		rest := args[1:]
		if command == "config" {
			if len(rest) == 0 || rest[0] != "validate" {
				return fail(ExitUsage, "cli: use config validate --config PATH")
			}
			rest = rest[1:]
		}
		path, err := parseFlags(rest)
		if errors.Is(err, flag.ErrHelp) {
			return write(help)
		}
		if err != nil || (command == "config" && path == "") {
			return fail(ExitUsage, "cli: invalid arguments; use help for usage")
		}
		var config Config
		if path != "" {
			config, err = LoadConfig(path)
			if err != nil {
				return fail(ExitFailure, err.Error())
			}
		}
		if command == "config" {
			return write("configuration valid\n")
		}
		snapshot := deps.Snapshot
		if config.Tab != "" {
			snapshot.Focused, _ = configTab(config.Tab)
		}
		return write(tui.Render(snapshot))
	default:
		return fail(ExitUsage, "cli: unknown command; use help for usage")
	}
}

func parseFlags(args []string) (string, error) {
	fs := flag.NewFlagSet("videofactory", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var path string
	fs.Func("config", "explicit configuration path", func(value string) error {
		if path != "" || strings.TrimSpace(value) == "" {
			return errors.New("invalid configuration path argument")
		}
		path = value
		return nil
	})
	if err := fs.Parse(args); err != nil {
		return "", err
	}
	if fs.NArg() != 0 {
		return "", errors.New("unexpected positional argument")
	}
	return path, nil
}

// Metadata is operator-supplied but still must not inject terminal controls or
// unbounded output. Invalid tokens are replaced, not echoed in diagnostics.
func buildToken(value, fallback string) string {
	if value == "" {
		return fallback
	}
	if len(value) > 128 {
		return "invalid"
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune("._-+:/", r) {
			continue
		}
		return "invalid"
	}
	return value
}
