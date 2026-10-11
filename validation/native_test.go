package validation

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manish96170/videofactory/domain"
	"github.com/manish96170/videofactory/render"
)

func nativeInspector(t *testing.T, root string) *ManagedNativeInspector {
	t.Helper()
	cfg := DefaultNativeConfig(root)
	cfg.RelativeRoot = root
	n, err := NewManagedNativeInspector(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return n
}
func writePNG(t *testing.T, root string, w, h int) string {
	t.Helper()
	path := filepath.Join(root, "image.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{A: 255})
		}
	}
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

type markerErrorContext struct {
	context.Context
	marker string
	err    error
}

func (c markerErrorContext) Err() error {
	if _, err := os.Stat(c.marker); err == nil {
		return c.err
	}
	return nil
}

func TestNativeLocalImageAndSourceGuards(t *testing.T) {
	root := t.TempDir()
	n := nativeInspector(t, root)
	writePNG(t, root, 64, 32)
	facts, err := n.Inspect(context.Background(), InspectionRequest{"image.png", domain.AssetKindImage})
	if err != nil || !facts.VisualDecoded || facts.Width != 64 || facts.Height != 32 {
		t.Fatal(facts, err)
	}
	if err := os.WriteFile(filepath.Join(root, "corrupt.png"), []byte("private corrupt media"), 0600); err != nil {
		t.Fatal(err)
	}
	outside := writePNG(t, t.TempDir(), 8, 8)
	if err := os.Symlink(outside, filepath.Join(root, "escape.png")); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		source string
		want   error
	}{
		{"missing.png", ErrMissingMedia}, {"corrupt.png", ErrInvalidMedia}, {"https://private.invalid/token", ErrNonLocalMedia},
		{"file:image.png", ErrNonLocalMedia}, {"hash:secret", ErrNonLocalMedia}, {"../outside.png", ErrNonLocalMedia},
		{"escape.png", ErrNonLocalMedia}, {outside, ErrNonLocalMedia}, {root, ErrNonLocalMedia}, {"//host/share", ErrNonLocalMedia}, {"image%03d.png", ErrNonLocalMedia},
	} {
		f, err := n.Inspect(context.Background(), InspectionRequest{tc.source, domain.AssetKindImage})
		if !errors.Is(err, tc.want) || f != (InspectionFacts{}) || strings.Contains(err.Error(), root) || strings.Contains(err.Error(), "private") {
			t.Errorf("guard %q: %v %+v", tc.source, err, f)
		}
		req := videoRequest()
		req.Asset.Kind = domain.AssetKindImage
		req.Asset.URI = tc.source
		req.Requirements = Requirements{Kind: domain.AssetKindImage, Quality: QualityFinal, Width: 64, Height: 32}
		result, validationErr := checker(t, n, DefaultConfig()).Validate(context.Background(), req)
		if validationErr != nil || result.Status != domain.ValidationFailed || result.RetryRecommended {
			t.Fatal(result, validationErr)
		}
	}
	large := filepath.Join(root, "large.png")
	file, err := os.Create(large)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxFileBytes + 1); err != nil {
		t.Fatal(err)
	}
	file.Close()
	if _, err := n.Inspect(context.Background(), InspectionRequest{large, domain.AssetKindImage}); !errors.Is(err, ErrInspectionLimit) {
		t.Fatal(err)
	}
	// PNG header-only huge dimensions must fail before attempting image decode.
	var body bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	if err := png.Encode(&body, img); err != nil {
		t.Fatal(err)
	}
	b := body.Bytes()
	binary.BigEndian.PutUint32(b[16:20], 8193)
	// Recompute IHDR CRC after changing its declared width.
	binary.BigEndian.PutUint32(b[29:33], crc32.ChecksumIEEE(b[12:29]))
	if err := os.WriteFile(filepath.Join(root, "huge.png"), b, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := n.Inspect(context.Background(), InspectionRequest{"huge.png", domain.AssetKindImage}); !errors.Is(err, ErrInspectionLimit) {
		t.Fatal(err)
	}
	if _, err := NewManagedNativeInspector(DefaultNativeConfig()); !errors.Is(err, ErrInvalidDependency) {
		t.Fatal(err)
	}
	if _, err := NewManagedNativeInspector(DefaultNativeConfig(string(filepath.Separator))); !errors.Is(err, ErrInvalidDependency) {
		t.Fatal(err)
	}
	cfg := DefaultNativeConfig(root)
	cfg.RelativeRoot = root
	copied, err := NewManagedNativeInspector(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfg.AllowedRoots[0] = t.TempDir()
	if _, err := copied.Inspect(context.Background(), InspectionRequest{"image.png", domain.AssetKindImage}); err != nil {
		t.Fatal("root alias retained", err)
	}
	cfg = DefaultNativeConfig(root)
	cfg.RelativeRoot = root
	cfg.OperationTimeout = time.Nanosecond
	bounded, err := NewManagedNativeInspector(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bounded.Inspect(context.Background(), InspectionRequest{"image.png", domain.AssetKindImage}); !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, ErrTransientInspection) {
		t.Fatal(err)
	}
}

func TestNativeProbeStrictNumbersAndAudioOrder(t *testing.T) {
	video := `{"codec_type":"video","codec_name":"h264","width":64,"height":32,"avg_frame_rate":"30/1"}`
	audio := `{"codec_type":"audio","codec_name":"aac","sample_rate":"48000","channels":2}`
	for _, streams := range []string{video + "," + audio, audio + "," + video, video + "," + audio + "," + audio, audio} {
		body := []byte(`{"format":{"duration":"1.0"},"streams":[` + streams + `]}`)
		f, err := parseProbe(body)
		want := strings.Count(streams, `"codec_type":"audio"`)
		if err != nil || f.AudioTracks != want || f.SampleRate != 48000 || f.Channels != 2 || f.Duration != time.Second {
			t.Fatal(f, err)
		}
		if strings.Contains(streams, `"video"`) && (f.Kind != domain.AssetKindVideo || f.CodecName != "h264" || f.Width != 64 || f.FPS != 30) {
			t.Fatal(f)
		}
	}
	valid := `{"format":{"duration":"1"},"streams":[` + video + `]}`
	for _, body := range []string{
		strings.Replace(valid, `"duration":"1"`, `"duration":"NaN"`, 1), strings.Replace(valid, `"duration":"1"`, `"duration":"Inf"`, 1),
		strings.Replace(valid, `"duration":"1"`, `"duration":"1e30"`, 1), strings.Replace(valid, `"duration":"1"`, `"duration":"-1"`, 1),
		strings.Replace(valid, `"30/1"`, `"NaN/1"`, 1), strings.Replace(valid, `"30/1"`, `"30/0"`, 1), strings.Replace(valid, `"30/1"`, `"30/Inf"`, 1),
		strings.Replace(valid, `"width":64`, `"width":0`, 1), strings.Replace(valid, `"width":64`, `"width":8193`, 1),
		strings.Replace(valid, `"duration":"1"`, `"duration":"1","duration":"2"`, 1), strings.Replace(valid, `"width":64`, `"width":64,"width":32`, 1),
		strings.Replace(valid, `"duration":"1"`, `"duration":"1","secret":true`, 1), valid + `{}`, `{"format":{"duration":"1"},"streams":[` + strings.Replace(audio, `"channels":2`, `"channels":0`, 1) + `]}`,
		strings.Replace(valid, `"streams"`, `"Streams"`, 1), strings.Replace(valid, `"width"`, `"Width"`, 1), strings.Replace(valid, `"duration":"1"`, `"duration":"1"`, 1) + " trailing",
		strings.Replace(valid, `"width":64`, `"duration":"NaN","width":64`, 1),
	} {
		if _, err := parseProbe([]byte(body)); err == nil {
			t.Errorf("accepted invalid probe: %s", body)
		}
	}
	for _, rate := range []string{"NaN", "Inf", "-1", "0"} {
		body := `{"format":{"duration":"1"},"streams":[` + strings.Replace(audio, `"48000"`, `"`+rate+`"`, 1) + `]}`
		if _, err := parseProbe([]byte(body)); err == nil {
			t.Fatal("invalid audio rate accepted")
		}
	}
}

func TestNativeProcessBoundsAndCancellation(t *testing.T) {
	if os.Getenv("VF_NATIVE_HELPER") == "1" {
		switch os.Args[len(os.Args)-1] {
		case "stdout":
			os.Stdout.Write(bytes.Repeat([]byte{'x'}, 128*1024))
		case "stdout-marker":
			if err := os.WriteFile(os.Getenv("VF_NATIVE_MARKER"), []byte("done"), 0600); err != nil {
				os.Exit(2)
			}
			os.Stdout.Write(bytes.Repeat([]byte{'x'}, 128*1024))
		case "stderr":
			os.Stderr.Write(bytes.Repeat([]byte{'x'}, 32*1024))
		case "sleep":
			time.Sleep(10 * time.Second)
		case "failure":
			os.Stderr.WriteString("private stderr")
			os.Exit(1)
		}
		os.Exit(0)
	}
	t.Setenv("VF_NATIVE_HELPER", "1")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, stream := range []string{"stdout", "stderr"} {
		_, err := runNative(context.Background(), t.TempDir(), executable, "-test.run=^TestNativeProcessBoundsAndCancellation$", stream)
		if !errors.Is(err, ErrInspectionLimit) {
			t.Fatal(stream, err)
		}
	}
	for _, tc := range []struct {
		name string
		err  error
	}{
		{name: "child deadline", err: context.DeadlineExceeded},
		{name: "parent cancellation", err: context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "command-finished")
			t.Setenv("VF_NATIVE_MARKER", marker)
			ctx := markerErrorContext{Context: context.Background(), marker: marker, err: tc.err}
			_, err := runNative(ctx, t.TempDir(), executable, "-test.run=^TestNativeProcessBoundsAndCancellation$", "stdout-marker")
			if tc.err == context.DeadlineExceeded {
				if !errors.Is(err, ErrInspectionLimit) || !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("permanent output limit marker lost alongside child deadline: %v", err)
				}
			} else if err != context.Canceled {
				t.Fatalf("parent cancellation did not remain authoritative: %v", err)
			}
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := runNative(ctx, t.TempDir(), executable, "-test.run=^TestNativeProcessBoundsAndCancellation$", "sleep"); err != context.DeadlineExceeded {
		t.Fatal(err)
	}
	if _, err := runNative(context.Background(), t.TempDir(), executable, "-test.run=^TestNativeProcessBoundsAndCancellation$", "failure"); !errors.Is(err, ErrInvalidMedia) || strings.Contains(err.Error(), "private") {
		t.Fatal(err)
	}
	if _, err := runNative(context.Background(), t.TempDir(), filepath.Join(t.TempDir(), "missing")); !errors.Is(err, ErrRuntimeMissing) || strings.Contains(err.Error(), "missing/") {
		t.Fatal(err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	cancel()
	if _, err := runNative(ctx, "", executable); err != context.Canceled {
		t.Fatal(err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	capture := &boundedCapture{limit: 8, cancel: cancel}
	capture.Write(make([]byte, 9))
	if capture.Len() != 8 || !capture.breached || ctx.Err() != context.Canceled {
		t.Fatal("capture did not bound/cancel")
	}
}

func TestNativeInspectionChildDeadlinePreservesPermanentMarker(t *testing.T) {
	deadline := context.DeadlineExceeded
	for _, permanent := range []error{ErrInspectionLimit, ErrMissingMedia, ErrInvalidMedia} {
		got := nativeInspectionError(nil, deadline, permanent)
		if !errors.Is(got, permanent) || !errors.Is(got, deadline) {
			t.Errorf("permanent marker %v lost alongside child deadline: %v", permanent, got)
		}
	}
	parent := context.Canceled
	got := nativeInspectionError(parent, deadline, ErrInspectionLimit)
	if got != parent || errors.Is(got, ErrInspectionLimit) {
		t.Fatalf("parent cancellation did not override child/permanent outcome: %v", got)
	}
	got = nativeInspectionError(nil, deadline, errors.New("incidental failure"))
	if !errors.Is(got, ErrTransientInspection) || !errors.Is(got, deadline) || errors.Is(got, ErrInspectionLimit) {
		t.Fatalf("nonpermanent child timeout classification changed: %v", got)
	}
}

func TestNativeRenderedVideoAndWAV(t *testing.T) {
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " missing")
		}
	}
	root := t.TempDir()
	n := nativeInspector(t, root)
	settings := render.DefaultSettings()
	settings.Width = 64
	settings.Height = 32
	settings.FPS = 30
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	shot := domain.Shot{ID: "solid", Duration: time.Second, Primitives: []domain.Primitive{{ID: "background", Type: domain.PrimitiveTypeBackground, Properties: map[string]any{"color": "000000"}}}}
	renderer := render.NewFFmpegRenderer(settings)
	output, err := renderer.RenderShot(ctx, render.ShotRenderRequest{Shot: shot, OutputDir: root})
	if err != nil {
		t.Fatal(err)
	}
	v := checker(t, n, DefaultConfig())
	req := videoRequest()
	req.Asset.URI = output.MediaPath
	result, err := v.Validate(ctx, req)
	if err != nil || result.Status != domain.ValidationPassed {
		t.Fatal(result, err)
	}
	facts, err := n.Inspect(ctx, InspectionRequest{output.MediaPath, domain.AssetKindVideo})
	if err != nil || !facts.VisualDecoded || facts.AudioTracks != 0 {
		t.Fatal(facts, err)
	}
	wav := writeWAV(t, root)
	facts, err = n.Inspect(ctx, InspectionRequest{wav, domain.AssetKindAudio})
	if err != nil || facts.AudioTracks != 1 || !facts.AudioDecoded || facts.SampleRate != 8000 || facts.Channels != 1 {
		t.Fatal(facts, err)
	}
	for _, audioFirst := range []bool{false, true} {
		path := filepath.Join(root, "with-audio.mp4")
		maps := []string{"0:v:0", "1:a:0"}
		if audioFirst {
			maps[0], maps[1] = maps[1], maps[0]
		}
		_, err := runNative(ctx, root, "ffmpeg", "-v", "error", "-y", "-i", output.MediaPath, "-i", wav, "-map", maps[0], "-map", maps[1], "-c:v", "copy", "-c:a", "aac", "-t", "1", path)
		if err != nil {
			t.Fatal(err)
		}
		facts, err = n.Inspect(ctx, InspectionRequest{path, domain.AssetKindVideo})
		if err != nil || facts.AudioTracks != 1 || !facts.AudioDecoded || facts.CodecName != "h264" {
			t.Fatal(facts, err)
		}
		probe, err := render.Probe(ctx, path)
		if err != nil || probe.AudioTracks != 1 || probe.CodecName != "h264" {
			t.Fatal(probe, err)
		}
		req.Asset.URI = path
		req.Requirements.AudioRequired = true
		result, err = v.Validate(ctx, req)
		if err != nil || result.Status != domain.ValidationPassed {
			t.Fatal(result, err)
		}
	}
	filters, err := runNative(ctx, root, "ffmpeg", "-hide_banner", "-filters")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(filters, []byte("drawtext")) && settings.FontPath != "" {
		shot.ID = "text"
		shot.Primitives = append(shot.Primitives, domain.Primitive{ID: "text", Type: domain.PrimitiveTypeText, Properties: map[string]any{"text": "Static", "fontSize": 12}})
		output, err = renderer.RenderShot(ctx, render.ShotRenderRequest{Shot: shot, OutputDir: root})
		if err != nil {
			t.Fatal(err)
		}
		req.Asset.URI = output.MediaPath
		req.Requirements.AudioRequired = false
		result, err = v.Validate(ctx, req)
		if err != nil || result.Status != domain.ValidationPassed {
			t.Fatal(result, err)
		}
	} else {
		t.Log("text fixture unavailable: missing drawtext/font; solid video fixture executed")
	}
	for _, kind := range []domain.PrimitiveType{domain.PrimitiveTypeBackground, domain.PrimitiveTypeText, domain.PrimitiveTypeImage, domain.PrimitiveTypeTransition} {
		if !render.SupportsPrimitive(kind) {
			t.Fatal(kind)
		}
	}
	if render.SupportsPrimitive(domain.PrimitiveTypeCodeEditor) || render.SupportsPrimitive("unknown") {
		t.Fatal("unsupported primitive accepted")
	}
}

func writeWAV(t *testing.T, root string) string {
	t.Helper()
	var b bytes.Buffer
	b.WriteString("RIFF")
	binary.Write(&b, binary.LittleEndian, uint32(36+16000))
	b.WriteString("WAVEfmt ")
	binary.Write(&b, binary.LittleEndian, uint32(16))
	binary.Write(&b, binary.LittleEndian, uint16(1))
	binary.Write(&b, binary.LittleEndian, uint16(1))
	binary.Write(&b, binary.LittleEndian, uint32(8000))
	binary.Write(&b, binary.LittleEndian, uint32(16000))
	binary.Write(&b, binary.LittleEndian, uint16(2))
	binary.Write(&b, binary.LittleEndian, uint16(16))
	b.WriteString("data")
	binary.Write(&b, binary.LittleEndian, uint32(16000))
	b.Write(make([]byte, 16000))
	path := filepath.Join(root, "audio.wav")
	if err := os.WriteFile(path, b.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
