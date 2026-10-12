# Dependencies and native requirements

## Go module

[`go.mod`](../go.mod) declares Go 1.23 and the single module
`github.com/manish96170/videofactory`. It has no `require` or `replace` directives
and no third-party Go module dependencies. Application packages use the Go
standard library plus packages in this module; rendering invokes external tools
with `os/exec`, not cgo/libav bindings.

You can check the module graph locally:

```sh
go list -m all
```

The current result is just the module above. This describes Go dependencies;
it does not imply that native rendering has no external dependencies.

## Native rendering

Install FFmpeg and FFprobe from a source appropriate to your platform. Verify the
installed build rather than assuming a package name implies particular features:

```sh
ffmpeg -version
ffprobe -version
ffmpeg -hide_banner -encoders
ffmpeg -hide_banner -filters
```

| Requirement | Used for |
| --- | --- |
| `ffmpeg` and `ffprobe` on `PATH` | Encoding/composition and media inspection |
| FFmpeg `libx264` encoder | The renderer explicitly selects H.264 via `-c:v libx264`, with `yuv420p` output |
| FFmpeg `drawtext` filter | Required only for Text primitives, including the fixture Director's text render milestone |
| Readable TrueType/OpenType font | Text rendering through `render.Settings.FontPath` |

Background/Image-only renders do not require `drawtext` or a font, but still need
the encoder and native tools. `drawtext` availability depends on the FFmpeg build
and its text-library support; a font alone does not enable it. Consult the
[FFmpeg drawtext documentation](https://ffmpeg.org/ffmpeg-filters.html#drawtext)
for the dependencies of the installed FFmpeg version.

`render.DefaultSettings()` supplies 1920×1080 at 30 FPS and checks a short list of
common system fonts. If none is found, its font path is empty; Text rendering then
fails explicitly. Programmatic callers can set `Settings.FontPath` to a font they
control and have rights to use. Font choice and native-tool versions affect output;
repeatable visual results require consistent inputs, settings, fonts, and tools.

Native tests use skip guards for missing executables; text tests also check
`drawtext` and a default font. Encoder absence is not universally a skip condition:
a present but incompatible FFmpeg can fail rendering. A passing test suite with
skips is not proof that all native render paths ran.

## Provider and platform dependencies

The shipped local/cloud adapters are injected boundaries tested with deterministic
fixtures. They do not bundle a model, Python environment, cloud SDK, default HTTP
transport, credential discovery, or provider installer. See
[providers](providers.md) for current interfaces and hardware limitations.

CLI builds use the standard library. Go race tests need a race-supported native
toolchain and its C compiler. The worker process runner supports macOS/Linux;
cross-compilation of the CLI does not validate native tools or model compatibility
on the destination system.

## Licensing boundaries

First-party code and documentation are governed by the proprietary
[LICENSE](../LICENSE). The repository's license does not grant rights to FFmpeg,
FFprobe, libx264, fonts, models, voices, or input assets. Those are separately
licensed materials. FFmpeg licensing depends on its build configuration and
enabled components; consult the installed distribution's notices and
[FFmpeg legal information](https://ffmpeg.org/legal.html).

The build instructions compile only the Go executable and do not bundle native
tools or fonts. Any separately authorized distribution must account for the actual
components distributed and their own terms; no third-party redistribution grant
is implied here. See [licensing](licensing.md) for the project decision.
