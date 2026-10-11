package intelligence

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/manish96170/videofactory/domain"
	"github.com/manish96170/videofactory/provider"
	"github.com/manish96170/videofactory/render"
	"github.com/manish96170/videofactory/resource"
	"github.com/manish96170/videofactory/validation"
)

type safeFailure struct {
	code  string
	cause error
}

func (e *safeFailure) Error() string           { return "intelligence: " + e.code }
func (e *safeFailure) Unwrap() error           { return e.cause }
func safeError(code string, cause error) error { return &safeFailure{code, cause} }
func blank(s string) bool                      { return !utf8.ValidString(s) || strings.TrimSpace(s) == "" }
func isNil(v any) bool {
	if v == nil {
		return true
	}
	r := reflect.ValueOf(v)
	switch r.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan:
		return r.IsNil()
	}
	return false
}
func verdict(v domain.DecisionVerdict, empty bool) bool {
	return v == domain.DecisionYes || v == domain.DecisionNo || v == domain.DecisionUncertain || empty && v == ""
}
func callbackError(ctx, child context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if child.Err() != nil {
		return child.Err()
	}
	return err
}

func clonePlanRequest(req resource.PlanRequest) resource.PlanRequest {
	req.Capabilities = resource.CloneCapabilities(req.Capabilities)
	if req.Remote != nil {
		remote := *req.Remote
		remote.Route = remote.Route.Clone()
		req.Remote = &remote
	}
	return req
}

// Encoded snapshot size is bounded to 1 MiB. encoding/json's internal encoding
// buffer is not kernel memory containment; callers own input allocation limits.
type boundedBuffer struct{ bytes.Buffer }

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if len(p) > 1<<20-b.Len() {
		return 0, ErrInvalidRequest
	}
	return b.Buffer.Write(p)
}
func snapshot(v, out any) error {
	var b boundedBuffer
	if err := json.NewEncoder(&b).Encode(v); err != nil {
		return err
	}
	return json.Unmarshal(b.Bytes(), out)
}
func renderSupport(t domain.PrimitiveType) bool { return render.SupportsPrimitive(t) }

// InputsReady covers properties, NOT native binaries/fonts or actual rendering.
// Image sources and unsupported/unknown properties remain unknown rather than
// duplicating the renderer's filter graph or trusting arbitrary assetPath values.
func cheapInputs(shot domain.Shot, caps []domain.PrimitiveCapabilities) Fact {
	ready := Yes
	backgrounds, transitions := 0, 0
	for i, p := range shot.Primitives {
		for _, key := range caps[i].RequiredProperties {
			if _, ok := p.Properties[key].(string); !ok {
				return No
			}
		}
		if !renderSupport(p.Type) || p.Type == domain.PrimitiveTypeImage {
			ready = Unknown
			continue
		}
		allowed := map[string]bool{"color": true}
		switch p.Type {
		case domain.PrimitiveTypeBackground:
			backgrounds++
			if backgrounds > 1 {
				return No
			}
		case domain.PrimitiveTypeText:
			allowed["text"], allowed["bounds"], allowed["fontSize"] = true, true, true
		case domain.PrimitiveTypeTransition:
			transitions++
			if transitions > 1 {
				return No
			}
			allowed = map[string]bool{"style": true}
			if v, ok := p.Properties["style"]; ok && v != "fade-in" && v != "fade-out" {
				return No
			}
		}
		for k, v := range p.Properties {
			if !allowed[k] {
				ready = Unknown
				continue
			}
			switch k {
			case "color":
				s, ok := v.(string)
				if !ok || !hexColor.MatchString(s) {
					return No
				}
			case "fontSize":
				n, ok := v.(float64)
				if !ok || math.IsNaN(n) || math.IsInf(n, 0) || n <= 0 {
					return No
				}
			case "bounds":
				b, ok := v.(map[string]any)
				if !ok {
					return No
				}
				x, xok := b["x"].(float64)
				y, yok := b["y"].(float64)
				w, wok := b["width"].(float64)
				h, hok := b["height"].(float64)
				if !xok || !yok || !wok || !hok || math.IsNaN(x+y+w+h) || math.IsInf(x+y+w+h, 0) || x < 0 || y < 0 || w <= 0 || h <= 0 || x+w > 1.0001 || y+h > 1.0001 {
					return No
				}
			}
		}
	}
	return ready
}

var hexColor = regexp.MustCompile(`^#?[0-9A-Fa-f]{6}$`)

// Retain only frozen core issue vocabulary; error/unknown warning evidence may
// never turn into affirmative reuse. Raw details and failure text are discarded.
func cleanValidation(v domain.ValidationResult, id, target domain.ID, q domain.Quality) (domain.ValidationResult, bool) {
	clean := domain.ValidationResult{ID: id, TargetID: target, Status: domain.ValidationFailed, FailureClass: "media"}
	if v.ID != id || v.TargetID != target || v.Details["quality"] != string(q) {
		return clean, false
	}
	switch v.Status {
	case domain.ValidationPassed, domain.ValidationWarning:
		if v.FailureClass != "" || v.RetryRecommended {
			return clean, false
		}
	case domain.ValidationFailed:
		if v.FailureClass != "media" {
			return clean, false
		}
	default:
		return clean, false
	}
	var issues []validation.Issue
	b, err := json.Marshal(v.Details["issues"])
	if err != nil || len(b) > 8192 || len(b) == 0 || b[0] != '[' {
		return clean, false
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&issues) != nil || len(issues) > 32 {
		return clean, false
	}
	hasError := false
	for _, i := range issues {
		if strings.Contains(i.Code, "|") || !strings.Contains("|kind_mismatch|invalid_codec|invalid_duration|duration_mismatch|invalid_dimensions|resolution_mismatch|visual_decode_failed|invalid_fps|fps_mismatch|invalid_audio|audio_missing|uniform_frame_heuristic|inspection_failed|inspection_limit|unauthorized_source|missing_media|runtime_missing|inspection_timeout|", "|"+i.Code+"|") || (i.Severity != "error" && i.Severity != "warning") {
			return clean, false
		}
		if (i.Severity == "warning") != (i.Code == "uniform_frame_heuristic") {
			return clean, false
		}
		hasError = hasError || i.Severity == "error"
	}
	if v.Status == domain.ValidationPassed && len(issues) != 0 || v.Status == domain.ValidationWarning && (len(issues) == 0 || hasError) || v.Status == domain.ValidationFailed && !hasError {
		return clean, false
	}
	clean.Details = map[string]any{"quality": string(q), "issues": append([]validation.Issue(nil), issues...)}
	clean.Status, clean.FailureClass, clean.RetryRecommended = v.Status, v.FailureClass, v.RetryRecommended
	return clean, v.Status != domain.ValidationFailed
}

// cleanRouteResult admits only structurally coherent results and frozen core
// rejection codes. It validates the router's response shape, not provider
// admission; route eligibility remains the router's responsibility.
func cleanRouteResult(r provider.RouteResult) (provider.RouteResult, bool) {
	clean := provider.RouteResult{
		EligibleIDs: append([]string(nil), r.EligibleIDs...),
		FallbackIDs: append([]string(nil), r.FallbackIDs...),
		Rejections:  make([]provider.Rejection, len(r.Rejections)),
	}
	validID := func(id string) bool {
		return !blank(id) && !strings.ContainsFunc(id, unicode.IsControl)
	}
	eligible := make(map[string]bool, len(r.EligibleIDs))
	for _, id := range r.EligibleIDs {
		if !validID(id) || eligible[id] {
			return provider.RouteResult{}, false
		}
		eligible[id] = true
	}
	if len(r.FallbackIDs) != max(0, len(r.EligibleIDs)-1) {
		return provider.RouteResult{}, false
	}
	for i, id := range r.FallbackIDs {
		if !validID(id) || id != r.EligibleIDs[i+1] {
			return provider.RouteResult{}, false
		}
	}
	seenRejections := make(map[string]bool, len(r.Rejections))
	for i, rejection := range r.Rejections {
		if !validID(rejection.ProviderID) || len(rejection.Codes) == 0 || eligible[rejection.ProviderID] || seenRejections[rejection.ProviderID] ||
			(i > 0 && r.Rejections[i-1].ProviderID >= rejection.ProviderID) {
			return provider.RouteResult{}, false
		}
		seenRejections[rejection.ProviderID] = true
		codes := append([]provider.RejectionCode(nil), rejection.Codes...)
		seenCodes := make(map[provider.RejectionCode]bool, len(codes))
		for _, code := range codes {
			if seenCodes[code] || !knownRejectionCode(code) {
				return provider.RouteResult{}, false
			}
			seenCodes[code] = true
		}
		clean.Rejections[i] = provider.Rejection{ProviderID: rejection.ProviderID, Codes: codes}
	}
	return clean, true
}

func knownRejectionCode(code provider.RejectionCode) bool {
	switch code {
	case provider.RejectHealth, provider.RejectTags, provider.RejectLocality,
		provider.RejectCostUnknown, provider.RejectCostUnit, provider.RejectCostLimit,
		provider.RejectLatencyUnknown, provider.RejectLatencyLimit,
		provider.RejectHardwareUnknown, provider.RejectMemoryUnknown,
		provider.RejectMemoryLimit, provider.RejectGPUUnknown, provider.RejectGPUUnavailable:
		return true
	default:
		return false
	}
}

func (a *Analyzer) route(ctx context.Context, req Request, out Result, tags []string) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if len(tags) == 0 {
		out.Code = "capabilities_unknown"
		return out, nil
	}
	policy := req.Route.Clone()
	policy.RequiredTags = unique(append(policy.RequiredTags, tags...))
	// No built-in audio capability vocabulary exists. Caller must explicitly
	// supply audio/composition tags for ANY new video. Tags grant eligibility,
	// never prove that audio was composed; verified video reuse bypasses routing.
	if req.Requirements.AudioRequired {
		out.MissingAudioReadiness = true
		if len(req.AudioRequiredTags) == 0 {
			out.Code = "audio_composition_required"
			return out, nil
		}
		policy.RequiredTags = unique(append(policy.RequiredTags, req.AudioRequiredTags...))
	}
	out.RequiredTags = append([]string(nil), policy.RequiredTags...)
	child, cancel := context.WithTimeout(ctx, 5_000_000_000)
	r, e := a.router.Route(child, policy.Clone())
	e = callbackError(ctx, child, e)
	cancel()
	if ctx.Err() != nil {
		return Result{}, ctx.Err()
	}
	if e != nil && !errors.Is(e, provider.ErrNoEligibleProviders) {
		out.Code = "routing_error"
		return out, nil
	}
	var owned provider.RouteResult
	if snapshot(r, &owned) != nil {
		out.Code = "routing_error"
		return out, nil
	}
	owned, valid := cleanRouteResult(owned)
	if !valid || e != nil && len(owned.EligibleIDs) != 0 {
		out.Code = "routing_error"
		return out, nil
	}
	out.Routing = &owned
	if e != nil || len(owned.EligibleIDs) == 0 {
		out.Code = "no_eligible_provider"
		return out, nil
	}
	if a.planner == nil {
		return out, nil
	}
	selected := owned.EligibleIDs[0]
	var binding resource.PlanRequest
	matches := 0
	for _, b := range req.Bindings {
		if b.Capabilities.ProviderID == selected {
			binding = b
			matches++
		}
	}
	if matches != 1 || binding.ID != req.ID || binding.TargetID != req.ShotID || blank(binding.Capabilities.ModelID) || blank(binding.ProfileID) || binding.Capabilities.Requirements == nil || binding.Remote != nil {
		return out, nil
	}
	// RouteResult has no provider-locality facts. Default/local-only admission
	// proves locality; Any/Remote require richer evidence and remain unknown in
	// this tranche. Never accept a remote model as a local-ready binding.
	if (policy.Locality != "" && policy.Locality != provider.LocalOnly) || !binding.Capabilities.Local {
		out.Code = "resource_unknown"
		return out, nil
	}
	for _, tag := range policy.RequiredTags {
		if !binding.Capabilities.Supports(tag) {
			return out, nil
		}
	}
	frames, ok := frameCount(req.Requirements)
	if !ok || binding.Workload.Width != req.Requirements.Width || binding.Workload.Height != req.Requirements.Height {
		return out, nil
	}
	if out.Strategy == StaticImage {
		frames = 1
	}
	if binding.Workload.Frames != frames || !(binding.Workload.Quality == string(req.Requirements.Quality) || req.Requirements.Quality == domain.QualityDraft && binding.Workload.Quality == "preview") {
		return out, nil
	}
	profile, e := resource.Profile(binding.Capabilities, binding.ProfileID)
	if e != nil || resource.SupportsWorkload(profile, binding.Workload) != nil {
		return out, nil
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	child, cancel = context.WithTimeout(ctx, 5_000_000_000)
	p, e := a.planner.Plan(child, clonePlanRequest(binding))
	e = callbackError(ctx, child, e)
	cancel()
	if ctx.Err() != nil {
		return Result{}, ctx.Err()
	}
	if e != nil || !matchingPlan(binding, profile, p) {
		out.Code = "resource_unknown"
		return out, nil
	}
	// Rationale is core-owned. This seam cannot attest the hardware-class label,
	// so omit it without mutating callback-owned provenance. Other strings below
	// match the trusted binding or are validated hash/backend/status vocabulary.
	p.Plan.Rationale = string(p.Status)
	provenance := *p.Plan.Provenance
	provenance.HardwareClass = ""
	p.Plan.Provenance = &provenance
	var ownedPlan resource.PlanResult
	if snapshot(p, &ownedPlan) != nil {
		return out, nil
	}
	out.Resource, out.ResourceStatus = &ownedPlan, string(p.Status)
	return out, nil
}

// Check the frozen binding and return evidence, not a second planner or lease.
// Local ineligibility may carry a valid estimate (e.g. permanent over-budget).
func matchingPlan(b resource.PlanRequest, profile domain.ExecutionProfile, p resource.PlanResult) bool {
	if p.Plan.ID != b.ID || p.Plan.TargetID != b.TargetID || p.Status != p.LocalStatus || p.Plan.PreferRemote ||
		(p.Status != resource.StatusLocalReady && p.Status != resource.StatusWait && p.Status != resource.StatusLocalIneligible) ||
		len(p.Remote.EligibleIDs)+len(p.Remote.FallbackIDs)+len(p.Remote.Rejections) != 0 {
		return false
	}
	r := p.Request
	if r.RAMBytes < 0 || r.CPU != profile.CPU || r.GPU != profile.GPU || r.Backend != profile.Backend || r.Exclusive != profile.Exclusive || ((profile.Heavy || r.GPU > 0) && !r.Heavy) ||
		p.Plan.RAMBytes != r.RAMBytes || p.Plan.CPU != r.CPU || p.Plan.GPU != r.GPU || p.Plan.Backend != r.Backend || p.Plan.Heavy != r.Heavy || p.Plan.Exclusive != r.Exclusive {
		return false
	}
	v := p.Plan.Provenance
	if v == nil || v.ProviderID != b.Capabilities.ProviderID || v.ModelID != b.Capabilities.ModelID || v.ModelRevision != b.Capabilities.Requirements.Revision ||
		v.ProfileID != profile.ID || v.ProfileRevision != profile.Revision || v.Workload != b.Workload || v.LegacyFloorRAMBytes < 0 || v.SafetyMarginPercent < 0 ||
		(v.HistoryHash != "" && !historyHash.MatchString(v.HistoryHash)) || (v.HistoryVersion > 0 && v.HistoryHash == "") {
		return false
	}
	declared := max(profile.LoadRAMBytes, profile.ResidentRAMBytes+profile.WorkspaceRAMBytes) // validated by resource.Profile
	floor, ok := legacyFloor(b.Capabilities.MinMemoryGB)
	if !ok || v.DeclaredPeakRAMBytes != declared || v.LegacyFloorRAMBytes != floor {
		return false
	}
	peak := max(declared, floor)
	if v.ObservedPeakRAMBytes != nil {
		if *v.ObservedPeakRAMBytes < 0 {
			return false
		}
		peak = max(peak, *v.ObservedPeakRAMBytes)
	}
	minimum, err := resource.ApplySafetyMargin(peak, v.SafetyMarginPercent)
	return err == nil && r.RAMBytes >= minimum
}

var historyHash = regexp.MustCompile(`^[0-9a-f]{64}$`)

func legacyFloor(gb float64) (int64, bool) {
	r, ok := new(big.Rat).SetString(strconv.FormatFloat(gb, 'g', -1, 64))
	if !ok || r.Sign() < 0 {
		return 0, false
	}
	r.Mul(r, new(big.Rat).SetInt64(resource.GB))
	n, rem := new(big.Int), new(big.Int)
	n.QuoRem(r.Num(), r.Denom(), rem)
	if rem.Sign() > 0 {
		n.Add(n, big.NewInt(1))
	}
	return n.Int64(), n.IsInt64()
}

// Exact ceil(durationNs * round-trippable FPS / 1e9), without float overflow or
// rounding a fractional frame down; int conversion is guarded for the platform.
func frameCount(r validation.Requirements) (int, bool) {
	f, ok := new(big.Rat).SetString(strconv.FormatFloat(r.FPS, 'g', -1, 64))
	if !ok || f.Sign() <= 0 {
		return 0, false
	}
	f.Mul(f, new(big.Rat).SetInt64(int64(r.ExpectedDuration)))
	f.Quo(f, big.NewRat(1_000_000_000, 1))
	n, rem := new(big.Int), new(big.Int)
	n.QuoRem(f.Num(), f.Denom(), rem)
	if rem.Sign() > 0 {
		n.Add(n, big.NewInt(1))
	}
	if !n.IsInt64() || n.Sign() <= 0 || n.Cmp(new(big.Int).SetUint64(uint64(^uint(0)>>1))) > 0 {
		return 0, false
	}
	return int(n.Int64()), true
}
