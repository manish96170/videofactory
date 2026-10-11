package validation

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/manish96170/videofactory/domain"
)

const maxFileBytes int64 = 1_000_000_000

// NativeConfig is trusted runtime configuration, never generator parameters.
// Roots must be existing absolute directories, not the filesystem root. Relative
// sources require an explicit RelativeRoot within AllowedRoots. Directories and
// files must not be concurrently replaced by an adversarial local writer.
type NativeConfig struct {
	AllowedRoots            []string
	RelativeRoot            string
	FFmpegPath, FFprobePath string
	WorkingDir              string
	OperationTimeout        time.Duration
}

func DefaultNativeConfig(roots ...string) NativeConfig {
	return NativeConfig{AllowedRoots: append([]string(nil), roots...), FFmpegPath: "ffmpeg", FFprobePath: "ffprobe", OperationTimeout: 5 * time.Second}
}

type ManagedNativeInspector struct{ config NativeConfig }

func NewManagedNativeInspector(cfg NativeConfig) (*ManagedNativeInspector, error) {
	if len(cfg.AllowedRoots) == 0 || len(cfg.AllowedRoots) > 16 || cfg.OperationTimeout <= 0 || cfg.FFmpegPath == "" || cfg.FFprobePath == "" {
		return nil, ErrInvalidDependency
	}
	cfg.AllowedRoots = append([]string(nil), cfg.AllowedRoots...)
	for i, root := range cfg.AllowedRoots {
		resolved, err := trustedDirectory(root)
		if err != nil {
			return nil, err
		}
		cfg.AllowedRoots[i] = resolved
	}
	for _, field := range []*string{&cfg.RelativeRoot, &cfg.WorkingDir} {
		if *field == "" {
			continue
		}
		resolved, err := trustedDirectory(*field)
		if err != nil {
			return nil, err
		}
		if !authorized(cfg.AllowedRoots, resolved) {
			return nil, ErrInvalidDependency
		}
		*field = resolved
	}
	if cfg.WorkingDir == "" {
		cfg.WorkingDir = cfg.AllowedRoots[0]
	}
	return &ManagedNativeInspector{cfg}, nil
}

func trustedDirectory(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", ErrInvalidDependency
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", safeError("invalid_dependency", errors.Join(ErrInvalidDependency, err))
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", safeError("invalid_dependency", errors.Join(ErrInvalidDependency, err))
	}
	if !info.IsDir() || filepath.Dir(resolved) == resolved {
		return "", ErrInvalidDependency
	}
	return resolved, nil
}

func authorized(roots []string, path string) bool {
	for _, root := range roots {
		rel, err := filepath.Rel(root, path)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
			return true
		}
	}
	return false
}

func (n *ManagedNativeInspector) localFile(source string) (*os.File, string, error) {
	// No URL schemes, network paths, hash locators, patterns, or exotic prefixes.
	if source == "" || strings.ContainsAny(source, ":\\\x00\r\n*?[]#%") || strings.HasPrefix(source, "//") {
		return nil, "", ErrNonLocalMedia
	}
	path := source
	if !filepath.IsAbs(path) {
		if n.config.RelativeRoot == "" {
			return nil, "", ErrNonLocalMedia
		}
		path = filepath.Join(n.config.RelativeRoot, path)
	}
	if !filepath.IsAbs(source) && !authorized(n.config.AllowedRoots, filepath.Clean(path)) {
		return nil, "", ErrNonLocalMedia
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		code := ErrInvalidMedia
		if errors.Is(err, os.ErrNotExist) {
			code = ErrMissingMedia
		}
		return nil, "", safeError("source_unavailable", errors.Join(code, err))
	}
	if !authorized(n.config.AllowedRoots, resolved) {
		return nil, "", ErrNonLocalMedia
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return nil, "", safeError("source_unavailable", errors.Join(ErrInvalidMedia, err))
	}
	if !info.Mode().IsRegular() {
		return nil, "", ErrNonLocalMedia
	}
	if info.Size() <= 0 {
		return nil, "", ErrInvalidMedia
	}
	if info.Size() > maxFileBytes {
		return nil, "", ErrInspectionLimit
	}
	f, err := os.Open(resolved)
	if err != nil {
		return nil, "", safeError("source_unavailable", errors.Join(ErrInvalidMedia, err))
	}
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		f.Close()
		return nil, "", ErrInvalidMedia
	}
	return f, resolved, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func (n *ManagedNativeInspector) Inspect(ctx context.Context, req InspectionRequest) (facts InspectionFacts, err error) {
	if ctx == nil {
		return InspectionFacts{}, ErrInvalidRequest
	}
	if err = ctx.Err(); err != nil {
		return InspectionFacts{}, err
	}
	if n == nil || len(n.config.AllowedRoots) == 0 || n.config.OperationTimeout <= 0 {
		return InspectionFacts{}, ErrInvalidDependency
	}
	child, cancel := context.WithTimeout(ctx, n.config.OperationTimeout)
	defer cancel()
	defer func() {
		err = nativeInspectionError(ctx.Err(), child.Err(), err)
		if err != nil {
			facts = InspectionFacts{}
		}
	}()
	f, path, err := n.localFile(req.URI)
	if err != nil {
		return InspectionFacts{}, err
	}
	defer f.Close()
	if req.Kind == domain.AssetKindImage {
		return inspectImage(child, f)
	}
	if req.Kind != domain.AssetKindVideo && req.Kind != domain.AssetKindAudio {
		return InspectionFacts{}, ErrInvalidMedia
	}
	header := make([]byte, 16)
	nread, _ := f.Read(header)
	header = header[:nread]
	demuxer := ""
	switch {
	case len(header) >= 8 && string(header[4:8]) == "ftyp":
		demuxer = "mov"
	case bytes.HasPrefix(header, []byte{0x1a, 0x45, 0xdf, 0xa3}):
		demuxer = "matroska"
	case len(header) >= 12 && string(header[:4]) == "RIFF" && string(header[8:12]) == "WAVE":
		demuxer = "wav"
	case bytes.HasPrefix(header, []byte("fLaC")):
		demuxer = "flac"
	case bytes.HasPrefix(header, []byte("ID3")) || len(header) >= 2 && header[0] == 0xff && header[1]&0xe0 == 0xe0:
		demuxer = "mp3"
	default:
		return InspectionFacts{}, ErrInvalidMedia
	}
	input := []string{"-max_alloc", "67108864", "-max_pixels", "16777216", "-protocol_whitelist", "file", "-format_whitelist", demuxer, "-f", demuxer, "-probesize", "1048576", "-analyzeduration", "1000000", "-threads", "1"}
	if demuxer == "mov" {
		input = append(input, "-enable_drefs", "0", "-use_absolute_path", "0")
	}
	probeArgs := append(append([]string{"-v", "error", "-nofind_stream_info"}, input...), "-show_entries", "format=duration:stream=duration,codec_type,codec_name,width,height,avg_frame_rate,sample_rate,channels", "-of", "json", path)
	body, err := runNative(child, n.config.WorkingDir, n.config.FFprobePath, probeArgs...)
	if err != nil {
		return InspectionFacts{}, err
	}
	facts, err = parseProbe(body)
	if err != nil {
		return InspectionFacts{}, err
	}
	if facts.Kind != req.Kind {
		return InspectionFacts{}, ErrInvalidMedia
	}
	decodeArgs := append(append([]string{"-v", "error", "-nostdin", "-xerror"}, input...), "-i", path)
	if facts.Kind == domain.AssetKindVideo {
		// One 8x8 midpoint thumbnail; solid/static/black frames are valid.
		args := append(append([]string(nil), decodeArgs...), "-ss", strconv.FormatFloat(facts.Duration.Seconds()/2, 'f', 9, 64), "-map", "0:v:0", "-frames:v", "1", "-vf", "scale=8:8", "-threads", "1", "-f", "rawvideo", "-pix_fmt", "rgb24", "pipe:1")
		out, e := runNative(child, n.config.WorkingDir, n.config.FFmpegPath, args...)
		if e != nil {
			return InspectionFacts{}, e
		}
		if len(out) != 8*8*3 {
			return InspectionFacts{}, ErrInvalidMedia
		}
		facts.VisualDecoded = true
		uniform := true
		for i := 3; i < len(out); i += 3 {
			if !bytes.Equal(out[:3], out[i:i+3]) {
				uniform = false
				break
			}
		}
		facts.UniformFrame = &uniform
	}
	if facts.AudioTracks > 0 {
		args := append(append([]string(nil), decodeArgs...), "-map", "0:a:0", "-t", "0.02", "-ac", "1", "-ar", "8000", "-threads", "1", "-f", "s16le", "pipe:1")
		out, e := runNative(child, n.config.WorkingDir, n.config.FFmpegPath, args...)
		if e != nil {
			return InspectionFacts{}, e
		}
		if len(out) < 2 || len(out) > 320 || len(out)%2 != 0 {
			return InspectionFacts{}, ErrInvalidMedia
		}
		facts.AudioDecoded = true
	}
	return facts, nil
}

func nativeInspectionError(parentErr, childErr, operationErr error) error {
	if parentErr != nil {
		return parentErr
	}
	if childErr == nil {
		return operationErr
	}
	if errors.Is(operationErr, ErrInspectionLimit) || errors.Is(operationErr, ErrNonLocalMedia) ||
		errors.Is(operationErr, ErrMissingMedia) || errors.Is(operationErr, ErrRuntimeMissing) ||
		errors.Is(operationErr, ErrInvalidMedia) {
		return errors.Join(operationErr, childErr)
	}
	return safeError("inspection_timeout", errors.Join(ErrTransientInspection, childErr))
}

func inspectImage(ctx context.Context, f *os.File) (InspectionFacts, error) {
	r := bufio.NewReader(contextReader{ctx, io.LimitReader(f, maxFileBytes+1)})
	cfg, format, err := image.DecodeConfig(r)
	if err != nil {
		if ctx.Err() != nil {
			return InspectionFacts{}, ctx.Err()
		}
		return InspectionFacts{}, safeError("image_invalid", errors.Join(ErrInvalidMedia, err))
	}
	if format != "png" && format != "jpeg" && format != "gif" {
		return InspectionFacts{}, ErrInvalidMedia
	}
	if !dimensions(cfg.Width, cfg.Height) {
		return InspectionFacts{}, ErrInspectionLimit
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return InspectionFacts{}, safeError("image_invalid", errors.Join(ErrInvalidMedia, err))
	}
	img, decodedFormat, err := image.Decode(contextReader{ctx, io.LimitReader(f, maxFileBytes+1)})
	if err != nil {
		if ctx.Err() != nil {
			return InspectionFacts{}, ctx.Err()
		}
		return InspectionFacts{}, safeError("image_invalid", errors.Join(ErrInvalidMedia, err))
	}
	if format != decodedFormat || img.Bounds().Dx() != cfg.Width || img.Bounds().Dy() != cfg.Height {
		return InspectionFacts{}, ErrInvalidMedia
	}
	return InspectionFacts{Kind: domain.AssetKindImage, Width: cfg.Width, Height: cfg.Height, CodecName: format, VisualDecoded: true}, nil
}

type nativeProbe struct {
	Programs     []struct{} `json:"programs"`
	StreamGroups []struct{} `json:"stream_groups"`
	Format       struct {
		Duration string `json:"duration"`
	} `json:"format"`
	Streams []struct {
		Duration   string `json:"duration"`
		CodecType  string `json:"codec_type"`
		CodecName  string `json:"codec_name"`
		Width      int    `json:"width"`
		Height     int    `json:"height"`
		FrameRate  string `json:"avg_frame_rate"`
		SampleRate string `json:"sample_rate"`
		Channels   int    `json:"channels"`
	} `json:"streams"`
}

func parseProbe(body []byte) (InspectionFacts, error) {
	if err := uniqueJSON(json.NewDecoder(bytes.NewReader(body)), "root", 0); err != nil {
		return InspectionFacts{}, ErrInvalidMedia
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	var p nativeProbe
	if err := d.Decode(&p); err != nil {
		return InspectionFacts{}, ErrInvalidMedia
	}
	if d.Decode(new(any)) != io.EOF || len(p.Streams) == 0 || len(p.Streams) > 64 || len(p.Programs) > 0 || len(p.StreamGroups) > 0 {
		return InspectionFacts{}, ErrInvalidMedia
	}
	durationText := p.Format.Duration
	if durationText == "" {
		for _, s := range p.Streams {
			if durationText == "" || s.CodecType == "video" {
				durationText = s.Duration
			}
			if s.CodecType == "video" {
				break
			}
		}
	}
	seconds, err := strconv.ParseFloat(durationText, 64)
	if err != nil {
		return InspectionFacts{}, ErrInvalidMedia
	}
	duration, err := SecondsDuration(seconds)
	if err != nil {
		return InspectionFacts{}, err
	}
	facts := InspectionFacts{Kind: domain.AssetKindAudio, Duration: duration}
	for _, s := range p.Streams {
		if !validCodec(s.CodecName) {
			return InspectionFacts{}, ErrInvalidMedia
		}
		if s.Duration != "" {
			seconds, e := strconv.ParseFloat(s.Duration, 64)
			if e != nil {
				return InspectionFacts{}, ErrInvalidMedia
			}
			if _, e = SecondsDuration(seconds); e != nil {
				return InspectionFacts{}, e
			}
		}
		switch s.CodecType {
		case "video":
			if !dimensions(s.Width, s.Height) {
				return InspectionFacts{}, ErrInspectionLimit
			}
			fps, e := nativeFPS(s.FrameRate)
			if e != nil {
				return InspectionFacts{}, e
			}
			if facts.Kind != domain.AssetKindVideo {
				facts.Kind = domain.AssetKindVideo
				facts.Width = s.Width
				facts.Height = s.Height
				facts.FPS = fps
				facts.CodecName = s.CodecName
			}
		case "audio":
			rate, e := strconv.Atoi(s.SampleRate)
			if e != nil || rate <= 0 || rate > 768000 || s.Channels <= 0 || s.Channels > 64 {
				return InspectionFacts{}, ErrInvalidMedia
			}
			facts.AudioTracks++
			if facts.AudioTracks == 1 {
				facts.SampleRate = rate
				facts.Channels = s.Channels
				if facts.CodecName == "" {
					facts.CodecName = s.CodecName
				}
			}
		default:
			return InspectionFacts{}, ErrInvalidMedia
		}
	}
	return facts, nil
}

func nativeFPS(text string) (float64, error) {
	num, den, ok := strings.Cut(text, "/")
	n, err := strconv.ParseFloat(num, 64)
	if err != nil || !finite(n) || n <= 0 {
		return 0, ErrInvalidMedia
	}
	if ok {
		d, e := strconv.ParseFloat(den, 64)
		if e != nil || !finite(d) || d <= 0 {
			return 0, ErrInvalidMedia
		}
		n /= d
	}
	if !finite(n) || n <= 0 {
		return 0, ErrInvalidMedia
	}
	return n, nil
}

// The bounded native schema rejects duplicate object keys and excessive nesting,
// in addition to DisallowUnknownFields. No reflection or permissive map decoding.
func uniqueJSON(d *json.Decoder, section string, depth int) error {
	if depth > 8 {
		return ErrInvalidMedia
	}
	t, err := d.Token()
	if err != nil {
		return err
	}
	delim, ok := t.(json.Delim)
	if !ok {
		return nil
	}
	if delim != '{' && delim != '[' {
		return ErrInvalidMedia
	}
	keys := map[string]bool{}
	allowed := ""
	switch section {
	case "root":
		allowed = "|format|streams|programs|stream_groups|"
	case "format":
		allowed = "|duration|"
	case "streams":
		allowed = "|duration|codec_type|codec_name|width|height|avg_frame_rate|sample_rate|channels|"
	}
	for d.More() {
		nextSection := section
		if delim == '{' {
			key, e := d.Token()
			if e != nil {
				return e
			}
			s, ok := key.(string)
			if !ok || keys[s] || !strings.Contains(allowed, "|"+s+"|") {
				return ErrInvalidMedia
			}
			keys[s] = true
			nextSection = s
		}
		if err := uniqueJSON(d, nextSection, depth+1); err != nil {
			return err
		}
	}
	_, err = d.Token()
	return err
}
