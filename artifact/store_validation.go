package artifact

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"strings"

	"github.com/manish96170/videofactory/domain"
)

func safeHandle(s string) bool {
	if len(s) == 0 || len(s) > 128 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func validIdentity(id Identity) bool {
	if !safeHandle(string(id.ProjectID)) || !safeHandle(string(id.ShotID)) || id.Quality != domain.QualityDraft && id.Quality != domain.QualityFinal || len(id.Key) != 64 {
		return false
	}
	for _, c := range id.Key {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func strictJSON(data []byte, dst any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return ErrStoreInput
	}
	if d.Decode(new(any)) != io.EOF {
		return ErrStoreInput
	}
	return nil
}

func validateEntry(e Entry) error {
	if !validIdentity(e.Identity) || !safeHandle(string(e.Manifest.ID)) || e.Manifest.ShotID != e.Identity.ShotID || e.Manifest.MediaPath != e.Media.Name {
		return ErrStoreInput
	}
	seen := make(map[domain.ID]bool)
	for _, element := range e.Manifest.Elements {
		if !safeHandle(string(element.ID)) || seen[element.ID] || element.ShotID != e.Identity.ShotID || !safeHandle(element.Type) || element.Start < 0 || element.End <= element.Start {
			return ErrStoreInput
		}
		seen[element.ID] = true
		if b := element.Bounds; b != nil {
			if !finite(b.X) || !finite(b.Y) || !finite(b.Width) || !finite(b.Height) || b.X < 0 || b.Y < 0 || b.Width <= 0 || b.Height <= 0 || b.X+b.Width > 1 || b.Y+b.Height > 1 {
				return ErrStoreInput
			}
		}
		if !safeJSON(element.EditableProperties, 0) {
			return ErrStoreInput
		}
	}
	if g := e.Generation; g != nil {
		if !safeHandle(string(g.ID)) || !safeHandle(string(g.RequestID)) || g.AssetID != "" && !safeHandle(string(g.AssetID)) || g.DurationMs < 0 || g.PeakMemoryMB != nil && (!finite(*g.PeakMemoryMB) || *g.PeakMemoryMB < 0) || !safeText(g.Seed) || !safeJSON(g.Metadata, 0) {
			return ErrStoreInput
		}
	}
	if v := e.Validation; v != nil {
		targetMatches := v.TargetID == e.Identity.ShotID || e.Generation != nil && e.Generation.AssetID != "" && v.TargetID == e.Generation.AssetID
		if !safeHandle(string(v.ID)) || !targetMatches || v.Status != domain.ValidationPassed && v.Status != domain.ValidationWarning && v.Status != domain.ValidationFailed || v.FailureClass != "" && !safeHandle(v.FailureClass) || !safeJSON(v.Details, 0) {
			return ErrStoreInput
		}
	}
	if p := e.Provenance; p != nil {
		if p.Seed != nil && (blank(*p.Seed) || !safeText(*p.Seed)) {
			return ErrStoreInput
		}
		for _, r := range []*Revision{p.Provider, p.Model, p.Profile} {
			if r != nil && (blank(r.Name) || blank(r.Revision) || !safeText(r.Name) || !safeText(r.Revision)) {
				return ErrStoreInput
			}
		}
		for _, digests := range []map[string]Digest{p.Sources, p.Dependencies} {
			for role, digest := range digests {
				if blank(role) || !safeText(role) || sensitiveKey(role) || digest == (Digest{}) {
					return ErrStoreInput
				}
			}
		}
	}
	return nil
}

// Only plain JSON-shaped extension values may enter persistence. In particular,
// encoding/json's willingness to turn a context/error/worker struct into {} (or
// execute a custom marshaler) does not make that object a safe artifact fact.
func plainJSON(value any, depth int) bool {
	if depth > 32 {
		return false
	}
	if value == nil {
		return true
	}
	if _, ok := value.(json.Marshaler); ok {
		return false
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Bool, reflect.String, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
		return true
	case reflect.Map:
		if v.Type().Key().Kind() != reflect.String {
			return false
		}
		iter := v.MapRange()
		for iter.Next() {
			if !plainJSON(iter.Value().Interface(), depth+1) {
				return false
			}
		}
		return true
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			if !plainJSON(v.Index(i).Interface(), depth+1) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

// Metadata is already JSON-snapshotted, so only JSON primitives remain. The
// depth bound prevents pathological extension trees. This is a conservative
// structural guard, not a secret detector: callers own the nonsecret contract.
func safeJSON(v any, depth int) bool {
	if depth > 32 {
		return false
	}
	switch value := v.(type) {
	case nil, bool:
		return true
	case float64:
		return finite(value)
	case string:
		return safeText(value)
	case []any:
		for _, child := range value {
			if !safeJSON(child, depth+1) {
				return false
			}
		}
		return true
	case map[string]any:
		for key, child := range value {
			if !safeText(key) || sensitiveKey(key) || !safeJSON(child, depth+1) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func sensitiveKey(key string) bool {
	key = strings.ToLower(strings.NewReplacer("_", "", "-", "", ".", "").Replace(key))
	for _, word := range []string{"prompt", "secret", "password", "credential", "token", "apikey", "rawerror"} {
		if strings.Contains(key, word) {
			return true
		}
	}
	return false
}

func safeText(s string) bool {
	if strings.ContainsRune(s, 0) || strings.Contains(s, "://") || strings.Contains(s, "\\") {
		return false
	}
	// Reject path-shaped strings anywhere in prose as well as bare paths.
	for _, word := range strings.Fields(s) {
		if strings.HasPrefix(word, "/") || strings.HasPrefix(word, "~") || strings.Contains(word, "../") || word == ".." || len(word) > 1 && word[1] == ':' {
			return false
		}
	}
	return true
}
