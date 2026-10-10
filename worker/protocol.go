package worker

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"unicode/utf8"

	"github.com/manish96170/videofactory/domain"
)

const (
	ProtocolVersion = 1
	MaxStdinBytes   = 1 << 20
	MaxStdoutBytes  = 1 << 20
	MaxStderrBytes  = 8 << 10
	MaxHeaderBytes  = 128
)

// Diagnostics are fixed core codes: never wrap runtime paths, argv, environment,
// stderr, payloads, or raw OS/codec/monitor errors.
var (
	ErrInvalidConfig  = errors.New("worker: invalid configuration")
	ErrInvalidRequest = errors.New("worker: invalid request")
	ErrInputLimit     = errors.New("worker: input limit exceeded")
	ErrOutputLimit    = errors.New("worker: output limit exceeded")
	ErrProtocol       = errors.New("worker: invalid protocol reply")
	ErrWorker         = errors.New("worker: runtime failed")
	ErrStart          = errors.New("worker: start failed")
	ErrMonitor        = errors.New("worker: native monitoring failed")
	ErrLimitExceeded  = errors.New("worker: RAM limit exceeded")
	ErrCleanup        = errors.New("worker: stop not proven")
	ErrUnsupported    = errors.New("worker: unsupported platform")
)

// Header contains bounded, nonsecret correlation handles. Only generate is
// supported in version 1; load/unload remain model-loader responsibilities.
type Header struct {
	Version         int       `json:"version"`
	Operation       string    `json:"operation"`
	RequestID       domain.ID `json:"requestId"`
	ProviderID      string    `json:"providerId"`
	ModelID         string    `json:"modelId"`
	ModelRevision   string    `json:"modelRevision"`
	ProfileID       string    `json:"profileId"`
	ProfileRevision string    `json:"profileRevision"`
}

// Request deliberately accepts bounded raw JSON, not an arbitrary map requiring
// a potentially unbounded Marshal allocation. Payload is the existing domain
// GenerationRequest schema. DecodeGenerationRequest provides the typed codec.
type Request struct {
	Header  Header          `json:"header"`
	Payload json.RawMessage `json:"payload"`
}

type Reply struct {
	Header    Header          `json:"header"`
	Status    string          `json:"status"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	ErrorCode string          `json:"errorCode,omitempty"`
}

func validHandle(s string) bool {
	if len(s) == 0 || len(s) > MaxHeaderBytes {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-_.:", c)) {
			return false
		}
	}
	return true
}

func validHeader(h Header) bool {
	if h.Version != ProtocolVersion || h.Operation != "generate" {
		return false
	}
	for _, s := range []string{string(h.RequestID), h.ProviderID, h.ModelID, h.ModelRevision, h.ProfileID, h.ProfileRevision} {
		if !validHandle(s) {
			return false
		}
	}
	return true
}

func strictJSON(raw []byte, dst any) error {
	// Scan the bounded original wire representation before decoding. Go's
	// struct decoder otherwise folds key case and overwrites duplicate fields,
	// so its validated DTO can disagree with the raw bytes we send/return.
	// json.Valid also enforces the standard decoder's finite nesting bound
	// before the recursive token walk; no unbounded Marshal/canonicalization.
	t := reflect.TypeOf(dst)
	if len(raw) > MaxStdoutBytes || t == nil || t.Kind() != reflect.Ptr || !utf8.Valid(raw) || !json.Valid(raw) {
		return ErrProtocol
	}
	scan := json.NewDecoder(bytes.NewReader(raw))
	scan.UseNumber()
	if err := scanExactJSONValue(scan, t.Elem()); err != nil {
		return ErrProtocol
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return ErrProtocol
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return ErrProtocol
	}
	return nil
}

// Struct boundaries use exactly their JSON tags, including nested known DTOs.
// Interface/map extensions allow arbitrary case-distinct keys but reject exact
// duplicate decoded keys. RawMessage stays opaque here: successful generation
// payloads receive their own typed scan, while error payloads are ignored.
func scanExactJSONValue(d *json.Decoder, t reflect.Type) error {
	for t != nil && t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t == reflect.TypeOf(json.RawMessage(nil)) {
		var opaque json.RawMessage
		if d.Decode(&opaque) != nil {
			return ErrProtocol
		}
		return nil
	}
	token, err := d.Token()
	if err != nil {
		return ErrProtocol
	}
	delim, container := token.(json.Delim)
	if !container {
		return nil
	}
	switch delim {
	case '{':
		var fields map[string]reflect.Type
		var element reflect.Type
		if t != nil && t.Kind() == reflect.Struct {
			fields = make(map[string]reflect.Type, t.NumField())
			for i := 0; i < t.NumField(); i++ {
				f := t.Field(i)
				if f.PkgPath != "" {
					continue
				}
				name := strings.Split(f.Tag.Get("json"), ",")[0]
				if name == "-" {
					continue
				}
				if name == "" {
					name = f.Name
				}
				fields[name] = f.Type
			}
		} else if t != nil && t.Kind() == reflect.Map {
			element = t.Elem()
		}
		seen := make(map[string]struct{})
		for d.More() {
			keyToken, err := d.Token()
			key, ok := keyToken.(string)
			if err != nil || !ok {
				return ErrProtocol
			}
			if _, duplicate := seen[key]; duplicate {
				return ErrProtocol
			}
			seen[key] = struct{}{}
			valueType := element
			if fields != nil {
				var known bool
				valueType, known = fields[key]
				if !known {
					return ErrProtocol
				}
			}
			if scanExactJSONValue(d, valueType) != nil {
				return ErrProtocol
			}
		}
		end, err := d.Token()
		if err != nil || end != json.Delim('}') {
			return ErrProtocol
		}
	case '[':
		var element reflect.Type
		if t != nil && (t.Kind() == reflect.Slice || t.Kind() == reflect.Array) {
			element = t.Elem()
		}
		for d.More() {
			if scanExactJSONValue(d, element) != nil {
				return ErrProtocol
			}
		}
		end, err := d.Token()
		if err != nil || end != json.Delim(']') {
			return ErrProtocol
		}
	default:
		return ErrProtocol
	}
	return nil
}

func DecodeGenerationRequest(raw json.RawMessage) (domain.GenerationRequest, error) {
	var g domain.GenerationRequest
	if len(raw) > MaxStdinBytes {
		return g, ErrInputLimit
	}
	if strictJSON(raw, &g) != nil || !validHandle(string(g.ID)) || !validHandle(string(g.ShotID)) || !validHandle(g.ProviderID) || (g.Quality != domain.QualityDraft && g.Quality != domain.QualityFinal) {
		return domain.GenerationRequest{}, ErrInvalidRequest
	}
	return g, nil
}

func DecodeGenerationResult(raw json.RawMessage, requestID domain.ID) (domain.GenerationResult, error) {
	var g domain.GenerationResult
	if len(raw) > MaxStdoutBytes || strictJSON(raw, &g) != nil || !validHandle(string(g.ID)) || g.RequestID != requestID || g.DurationMs < 0 {
		return domain.GenerationResult{}, ErrProtocol
	}
	return g, nil
}

func encodeRequest(r Request) ([]byte, error) {
	if len(r.Payload) > MaxStdinBytes {
		return nil, ErrInputLimit
	}
	if !validHeader(r.Header) {
		return nil, ErrInvalidRequest
	}
	// Only the bounded header is marshalled. Raw payload is copied verbatim;
	// preflight the actual wire length BEFORE allocating the envelope or decoding.
	h, _ := json.Marshal(r.Header)
	const prefix = `{"header":`
	const middle = `,"payload":`
	n := len(prefix) + len(h) + len(middle) + len(r.Payload) + 1
	if n > MaxStdinBytes {
		return nil, ErrInputLimit
	}
	g, err := DecodeGenerationRequest(r.Payload)
	if err != nil || g.ID != r.Header.RequestID || g.ProviderID != r.Header.ProviderID {
		return nil, ErrInvalidRequest
	}
	out := make([]byte, 0, n)
	out = append(out, prefix...)
	out = append(out, h...)
	out = append(out, middle...)
	out = append(out, r.Payload...)
	out = append(out, '}')
	return out, nil
}

func decodeReply(raw []byte, expected Header) (*Reply, error) {
	var r Reply
	if len(raw) > MaxStdoutBytes || strictJSON(raw, &r) != nil || r.Header != expected {
		return nil, ErrProtocol
	}
	switch r.Status {
	case "ok":
		if r.ErrorCode != "" {
			return nil, ErrProtocol
		}
		if _, err := DecodeGenerationResult(r.Payload, expected.RequestID); err != nil {
			return nil, err
		}
		return &r, nil
	case "error":
		// Payloads accompanying errors are deliberately ignored, never decoded or
		// returned. Only this fixed vocabulary is admitted; no worker prose leaks.
		switch r.ErrorCode {
		case "failed", "unavailable", "invalid_request":
			return nil, ErrWorker
		}
		return nil, ErrProtocol
	default:
		return nil, ErrProtocol
	}
}
