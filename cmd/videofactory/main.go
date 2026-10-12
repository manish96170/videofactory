// Command videofactory is the Video Factory entrypoint.
package main

import (
	"context"
	"os"

	"github.com/manish96170/videofactory/cli"
	"github.com/manish96170/videofactory/tui"
)

// Inject explicit metadata with cross-platform Go linker flags, for example:
// go build -trimpath -buildvcs=false -ldflags "-X main.version=v1.0.0 -X main.commit=abc123 -X main.buildDate=2026-10-12T00:00:00Z" ./cmd/videofactory
// No build timestamp or VCS metadata is discovered at runtime; -buildvcs=false
// also disables Go's build-time VCS stamping.
var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

func main() {
	os.Exit(cli.Run(context.Background(), os.Args[1:], cli.Dependencies{
		Stdout:   os.Stdout,
		Stderr:   os.Stderr,
		Build:    cli.BuildInfo{Version: version, Commit: commit, BuildDate: buildDate},
		Snapshot: tui.New().Snapshot(),
	}))
}
