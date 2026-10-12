# Phase 17 — Performance / Reliability / Packaging

## Measurements and decisions

Deterministic fixture benchmarks on Darwin/arm64 measured logger emit 104.6 ns/op,
parallel counter add 106.9 ns/op, histogram observe 14.11 ns/op, and a 192-series
snapshot 35.8 µs/op. The 96-shot pipeline fixture measured cold 4.09–5.57s and warm
cache 0.47–0.56s with 96 hits, zero new renders, and 96 shots/op. These are observed
fixture measurements, not hardware guarantees or model/provider benchmarks.

No resource scheduler/startup tuning was justified by these results. Provider/model load
timing is explicitly unavailable because Phase 17 did not load models. Packaging uses
`go build -trimpath -buildvcs=false -ldflags '-X main.version=... -X main.commit=... -X main.buildDate=...'`;
macOS/arm64, Linux/amd64, and Windows/amd64 builds compile. No archives/binaries publish.

Reproducible profile command:
`profile_dir="$(mktemp -d)"; go test -run '^$' -bench '^BenchmarkPipelineLargeDeterministicWarmCache$' -benchtime=1x -cpuprofile="$profile_dir/cpu.pprof" -memprofile="$profile_dir/mem.pprof" ./pipeline`.
On the recorded Darwin/arm64 run, CPU samples were primarily `syscall.rawsyscalln`
(52.03%) and runtime wait/OS functions; allocation profile top entries were artifact
copying (97.87 MB), `bytes.Clone` (63.93 MB), and slice growth (50.38 MB). These are
one-run observations, not optimization targets without repeatable workload evidence.

Concurrency profile command:
`profile_dir="$(mktemp -d)"; go test -count=1 -run '^TestRepeatedConcurrentAcquireReleaseHasNoAccountingDrift$' -mutexprofile="$profile_dir/mutex.pprof" -mutexprofilefraction=1 -blockprofile="$profile_dir/block.pprof" -blockprofilerate=1 ./resource`.
The recorded mutex profile had 1.39 ms total delay (97.48% in `sync.Mutex.Unlock`);
the block profile had 2.45 ms (55.28% `sync.Mutex.Lock`, 27.83% channel receive).
The bounded concurrent acquire/release test passed with no accounting drift. This does
not justify changing the intentional FIFO/head-of-line resource policy without a more
representative bottleneck.

Cross-build commands verified:
`build_dir="$(mktemp -d)"; GOOS=darwin GOARCH=arm64 go build -trimpath -buildvcs=false -o "$build_dir/videofactory-darwin-arm64" ./cmd/videofactory`,
`build_dir="$(mktemp -d)"; GOOS=linux GOARCH=amd64 go build -trimpath -buildvcs=false -o "$build_dir/videofactory-linux-amd64" ./cmd/videofactory`, and
`build_dir="$(mktemp -d)"; GOOS=windows GOARCH=amd64 go build -trimpath -buildvcs=false -o "$build_dir/videofactory-windows-amd64.exe" ./cmd/videofactory`.
An injected build verified
`build_dir="$(mktemp -d)"; go build -trimpath -buildvcs=false -ldflags '-X main.version=v0.0.0-test -X main.commit=testcommit -X main.buildDate=2026-10-12' -o "$build_dir/videofactory" ./cmd/videofactory`
and `"$build_dir/videofactory" version` printed all three injected values.

## Implemented

Bounded dependency-free structured logs, metrics, and trace records; strict explicit CLI
config/help/version/TUI snapshot commands; large deterministic cache/cancellation/recovery
stress tests and cross-build verification.
