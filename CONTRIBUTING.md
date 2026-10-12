# Contributing and verification

## Permission and reports

Video Factory is proprietary source-available software under [LICENSE](LICENSE),
not an open-source contribution grant. You may compile and run the unmodified
source for the personal, non-commercial use described there. Modification and
derivative works, including code or documentation patches, require the licensor's
express written authorization. Commercial use, redistribution, and sublicensing
also require separate permission. This guide does not expand the license.

Bug reports and documentation feedback should describe the expected result,
actual result, command, Go/platform version, and a minimal nonsecret reproduction.
For rendering reports, include the relevant FFmpeg version and whether `libx264`
and `drawtext` are present. Review logs before sharing: omit credentials, private
media/prompts, machine-specific paths, and private service configuration.

## Authorized development workflow

Maintainers and expressly authorized contributors must read [PHASES.md](PHASES.md)
in full and [AGENTS.md](AGENTS.md) before editing. `PHASES.md` is the execution
authority: follow phase scope, the sequential two-model review gate, evidence
requirements, TODO semantics, and one logical commit per completed phase.

Keep changes consistent with the small Go interfaces, explicit dependencies,
context cancellation, deterministic-first behavior, and caller-owned lifecycle
contracts. Test changed behavior and preserve relevant cancellation, ownership,
resource, and integrity regressions. Keep optional ideas in
[FUTURE_IDEAS.md](FUTURE_IDEAS.md).

Use [ARCHITECTURE.md](ARCHITECTURE.md) and [DECISIONS.md](DECISIONS.md) for design
context. Record review results in `docs/reviews/` and phase evidence in
`docs/phases/`. The orchestrator owns integration, final verification, phase
status, and the phase commit. Publication and automatic push are not part of the
phase gate.

## Build and test

Requirements: Go 1.23+; the module has no third-party Go module dependencies.
Native render tests additionally need the tools described in
[docs/dependencies.md](docs/dependencies.md).

```sh
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
go build -trimpath -buildvcs=false ./...
```

Run race tests on a native platform/toolchain supporting Go's race detector, with
its required C compiler available. These are host tests, not cross-run tests.
For the native render milestones, use the verbose commands in the
[quickstart](docs/quickstart.md) and record whether each passed or skipped.

## Local binaries and cross-builds

These POSIX-shell commands write only local build outputs into ignored `bin/`.
They compile the CLI; they do not package FFmpeg or prove target runtime support.

```sh
mkdir -p bin
go build -trimpath -buildvcs=false -o bin/videofactory ./cmd/videofactory
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -buildvcs=false -o bin/videofactory-darwin-arm64 ./cmd/videofactory
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -buildvcs=false -o bin/videofactory-linux-amd64 ./cmd/videofactory
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -buildvcs=false -o bin/videofactory-windows-amd64.exe ./cmd/videofactory
```

`-trimpath` removes local source-path information; `-buildvcs=false` disables Go
VCS stamping. Version metadata defaults to `dev` / `unknown` / `unknown`. To
verify explicit injection with clearly marked test values:

```sh
go build -trimpath -buildvcs=false -ldflags '-X main.version=v0.0.0-test -X main.commit=testcommit -X main.buildDate=2026-10-12' -o bin/videofactory ./cmd/videofactory
./bin/videofactory version
```

These values are illustrative, not a release version or commit. Build comparisons
also depend on the Go toolchain, target, flags, and source contents; the flags alone
are not a cross-toolchain byte-reproducibility guarantee.

The bounded worker runner supports macOS and Linux process monitoring. Other
platforms return an unsupported error for that runner; a Windows cross-build is
only compilation evidence. See [release readiness](docs/release-readiness.md).

Generated media, binaries, profiles, weights, credentials, and machine-specific
configuration belong outside source control. Review the working tree and history
under the public-repository checks in `PHASES.md` before publication.
