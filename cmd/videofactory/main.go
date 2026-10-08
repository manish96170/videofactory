// Command videofactory is the Video Factory entrypoint.
//
// Phase 00 only needs this to prove the module builds into a runnable
// binary. The real CLI/TUI control plane is implemented in Phase 12.
package main

import "fmt"

// version is overridden via -ldflags in release builds; Phase 00 does not
// wire up real build-time versioning.
var version = "dev"

func main() {
	fmt.Printf("videofactory %s (foundation phase)\n", version)
}
