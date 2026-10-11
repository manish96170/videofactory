package validation

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/manish96170/videofactory/domain"
)

type inspectFunc func(context.Context, InspectionRequest) (InspectionFacts, error)

func (f inspectFunc) Inspect(ctx context.Context, r InspectionRequest) (InspectionFacts, error) {
	return f(ctx, r)
}

type nilInspector struct{}

func (*nilInspector) Inspect(context.Context, InspectionRequest) (InspectionFacts, error) {
	panic("typed nil invoked")
}

func videoRequest() Request {
	return Request{ID: "check", TargetID: "asset", Asset: domain.Asset{ID: "asset", Kind: domain.AssetKindVideo, URI: "private-source"}, Requirements: Requirements{Kind: domain.AssetKindVideo, Quality: QualityDraft, ExpectedDuration: time.Second, FPS: 30, Width: 64, Height: 32}}
}
func videoFacts() InspectionFacts {
	return InspectionFacts{Kind: domain.AssetKindVideo, Duration: time.Second, FPS: 30, Width: 64, Height: 32, VisualDecoded: true, CodecName: "h264"}
}
func checker(t *testing.T, f Inspector, cfg Config) *MediaChecker {
	t.Helper()
	v, err := NewMediaChecker(f, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestRequirementsAndTolerances(t *testing.T) {
	for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), 0, -1} {
		r := videoRequest().Requirements
		r.FPS = value
		if !errors.Is(r.Validate(), ErrInvalidRequest) {
			t.Errorf("accepted FPS %v", value)
		}
	}
	for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), 0, -1, 1e-12, float64(math.MaxInt64) / 1e9, 1e30} {
		if _, err := SecondsDuration(value); !errors.Is(err, ErrInvalidMedia) {
			t.Errorf("accepted seconds %v", value)
		}
	}
	if d, err := SecondsDuration(1.25); err != nil || d != 1250*time.Millisecond {
		t.Fatal(d, err)
	}
	for _, change := range []func(*Requirements){func(r *Requirements) { r.Quality = "secret" }, func(r *Requirements) { r.ExpectedDuration = 0 }, func(r *Requirements) { r.Width = 0 }, func(r *Requirements) { r.Height = -1 }, func(r *Requirements) { r.Width = 8193 }, func(r *Requirements) { r.Width = 8192; r.Height = 8192 }} {
		r := videoRequest().Requirements
		change(&r)
		if r.Validate() == nil {
			t.Fatal("invalid requirement accepted")
		}
	}
	for _, tc := range []struct {
		duration time.Duration
		fps      float64
		passed   bool
	}{
		{850 * time.Millisecond, 29.99, true}, {1150 * time.Millisecond, 30.01, true},
		{850*time.Millisecond - time.Nanosecond, 30, false}, {1150*time.Millisecond + time.Nanosecond, 30, false},
		{time.Second, 29.989999, false}, {time.Second, 30.010001, false},
		{time.Duration(math.MaxInt64), 30, false}, {-time.Second, 30, false}, {time.Second, math.NaN(), false}, {time.Second, math.Inf(1), false},
	} {
		facts := videoFacts()
		facts.Duration = tc.duration
		facts.FPS = tc.fps
		v := checker(t, inspectFunc(func(context.Context, InspectionRequest) (InspectionFacts, error) { return facts, nil }), DefaultConfig())
		result, err := v.Validate(context.Background(), videoRequest())
		if err != nil || (result.Status == domain.ValidationPassed) != tc.passed {
			t.Errorf("%+v: %+v %v", tc, result, err)
		}
	}
}

func TestMeasuredKindsAudioImagesAndMetadata(t *testing.T) {
	for _, tc := range []struct {
		name   string
		kind   domain.AssetKind
		change func(*Request, *InspectionFacts)
		status domain.ValidationStatus
	}{
		{"video_no_audio", domain.AssetKindVideo, func(*Request, *InspectionFacts) {}, domain.ValidationPassed},
		{"required_missing", domain.AssetKindVideo, func(r *Request, f *InspectionFacts) { r.Requirements.AudioRequired = true }, domain.ValidationFailed},
		{"required_present", domain.AssetKindVideo, func(r *Request, f *InspectionFacts) {
			r.Requirements.AudioRequired = true
			f.AudioTracks = 1
			f.SampleRate = 48000
			f.Channels = 2
			f.AudioDecoded = true
		}, domain.ValidationPassed},
		{"optional_invalid", domain.AssetKindVideo, func(r *Request, f *InspectionFacts) {
			f.AudioTracks = 1
			f.SampleRate = 0
			f.Channels = 2
			f.AudioDecoded = true
		}, domain.ValidationFailed},
		{"audio", domain.AssetKindAudio, func(r *Request, f *InspectionFacts) {
			f.AudioTracks = 1
			f.SampleRate = 8000
			f.Channels = 1
			f.AudioDecoded = true
		}, domain.ValidationPassed},
		{"image", domain.AssetKindImage, func(*Request, *InspectionFacts) {}, domain.ValidationPassed},
		{"image_min", domain.AssetKindImage, func(r *Request, f *InspectionFacts) {
			r.Requirements.ImageMinimumDimensions = true
			f.Width = 128
			f.Height = 64
		}, domain.ValidationPassed},
		{"image_exact", domain.AssetKindImage, func(r *Request, f *InspectionFacts) { f.Width = 128 }, domain.ValidationFailed},
		{"uniform_valid", domain.AssetKindVideo, func(r *Request, f *InspectionFacts) { yes := true; f.UniformFrame = &yes }, domain.ValidationPassed},
		{"uniform_warning", domain.AssetKindVideo, func(r *Request, f *InspectionFacts) {
			yes := true
			f.UniformFrame = &yes
			r.Requirements.WarnUniformFrame = true
		}, domain.ValidationWarning},
		{"decode_missing", domain.AssetKindVideo, func(r *Request, f *InspectionFacts) { f.VisualDecoded = false }, domain.ValidationFailed},
		{"codec_missing", domain.AssetKindVideo, func(r *Request, f *InspectionFacts) { f.CodecName = "" }, domain.ValidationFailed},
		{"kind_mismatch", domain.AssetKindVideo, func(r *Request, f *InspectionFacts) { f.Kind = domain.AssetKindImage }, domain.ValidationFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, f := videoRequest(), videoFacts()
			r.Asset.Kind = tc.kind
			r.Requirements.Kind = tc.kind
			f.Kind = tc.kind
			if tc.kind == domain.AssetKindImage {
				r.Requirements.ExpectedDuration = 0
				r.Requirements.FPS = 0
				f.Duration = 0
				f.FPS = 0
			}
			if tc.kind == domain.AssetKindAudio {
				r.Requirements.Width = 0
				r.Requirements.Height = 0
				r.Requirements.FPS = 0
				f.Width = 0
				f.Height = 0
				f.FPS = 0
				f.VisualDecoded = false
			}
			r.Asset.Metadata = map[string]any{"fps": math.NaN(), "duration": 1e30, "quality": "secret", "path": "private-source"}
			tc.change(&r, &f)
			v := checker(t, inspectFunc(func(context.Context, InspectionRequest) (InspectionFacts, error) { return f, nil }), DefaultConfig())
			got, err := v.Validate(context.Background(), r)
			if err != nil || got.Status != tc.status || got.RetryRecommended {
				t.Fatal(got, err)
			}
			body, err := json.Marshal(got)
			if err != nil || strings.Contains(string(body), "private-source") || strings.Contains(string(body), "secret") {
				t.Fatal("unsafe/non-JSON details", err)
			}
		})
	}
}

func TestDependenciesCancellationRetryAndSafeErrors(t *testing.T) {
	var p *nilInspector
	var f inspectFunc
	for _, dep := range []Inspector{nil, p, f} {
		if _, err := NewMediaChecker(dep, DefaultConfig()); !errors.Is(err, ErrInvalidDependency) {
			t.Fatal(err)
		}
	}
	for _, cfg := range []Config{{}, {InspectionTimeout: time.Second, FPSTolerance: math.NaN()}, {InspectionTimeout: time.Second, DurationTolerance: -1}} {
		if _, err := NewMediaChecker(inspectFunc(func(context.Context, InspectionRequest) (InspectionFacts, error) { return videoFacts(), nil }), cfg); err == nil {
			t.Fatal("invalid config")
		}
	}
	for _, tc := range []struct {
		name  string
		err   error
		retry bool
	}{
		{"permanent", errors.New("private-source secret native stderr"), false},
		{"transient", errors.Join(ErrTransientInspection, errors.New("private-source")), true},
		{"inspection_limit_with_deadline", errors.Join(ErrInspectionLimit, context.DeadlineExceeded), false},
		{"missing", ErrMissingMedia, false}, {"runtime", ErrRuntimeMissing, false},
		{"fabricated_deadline", context.DeadlineExceeded, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := checker(t, inspectFunc(func(context.Context, InspectionRequest) (InspectionFacts, error) { return videoFacts(), tc.err }), DefaultConfig())
			got, err := v.Validate(context.Background(), videoRequest())
			if err != nil || got.Status != domain.ValidationFailed || got.RetryRecommended != tc.retry {
				t.Fatal(got, err)
			}
			b, _ := json.Marshal(got)
			if strings.Contains(string(b), "private-source") || strings.Contains(string(b), "stderr") {
				t.Fatal("raw error leak")
			}
		})
	}
	for _, late := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		calls := 0
		v := checker(t, inspectFunc(func(context.Context, InspectionRequest) (InspectionFacts, error) {
			calls++
			cancel()
			return videoFacts(), errors.New("private")
		}), DefaultConfig())
		if !late {
			cancel()
		}
		got, err := v.Validate(ctx, videoRequest())
		if err != context.Canceled || !reflect.DeepEqual(got, domain.ValidationResult{}) || calls != map[bool]int{false: 0, true: 1}[late] {
			t.Fatal(got, err, calls)
		}
	}
	cfg := DefaultConfig()
	cfg.InspectionTimeout = time.Millisecond
	v := checker(t, inspectFunc(func(ctx context.Context, _ InspectionRequest) (InspectionFacts, error) {
		<-ctx.Done()
		return videoFacts(), nil
	}), cfg)
	got, err := v.Validate(context.Background(), videoRequest())
	if err != nil || !got.RetryRecommended || got.Status != domain.ValidationFailed {
		t.Fatal(got, err)
	}
	private := errors.New("private-source")
	err = safeError("native_failed", private)
	if !errors.Is(err, private) || strings.Contains(err.Error(), "private-source") {
		t.Fatal(err)
	}
	for _, dependencyError := range []error{ErrInvalidDependency, ErrInvalidRequest} {
		v := checker(t, inspectFunc(func(context.Context, InspectionRequest) (InspectionFacts, error) {
			return videoFacts(), dependencyError
		}), DefaultConfig())
		got, err := v.Validate(context.Background(), videoRequest())
		if !errors.Is(err, dependencyError) || !reflect.DeepEqual(got, domain.ValidationResult{}) {
			t.Fatal(got, err)
		}
	}
	r := videoRequest()
	r.Requirements.Quality = "secret-invalid"
	calls := 0
	v = checker(t, inspectFunc(func(context.Context, InspectionRequest) (InspectionFacts, error) { calls++; return videoFacts(), nil }), DefaultConfig())
	got, err = v.Validate(context.Background(), r)
	if err != ErrInvalidRequest || !reflect.DeepEqual(got, domain.ValidationResult{}) || calls != 0 {
		t.Fatal(got, err, calls)
	}
}

func TestLeadQualityAlias(t *testing.T) {
	var quality Quality = domain.QualityDraft
	var shared domain.Quality = QualityFinal
	r := videoRequest().Requirements
	r.Quality = domain.QualityDraft
	if quality != domain.QualityDraft || shared != domain.QualityFinal || r.Validate() != nil {
		t.Fatal("domain quality constants not accepted")
	}
	r.Quality = domain.QualityFinal
	if r.Validate() != nil {
		t.Fatal("domain final quality not accepted")
	}
	if reflect.TypeOf(QualityDraft) != reflect.TypeOf(domain.QualityDraft) {
		t.Fatal("validation quality is not the domain quality type")
	}
}

func TestLeadNilContextsAndPermanentRetryPriority(t *testing.T) {
	t.Run("nil_validator_context", func(t *testing.T) {
		defer func() {
			if recover() != nil {
				t.Error("nil context panicked")
			}
		}()
		calls := 0
		v := checker(t, inspectFunc(func(context.Context, InspectionRequest) (InspectionFacts, error) { calls++; return videoFacts(), nil }), DefaultConfig())
		got, err := v.Validate(nil, videoRequest())
		if err != ErrInvalidRequest || !reflect.DeepEqual(got, domain.ValidationResult{}) || calls != 0 {
			t.Fatal(got, err, calls)
		}
	})
	t.Run("nil_native_context", func(t *testing.T) {
		defer func() {
			if recover() != nil {
				t.Error("nil context panicked")
			}
		}()
		n, err := NewManagedNativeInspector(DefaultNativeConfig(t.TempDir()))
		if err != nil {
			t.Fatal(err)
		}
		got, err := n.Inspect(nil, InspectionRequest{URI: "nonexistent", Kind: domain.AssetKindVideo})
		if err != ErrInvalidRequest || !reflect.DeepEqual(got, InspectionFacts{}) {
			t.Fatal(got, err)
		}
	})
	for _, tc := range []struct {
		marker error
		code   string
	}{
		{ErrNonLocalMedia, "unauthorized_source"}, {ErrMissingMedia, "missing_media"},
		{ErrRuntimeMissing, "runtime_missing"}, {ErrInspectionLimit, "inspection_limit"}, {ErrInvalidMedia, "inspection_failed"},
	} {
		for _, expired := range []bool{false, true} {
			t.Run(tc.code+map[bool]string{false: "_transient", true: "_expired_child"}[expired], func(t *testing.T) {
				cfg := DefaultConfig()
				if expired {
					cfg.InspectionTimeout = time.Millisecond
				}
				cause := errors.Join(tc.marker, ErrTransientInspection)
				v := checker(t, inspectFunc(func(ctx context.Context, _ InspectionRequest) (InspectionFacts, error) {
					if expired {
						<-ctx.Done()
					}
					return videoFacts(), cause
				}), cfg)
				got, err := v.Validate(context.Background(), videoRequest())
				want := []Issue{{Code: tc.code, Severity: "error"}}
				if err != nil || got.Status != domain.ValidationFailed || got.RetryRecommended || !reflect.DeepEqual(got.Details["issues"], want) {
					t.Fatal(got, err)
				}
			})
		}
	}
}
