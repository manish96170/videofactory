# Quickstart

Run these commands from the repository root in a POSIX shell with Go 1.23+.
Use is subject to the proprietary [LICENSE](../LICENSE): personal,
non-commercial use of unmodified source only, unless separately authorized.

## 1. Build the CLI

```sh
mkdir -p bin
go build -trimpath -buildvcs=false -o bin/videofactory ./cmd/videofactory
./bin/videofactory help
./bin/videofactory version
```

Without explicit linker metadata, version output includes `videofactory dev`,
`commit: unknown`, and `built: unknown`, followed by the Go version and target
platform. `--version` is an alias; `help`, `-h`, and `--help` print usage.
No arguments also prints usage.

## 2. Validate presentation configuration

Create a local configuration file:

```sh
mkdir -p tmp
cat > tmp/quickstart.json <<'JSON'
{"version":1,"tab":"shots"}
JSON
./bin/videofactory config validate --config tmp/quickstart.json
./bin/videofactory tui --config tmp/quickstart.json
```

Validation prints `configuration valid`. `tui` renders one snapshot and exits.
The entrypoint supplies an empty control-plane snapshot; this command does not
load a project, start a terminal interaction loop, or run a renderer.

Configuration is an explicitly named regular UTF-8 JSON file of at most 64 KiB:

- `version` is required and must be integer `1`.
- `tab` may be omitted; if present it must be one of `chat`, `plan`, `shots`,
  `timeline`, `assets`, `models`, or `logs`.
- Keys are case-sensitive. Unknown/duplicate keys, nulls, an empty tab, and trailing
  JSON values are rejected.
- This is presentation configuration only; credentials, provider settings, and
  execution settings are not accepted.

The loader does not discover files or expand environment variables or home-directory
syntax. Keep the file and its parent directories under your control while reading.
Diagnostics use fixed messages rather than echoing config contents or file paths.
Exit codes are `0` for success, `1` for configuration/I/O/context failures, and `2`
for invalid command usage.

## 3. Run the deterministic sample workflow

The runnable samples are existing Go integration tests. They create artifacts in
test-owned temporary directories and clean them up; there is no persistent-output
sample CLI command. The following tests exercise complementary parts of the system.

### Real MP4 through the pipeline

Install the [native requirements](dependencies.md), including `libx264`, then run:

```sh
go test -count=1 -v ./pipeline -run '^TestRenderTimelineComposerProducesRealFFmpegOutput$'
```

The sample project in [`pipeline/pipeline_test.go`](../pipeline/pipeline_test.go)
has two Background shots of 2 and 3 seconds. The test injects a fixed plan,
analysis, and validator fixtures, but uses the real FFmpeg renderer, preview
artifact storage, and timeline composer. It independently probes the composed
640×360 MP4 and checks its duration is approximately 5 seconds. It needs no font
or `drawtext`. This is real-media composition evidence, not an all-production
Director/intelligence/validation integration.

### Prompt to text video

With `drawtext` and a discoverable default font available:

```sh
go test -count=1 -v ./director -run '^TestSecondMilestonePromptToRenderedMP4$'
```

[`director/second_milestone_test.go`](../director/second_milestone_test.go) uses:

> First we open the code editor. Then we write a small function. Finally we run it in the terminal.

The fixture Director creates three Background+Text shots. The test renders them
at 320×240, composes a silent MP4, and verifies dimensions and duration with
FFprobe. The text describes those actions; the sample does not draw a code editor
or terminal application. This workflow uses fixed planning heuristics and no AI.

For both commands, check for `PASS` versus `SKIP` in verbose output. Missing native
tools or text prerequisites can skip these tests. Generated IDs/timestamps are
fresh, and encoded bytes are not promised identical across native-tool versions.

### Cache and recovery without native rendering

```sh
go test -count=1 -v ./pipeline -run '^(TestRunDeterministicPipelineAndIncrementalCache|TestPartialFailureResumesWithoutRerenderingVerifiedShot|TestPixelInputChangeInvalidatesOnlyAffectedShot)$'
```

These use synthetic media and injected dependencies to verify cache hits,
completed-shot reuse after failure, and affected-shot rebuild behavior. They do
not prove video encoding. Resume reconstructs work from verified artifact and
checkpoint evidence; it does not restore a running process.

## 4. Verify the full source tree

```sh
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
go build -trimpath -buildvcs=false ./...
```

See [CONTRIBUTING.md](../CONTRIBUTING.md) for cross-builds and verification
requirements, and [providers](providers.md) for offline provider samples and the
current no-live-execution boundary.
