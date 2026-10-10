package worker

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/manish96170/videofactory/domain"
)

func testRequest() Request {
	return Request{Header: Header{Version: 1, Operation: "generate", RequestID: "request-1", ProviderID: "provider-1", ModelID: "model-1", ModelRevision: "v1", ProfileID: "profile-1", ProfileRevision: "v2"},
		Payload: json.RawMessage(`{"id":"request-1","shotId":"shot-1","providerId":"provider-1","quality":"draft","params":{"prompt":"not execution config"}}`)}
}

func testReply() Reply {
	return Reply{Header: testRequest().Header, Status: "ok", Payload: json.RawMessage(`{"id":"result-1","requestId":"request-1","peakMemoryMb":1e100}`)}
}

func TestRequestWireLimitPreflight(t *testing.T) {
	r := testRequest()
	base, err := encodeRequest(r)
	if err != nil {
		t.Fatal(err)
	}
	r.Payload = append(r.Payload, bytes.Repeat([]byte(" "), MaxStdinBytes-len(base))...)
	wire, err := encodeRequest(r)
	if err != nil || len(wire) != MaxStdinBytes {
		t.Fatalf("exact bound: %d %v", len(wire), err)
	}
	r.Payload = append(r.Payload, ' ')
	if _, err = encodeRequest(r); !errors.Is(err, ErrInputLimit) {
		t.Fatalf("envelope overhead not enforced: %v", err)
	}
	r.Payload = make([]byte, MaxStdinBytes+1)
	if _, err = encodeRequest(r); !errors.Is(err, ErrInputLimit) {
		t.Fatal(err)
	}
	// Unsafe header input is rejected before it can cause large Marshal work.
	r = testRequest()
	r.Header.ModelID = strings.Repeat("x", MaxStdinBytes)
	if _, err = encodeRequest(r); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal(err)
	}
}

func TestTypedGenerationCodecsAndSchema(t *testing.T) {
	r := testRequest()
	g, err := DecodeGenerationRequest(r.Payload)
	if err != nil || g.ID != domain.ID("request-1") {
		t.Fatal(g, err)
	}
	for _, raw := range []string{`null`, `{}`, string(r.Payload) + ` {}`, `{"id":"request-1","shotId":"shot-1","providerId":"provider-1","quality":"draft","unexpected":1}`} {
		if _, err = DecodeGenerationRequest(json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	for _, raw := range []string{`null`, `{}`, `{"id":"r","requestId":"wrong"}`, `{"id":"r","requestId":"request-1","unexpected":1}`} {
		if _, err = DecodeGenerationResult(json.RawMessage(raw), "request-1"); !errors.Is(err, ErrProtocol) {
			t.Fatalf("accepted %s: %v", raw, err)
		}
	}
}

func TestReplySchemaAndEveryCorrelation(t *testing.T) {
	ok := testReply()
	wire, _ := json.Marshal(ok)
	if _, err := decodeReply(wire, testRequest().Header); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*Header){
		func(h *Header) { h.Version++ }, func(h *Header) { h.Operation = "load" }, func(h *Header) { h.RequestID = "wrong" },
		func(h *Header) { h.ProviderID = "wrong" }, func(h *Header) { h.ModelID = "wrong" }, func(h *Header) { h.ModelRevision = "wrong" },
		func(h *Header) { h.ProfileID = "wrong" }, func(h *Header) { h.ProfileRevision = "wrong" },
	} {
		bad := ok
		mutate(&bad.Header)
		wire, _ = json.Marshal(bad)
		if _, err := decodeReply(wire, testRequest().Header); !errors.Is(err, ErrProtocol) {
			t.Fatal(err)
		}
	}
	for _, wire := range []string{`{`, `{}`, `null`, string(mustJSON(t, ok)) + ` {}`, strings.Replace(string(mustJSON(t, ok)), `"status":"ok"`, `"status":"unknown"`, 1), strings.Replace(string(mustJSON(t, ok)), `"status":"ok"`, `"unknown":1,"status":"ok"`, 1)} {
		if _, err := decodeReply([]byte(wire), testRequest().Header); !errors.Is(err, ErrProtocol) {
			t.Fatal(err)
		}
	}
	bad := ok
	bad.Status = "error"
	bad.ErrorCode = "failed"
	bad.Payload = json.RawMessage(`{"secret":"do not return", "unrelated":[1,2]}`)
	if reply, err := decodeReply(mustJSON(t, bad), testRequest().Header); reply != nil || !errors.Is(err, ErrWorker) {
		t.Fatal(reply, err)
	}
	bad.ErrorCode = "credential-secret"
	if reply, err := decodeReply(mustJSON(t, bad), testRequest().Header); reply != nil || !errors.Is(err, ErrProtocol) || strings.Contains(err.Error(), "credential") {
		t.Fatal(reply, err)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestOSPeakSemanticUnitsAndOverflow(t *testing.T) {
	for _, c := range []struct {
		raw  int64
		kib  bool
		want int64
		ok   bool
	}{{42, false, 42, true}, {42, true, 42 * 1024, true}, {0, true, 0, true}, {-1, false, 0, false}, {math.MaxInt64, true, 0, false}} {
		v, ok := normalizePeak(c.raw, c.kib)
		if v != c.want || ok != c.ok {
			t.Fatalf("%+v => %d %v", c, v, ok)
		}
	}
}

func TestCaptureCapacityBound(t *testing.T) {
	var b []byte
	for n := 0; n < MaxStdoutBytes; n += 4096 {
		b = appendCapture(b, make([]byte, 4096), MaxStdoutBytes)
		if cap(b) > MaxStdoutBytes {
			t.Fatal(cap(b))
		}
	}
}

func TestExactJSONStructKeysAndDuplicateFields(t *testing.T) {
	for _, replacement := range []string{
		`"ID":"request-1"`,
		`"id":"wrong","ID":"request-1"`,
		`"id":"wrong","id":"request-1"`,
		`"id":"wrong","\u0069d":"request-1"`,
	} {
		t.Run("request/"+replacement, func(t *testing.T) {
			r := testRequest()
			r.Payload = json.RawMessage(strings.Replace(string(r.Payload), `"id":"request-1"`, replacement, 1))
			if _, err := encodeRequest(r); !errors.Is(err, ErrInvalidRequest) {
				t.Fatal("ambiguous request accepted", err)
			}
		})
	}
	for _, replacement := range []string{
		`"RequestID":"request-1"`,
		`"requestId":"wrong","RequestID":"request-1"`,
		`"requestId":"wrong","requestId":"request-1"`,
	} {
		t.Run("result/"+replacement, func(t *testing.T) {
			r := testReply()
			r.Payload = json.RawMessage(strings.Replace(string(r.Payload), `"requestId":"request-1"`, replacement, 1))
			if reply, err := decodeReply(mustJSON(t, r), testRequest().Header); reply != nil || !errors.Is(err, ErrProtocol) {
				t.Fatal("ambiguous result accepted", err)
			}
		})
	}
	for _, replacement := range []string{`"Version":1`, `"version":2,"Version":1`, `"version":2,"version":1`} {
		t.Run("header/"+replacement, func(t *testing.T) {
			wire := strings.Replace(string(mustJSON(t, testReply())), `"version":1`, replacement, 1)
			if reply, err := decodeReply([]byte(wire), testRequest().Header); reply != nil || !errors.Is(err, ErrProtocol) {
				t.Fatal("ambiguous header accepted", err)
			}
		})
	}
	for _, replacement := range []string{`"Status":"ok"`, `"status":"error","Status":"ok"`, `"status":"error","status":"ok"`} {
		t.Run("envelope/"+replacement, func(t *testing.T) {
			wire := strings.Replace(string(mustJSON(t, testReply())), `"status":"ok"`, replacement, 1)
			if reply, err := decodeReply([]byte(wire), testRequest().Header); reply != nil || !errors.Is(err, ErrProtocol) {
				t.Fatal("ambiguous envelope accepted", err)
			}
		})
	}
	var nested struct {
		Asset domain.Asset `json:"asset"`
	}
	for _, raw := range []string{`{"Asset":{"id":"asset-1"}}`, `{"asset":{"id":"wrong","ID":"asset-1"}}`, `{"asset":{"id":"wrong","id":"asset-1"}}`, `{"asset":{"URI":"content:asset"}}`} {
		t.Run("nested/"+raw, func(t *testing.T) {
			if err := strictJSON([]byte(raw), &nested); !errors.Is(err, ErrProtocol) {
				t.Fatal("ambiguous nested struct accepted", err)
			}
		})
	}
}

func TestOpenExtensionKeysRemainOpaqueButRejectExactDuplicates(t *testing.T) {
	for _, extension := range []string{
		`{"id":1,"ID":2,"header":{"requestId":"one","RequestID":"two"},"items":[{"asset":{"URI":1,"uri":2}}]}`,
		`{"nested":{"credential-secret":1,"Credential-Secret":2}}`,
	} {
		r := testRequest()
		r.Payload = json.RawMessage(`{"id":"request-1","shotId":"shot-1","providerId":"provider-1","quality":"draft","params":` + extension + `}`)
		if _, err := encodeRequest(r); err != nil {
			t.Fatal("valid extension keys rejected", err)
		}
		reply := testReply()
		reply.Payload = json.RawMessage(`{"id":"result-1","requestId":"request-1","metadata":` + extension + `}`)
		if _, err := decodeReply(mustJSON(t, reply), r.Header); err != nil {
			t.Fatal("valid metadata rejected", err)
		}
	}
	for _, extension := range []string{`{"nested":{"key":1,"key":2}}`, `{"items":[{"key":1,"\u006bey":2}]}`} {
		r := testRequest()
		r.Payload = json.RawMessage(`{"id":"request-1","shotId":"shot-1","providerId":"provider-1","quality":"draft","params":` + extension + `}`)
		if _, err := encodeRequest(r); !errors.Is(err, ErrInvalidRequest) {
			t.Fatal("duplicate extension key accepted", err)
		}
		reply := testReply()
		reply.Payload = json.RawMessage(`{"id":"result-1","requestId":"request-1","metadata":` + extension + `}`)
		if _, err := decodeReply(mustJSON(t, reply), r.Header); !errors.Is(err, ErrProtocol) {
			t.Fatal("duplicate metadata key accepted", err)
		}
	}
	// Error payloads remain untyped, ignored, and never returned.
	reply := testReply()
	reply.Status = "error"
	reply.ErrorCode = "failed"
	reply.Payload = json.RawMessage(`{"ID":"not-a-result","id":"different","extra":{"URI":true}}`)
	if result, err := decodeReply(mustJSON(t, reply), testRequest().Header); result != nil || !errors.Is(err, ErrWorker) {
		t.Fatal(result, err)
	}
	reply.Payload = json.RawMessage(`{"requestId":"ignored","requestId":"also-ignored"}`)
	if result, err := decodeReply(mustJSON(t, reply), testRequest().Header); result != nil || !errors.Is(err, ErrWorker) {
		t.Fatal("error payload was interpreted", result, err)
	}
}

func TestExactJSONScanRejectsOversizeDepthAndInvalidUTF8(t *testing.T) {
	var dst domain.GenerationRequest
	for _, raw := range [][]byte{
		make([]byte, MaxStdinBytes+1),
		[]byte(strings.Repeat("[", 10001) + "0" + strings.Repeat("]", 10001)),
		[]byte("{\"params\":{\"text\":\"\xff\"}}"),
	} {
		if err := strictJSON(raw, &dst); !errors.Is(err, ErrProtocol) {
			t.Fatal("invalid bounded scan accepted", err)
		}
	}
}
