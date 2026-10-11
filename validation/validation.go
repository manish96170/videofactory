// Package validation verifies measured media facts without generating media,
// trusting asset metadata, acquiring resources, or performing semantic QA.
package validation

import (
	"context"
	"errors"
	"math"
	"reflect"
	"strings"
	"time"

	"github.com/manish96170/videofactory/domain"
)

type Quality = domain.Quality

const (
	QualityDraft = domain.QualityDraft
	QualityFinal = domain.QualityFinal
)

// Requirements are explicit, independent of the quality label. Image dimensions
// match exactly unless ImageMinimumDimensions is explicitly enabled for reuse.
// Image duration/FPS and audio dimensions/FPS must be zero (not applicable).
type Requirements struct {
	Kind                   domain.AssetKind `json:"kind"`
	Quality                Quality          `json:"quality"`
	ExpectedDuration       time.Duration    `json:"expectedDuration"`
	FPS                    float64          `json:"fps"`
	Width                  int              `json:"width"`
	Height                 int              `json:"height"`
	AudioRequired          bool             `json:"audioRequired,omitempty"`
	ImageMinimumDimensions bool             `json:"imageMinimumDimensions,omitempty"`
	WarnUniformFrame       bool             `json:"warnUniformFrame,omitempty"`
}

// IDs are nonsecret correlation handles. Asset.Metadata is never inspected or
// passed to a dependency. Callers must not mutate requests during Validate.
type Request struct {
	ID           domain.ID    `json:"id"`
	TargetID     domain.ID    `json:"targetId"`
	Asset        domain.Asset `json:"asset"`
	Requirements Requirements `json:"requirements"`
}

type Validator interface {
	Validate(context.Context, Request) (domain.ValidationResult, error)
}

type InspectionRequest struct {
	URI  string
	Kind domain.AssetKind
}

// InspectionFacts describe measured source media, not generation wall time.
// Audio fields describe the first audio stream; adapters must also validate
// every audio stream's positive sample rate/channels. A false decode fact is a
// failure, not an unknown success. UniformFrame is an optional heuristic only.
type InspectionFacts struct {
	Kind                              domain.AssetKind
	Duration                          time.Duration
	Width, Height                     int
	FPS                               float64
	CodecName                         string
	AudioTracks, SampleRate, Channels int
	VisualDecoded, AudioDecoded       bool
	UniformFrame                      *bool
}

// Inspector must cooperate with context and return stable facts. For concurrent
// validation it must be concurrency-safe. Payloads accompanying errors are ignored.
type Inspector interface {
	Inspect(context.Context, InspectionRequest) (InspectionFacts, error)
}

var (
	ErrInvalidRequest      = errors.New("validation: invalid_request")
	ErrInvalidDependency   = errors.New("validation: invalid_dependency")
	ErrTransientInspection = errors.New("validation: transient_inspection")
	ErrMissingMedia        = errors.New("validation: missing_media")
	ErrNonLocalMedia       = errors.New("validation: unauthorized_source")
	ErrInvalidMedia        = errors.New("validation: invalid_media")
	ErrInspectionLimit     = errors.New("validation: inspection_limit")
	ErrRuntimeMissing      = errors.New("validation: runtime_missing")
)

// Error prints only a core code. Unwrapped causes may contain sensitive OS data;
// callers must not log them without redaction.
type Error struct {
	code  string
	cause error
}

func (e *Error) Error() string                 { return "validation: " + e.code }
func (e *Error) Unwrap() error                 { return e.cause }
func safeError(code string, cause error) error { return &Error{code, cause} }

type Issue struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
}

type Config struct {
	InspectionTimeout time.Duration
	DurationTolerance time.Duration
	FPSTolerance      float64
}

func DefaultConfig() Config { return Config{5 * time.Second, 150 * time.Millisecond, 0.01} }

type MediaChecker struct {
	inspector Inspector
	config    Config
}

func NewMediaChecker(inspector Inspector, cfg Config) (*MediaChecker, error) {
	if isNil(inspector) {
		return nil, ErrInvalidDependency
	}
	if cfg.InspectionTimeout <= 0 || cfg.DurationTolerance < 0 || !finite(cfg.FPSTolerance) || cfg.FPSTolerance < 0 {
		return nil, ErrInvalidRequest
	}
	return &MediaChecker{inspector, cfg}, nil
}

func isNil(v any) bool {
	if v == nil {
		return true
	}
	switch reflect.ValueOf(v).Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflect.ValueOf(v).IsNil()
	}
	return false
}
func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
func dimensions(w, h int) bool {
	return w > 0 && h > 0 && w <= 8192 && h <= 8192 && int64(w)*int64(h) <= 16_777_216
}

func (r Requirements) Validate() error {
	if (r.Quality != QualityDraft && r.Quality != QualityFinal) || !finite(r.FPS) {
		return ErrInvalidRequest
	}
	valid := false
	switch r.Kind {
	case domain.AssetKindVideo:
		valid = r.ExpectedDuration > 0 && r.FPS > 0 && dimensions(r.Width, r.Height) && !r.ImageMinimumDimensions
	case domain.AssetKindImage:
		valid = dimensions(r.Width, r.Height) && r.ExpectedDuration == 0 && r.FPS == 0 && !r.AudioRequired
	case domain.AssetKindAudio:
		valid = r.ExpectedDuration > 0 && r.Width == 0 && r.Height == 0 && r.FPS == 0 && !r.ImageMinimumDimensions && !r.WarnUniformFrame
	}
	if !valid {
		return ErrInvalidRequest
	}
	return nil
}

func (v *MediaChecker) Validate(ctx context.Context, req Request) (domain.ValidationResult, error) {
	if ctx == nil {
		return domain.ValidationResult{}, ErrInvalidRequest
	}
	if err := ctx.Err(); err != nil {
		return domain.ValidationResult{}, err
	}
	if v == nil || isNil(v.inspector) {
		return domain.ValidationResult{}, ErrInvalidDependency
	}
	r := req.Requirements
	if strings.TrimSpace(string(req.ID)) == "" || strings.TrimSpace(string(req.TargetID)) == "" {
		return domain.ValidationResult{}, ErrInvalidRequest
	}
	if err := r.Validate(); err != nil {
		return domain.ValidationResult{}, err
	}
	child, cancel := context.WithTimeout(ctx, v.config.InspectionTimeout)
	defer cancel()
	var f InspectionFacts
	err := child.Err()
	if err == nil {
		f, err = v.inspector.Inspect(child, InspectionRequest{req.Asset.URI, r.Kind})
	}
	if parentErr := ctx.Err(); parentErr != nil {
		return domain.ValidationResult{}, parentErr
	}
	if child.Err() != nil {
		err = errors.Join(err, child.Err())
	}
	if errors.Is(err, ErrInvalidDependency) || errors.Is(err, ErrInvalidRequest) {
		return domain.ValidationResult{}, safeError("invalid_dependency", err)
	}
	result := domain.ValidationResult{ID: req.ID, TargetID: req.TargetID, Status: domain.ValidationPassed}
	issues := []Issue{}
	check := func(code string, valid bool) {
		if !valid {
			issues = append(issues, Issue{code, "error"})
			result.Status = domain.ValidationFailed
			result.FailureClass = "media"
		}
	}
	if err != nil {
		// Parent cancellation wins above. Permanent markers retain diagnostic
		// priority and forbid retry advice, even alongside an expired child.
		permanent := errors.Is(err, ErrInspectionLimit) || errors.Is(err, ErrNonLocalMedia) || errors.Is(err, ErrMissingMedia) || errors.Is(err, ErrRuntimeMissing) || errors.Is(err, ErrInvalidMedia)
		code := "inspection_failed"
		switch {
		case errors.Is(err, ErrInspectionLimit):
			code = "inspection_limit"
		case errors.Is(err, ErrNonLocalMedia):
			code = "unauthorized_source"
		case errors.Is(err, ErrMissingMedia):
			code = "missing_media"
		case errors.Is(err, ErrRuntimeMissing):
			code = "runtime_missing"
		case errors.Is(err, ErrInvalidMedia):
			code = "inspection_failed"
		case errors.Is(child.Err(), context.DeadlineExceeded) || errors.Is(err, ErrTransientInspection) && errors.Is(err, context.DeadlineExceeded):
			code = "inspection_timeout"
		}
		check(code, false)
		result.RetryRecommended = !permanent && (errors.Is(err, ErrTransientInspection) || errors.Is(child.Err(), context.DeadlineExceeded))
	} else {
		check("kind_mismatch", f.Kind == r.Kind && req.Asset.Kind == r.Kind)
		check("invalid_codec", validCodec(f.CodecName))
		if r.Kind != domain.AssetKindImage {
			check("invalid_duration", f.Duration > 0)
			if f.Duration > 0 {
				diff := f.Duration - r.ExpectedDuration
				check("duration_mismatch", diff <= v.config.DurationTolerance && diff >= -v.config.DurationTolerance)
			}
		}
		if r.Kind != domain.AssetKindAudio {
			check("invalid_dimensions", dimensions(f.Width, f.Height))
			match := f.Width == r.Width && f.Height == r.Height
			if r.ImageMinimumDimensions {
				match = f.Width >= r.Width && f.Height >= r.Height
			}
			check("resolution_mismatch", match)
			check("visual_decode_failed", f.VisualDecoded)
		}
		if r.Kind == domain.AssetKindVideo {
			check("invalid_fps", finite(f.FPS) && f.FPS > 0)
			if finite(f.FPS) && f.FPS > 0 {
				check("fps_mismatch", f.FPS >= r.FPS-v.config.FPSTolerance && f.FPS <= r.FPS+v.config.FPSTolerance)
			}
		}
		check("invalid_audio", f.AudioTracks >= 0 && f.AudioTracks <= 64)
		check("audio_missing", f.AudioTracks > 0 || !(r.AudioRequired || r.Kind == domain.AssetKindAudio))
		if f.AudioTracks > 0 {
			check("invalid_audio", f.SampleRate > 0 && f.SampleRate <= 768000 && f.Channels > 0 && f.Channels <= 64 && f.AudioDecoded)
		}
		check("kind_mismatch", r.Kind != domain.AssetKindImage || f.AudioTracks == 0)
		if len(issues) == 0 && r.WarnUniformFrame && f.UniformFrame != nil && *f.UniformFrame {
			issues = append(issues, Issue{"uniform_frame_heuristic", "warning"})
			result.Status = domain.ValidationWarning
		}
	}
	result.Details = map[string]any{"quality": string(r.Quality), "issues": issues}
	if err := ctx.Err(); err != nil {
		return domain.ValidationResult{}, err
	}
	return result, nil
}

func validCodec(name string) bool {
	return len(name) > 0 && len(name) <= 64 && strings.IndexFunc(name, func(r rune) bool { return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_') }) < 0
}

// SecondsDuration rejects nonfinite, nonpositive, sub-nanosecond, and overflowing
// native durations before conversion to signed nanoseconds.
func SecondsDuration(seconds float64) (time.Duration, error) {
	n := seconds * float64(time.Second)
	if !finite(n) || n < 1 || n >= float64(math.MaxInt64) {
		return 0, ErrInvalidMedia
	}
	return time.Duration(n), nil
}
