package main

import (
	"encoding/json"
	"testing"
)

// The stable error-code set is a cross-language contract. If this list changes,
// ERROR_CODES in apps/desktop/src/lib/protocol.ts must change in lockstep.
func TestErrorCodesContract(t *testing.T) {
	want := []string{
		"invalid_request",
		"protocol_mismatch",
		"not_activated",
		"server_not_found",
		"app_not_found",
		"ssh_unknown_host",
		"ssh_auth_failed",
		"ssh_unreachable",
		"remote_state_invalid",
		"operation_timeout",
		"operation_cancelled",
		"action_not_allowed",
		"internal_error",
	}
	if len(ErrorCodes) != len(want) {
		t.Fatalf("ErrorCodes length = %d, want %d", len(ErrorCodes), len(want))
	}
	for i, code := range ErrorCodes {
		if string(code) != want[i] {
			t.Errorf("ErrorCodes[%d] = %q, want %q", i, code, want[i])
		}
	}
}

func TestRequestDecode(t *testing.T) {
	line := []byte(`{"version":1,"id":"req-123","method":"server.snapshot","params":{"server":"production"}}`)
	var req Request
	if err := json.Unmarshal(line, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if req.Version != 1 || req.ID != "req-123" || req.Method != "server.snapshot" {
		t.Fatalf("decoded wrong: %+v", req)
	}
	var params struct {
		Server string `json:"server"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		t.Fatalf("params unmarshal: %v", err)
	}
	if params.Server != "production" {
		t.Errorf("params.Server = %q, want production", params.Server)
	}
}

func TestSuccessResponseEncoding(t *testing.T) {
	b, err := json.Marshal(Response{Version: 1, ID: "req-1", Result: map[string]interface{}{"reachable": true}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(b)
	want := `{"version":1,"id":"req-1","result":{"reachable":true}}`
	if got != want {
		t.Errorf("success response = %s, want %s", got, want)
	}
	// A success response must not carry an error field.
	if containsKey(t, b, "error") {
		t.Errorf("success response unexpectedly contains error field: %s", got)
	}
}

func TestErrorResponseEncoding(t *testing.T) {
	resp := Response{Version: 1, ID: "req-1", Error: newError(ErrSSHUnreachable, "nope", true, nil)}
	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Details must always serialize as an object, never null, and result must be
	// absent.
	if containsKey(t, b, "result") {
		t.Errorf("error response unexpectedly contains result field: %s", b)
	}
	var decoded struct {
		Version int `json:"version"`
		ID      string
		Error   struct {
			Code      string
			Message   string
			Retryable bool
			Details   map[string]interface{}
		}
	}
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded.Error.Code != "ssh_unreachable" || !decoded.Error.Retryable {
		t.Errorf("error decoded wrong: %+v", decoded.Error)
	}
	if decoded.Error.Details == nil {
		t.Errorf("details must be a non-nil object")
	}
}

func TestEventEncoding(t *testing.T) {
	b, err := json.Marshal(Event{Version: 1, Event: "logs.line", Subscription: "log-45", Data: map[string]string{"line": "hi"}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(b)
	want := `{"version":1,"event":"logs.line","subscription":"log-45","data":{"line":"hi"}}`
	if got != want {
		t.Errorf("event = %s, want %s", got, want)
	}
}

func TestSalvageID(t *testing.T) {
	// Valid JSON with an id still readable.
	if got := salvageID([]byte(`{"id":"req-9","method":`)); got != "" {
		// This is malformed overall; salvage returns "" because unmarshal fails.
		t.Logf("salvage of partially-malformed returned %q", got)
	}
	if got := salvageID([]byte(`{"id":"req-9"}`)); got != "req-9" {
		t.Errorf("salvageID = %q, want req-9", got)
	}
	if got := salvageID([]byte(`not json`)); got != "" {
		t.Errorf("salvageID(garbage) = %q, want empty", got)
	}
}

// containsKey reports whether the top-level JSON object encoded in b has key.
func containsKey(t *testing.T, b []byte, key string) bool {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("containsKey unmarshal: %v", err)
	}
	_, ok := m[key]
	return ok
}
