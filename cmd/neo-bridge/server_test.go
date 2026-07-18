package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// run drives the server with the given input lines and returns the decoded
// responses written to stdout, in order.
func run(t *testing.T, input string) []Response {
	t.Helper()
	var out bytes.Buffer
	srv := NewServer("1.2.3", "4.5.6", nil)
	if err := srv.Run(context.Background(), strings.NewReader(input), &out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	return decodeResponses(t, out.Bytes())
}

func decodeResponses(t *testing.T, b []byte) []Response {
	t.Helper()
	var responses []Response
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var r Response
		if err := json.Unmarshal(line, &r); err != nil {
			t.Fatalf("decode response %q: %v", line, err)
		}
		responses = append(responses, r)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	return responses
}

func TestHello(t *testing.T) {
	resp := run(t, `{"version":1,"id":"h1","method":"bridge.hello"}`+"\n")
	if len(resp) != 1 {
		t.Fatalf("want 1 response, got %d", len(resp))
	}
	if resp[0].Version != 1 || resp[0].ID != "h1" || resp[0].Error != nil {
		t.Fatalf("unexpected response: %+v", resp[0])
	}
	// Result is decoded as a generic map; re-marshal into HelloResult to assert.
	var hello HelloResult
	remarshal(t, resp[0].Result, &hello)
	if hello.ProtocolVersion != 1 {
		t.Errorf("protocolVersion = %d, want 1", hello.ProtocolVersion)
	}
	if hello.BridgeVersion != "1.2.3" || hello.CoreVersion != "4.5.6" {
		t.Errorf("versions = %q/%q, want 1.2.3/4.5.6", hello.BridgeVersion, hello.CoreVersion)
	}
	if hello.Platform == "" || hello.Arch == "" {
		t.Errorf("platform/arch must be populated: %+v", hello)
	}
	if hello.Activation == "" {
		t.Errorf("activation must be populated")
	}
}

func TestShutdownStopsTheLoop(t *testing.T) {
	// A request AFTER shutdown must not be processed — the loop stops.
	input := `{"version":1,"id":"s1","method":"bridge.shutdown"}` + "\n" +
		`{"version":1,"id":"h2","method":"bridge.hello"}` + "\n"
	resp := run(t, input)
	if len(resp) != 1 {
		t.Fatalf("want exactly 1 response (shutdown), got %d: %+v", len(resp), resp)
	}
	if resp[0].ID != "s1" || resp[0].Error != nil {
		t.Fatalf("unexpected shutdown response: %+v", resp[0])
	}
	var sr ShutdownResult
	remarshal(t, resp[0].Result, &sr)
	if !sr.OK {
		t.Errorf("shutdown result ok = false")
	}
}

func TestStdinCloseIsGraceful(t *testing.T) {
	// No trailing newline, then EOF: the request is still processed and Run
	// returns nil.
	resp := run(t, `{"version":1,"id":"h1","method":"bridge.hello"}`)
	if len(resp) != 1 || resp[0].ID != "h1" || resp[0].Error != nil {
		t.Fatalf("want one successful hello, got %+v", resp)
	}
}

func TestMalformedRequest(t *testing.T) {
	resp := run(t, "this is not json\n")
	if len(resp) != 1 {
		t.Fatalf("want 1 response, got %d", len(resp))
	}
	if resp[0].Error == nil || resp[0].Error.Code != ErrInvalidRequest {
		t.Fatalf("want invalid_request error, got %+v", resp[0])
	}
	if resp[0].Error.Details == nil {
		t.Errorf("error details must be a non-nil object")
	}
}

func TestVersionMismatch(t *testing.T) {
	resp := run(t, `{"version":2,"id":"v1","method":"bridge.hello"}`+"\n")
	if len(resp) != 1 {
		t.Fatalf("want 1 response, got %d", len(resp))
	}
	if resp[0].Error == nil || resp[0].Error.Code != ErrProtocolMismatch {
		t.Fatalf("want protocol_mismatch, got %+v", resp[0])
	}
	if resp[0].ID != "v1" {
		t.Errorf("mismatch response id = %q, want v1", resp[0].ID)
	}
	// The mismatch details expose expected/received so the UI can explain it.
	if got := resp[0].Error.Details["received"]; got == nil {
		t.Errorf("details.received missing: %+v", resp[0].Error.Details)
	}
}

func TestDuplicateID(t *testing.T) {
	input := `{"version":1,"id":"dup","method":"bridge.hello"}` + "\n" +
		`{"version":1,"id":"dup","method":"bridge.hello"}` + "\n"
	resp := run(t, input)
	if len(resp) != 2 {
		t.Fatalf("want 2 responses, got %d", len(resp))
	}
	if resp[0].Error != nil {
		t.Fatalf("first response should succeed, got %+v", resp[0])
	}
	if resp[1].Error == nil || resp[1].Error.Code != ErrInvalidRequest {
		t.Fatalf("second (duplicate) should be invalid_request, got %+v", resp[1])
	}
}

func TestMissingMethod(t *testing.T) {
	resp := run(t, `{"version":1,"id":"m1"}`+"\n")
	if len(resp) != 1 || resp[0].Error == nil || resp[0].Error.Code != ErrInvalidRequest {
		t.Fatalf("want invalid_request for missing method, got %+v", resp)
	}
}

func TestMissingID(t *testing.T) {
	resp := run(t, `{"version":1,"method":"bridge.hello"}`+"\n")
	if len(resp) != 1 || resp[0].Error == nil || resp[0].Error.Code != ErrInvalidRequest {
		t.Fatalf("want invalid_request for missing id, got %+v", resp)
	}
}

func TestUnknownMethod(t *testing.T) {
	resp := run(t, `{"version":1,"id":"u1","method":"does.not.exist"}`+"\n")
	if len(resp) != 1 || resp[0].Error == nil || resp[0].Error.Code != ErrInvalidRequest {
		t.Fatalf("want invalid_request for unknown method, got %+v", resp)
	}
}

func TestBlankLinesIgnored(t *testing.T) {
	input := "\n   \n" + `{"version":1,"id":"h1","method":"bridge.hello"}` + "\n\n"
	resp := run(t, input)
	if len(resp) != 1 || resp[0].ID != "h1" || resp[0].Error != nil {
		t.Fatalf("blank lines should be skipped, got %+v", resp)
	}
}

func TestContextCancellationStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled
	var out bytes.Buffer
	srv := NewServer("1", "1", nil)
	// A never-ending reader; Run must return promptly because ctx is cancelled.
	if err := srv.Run(ctx, strings.NewReader(""), &out); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// remarshal round-trips a decoded generic value into a typed struct.
func remarshal(t *testing.T, v interface{}, out interface{}) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("remarshal marshal: %v", err)
	}
	if err := json.Unmarshal(b, out); err != nil {
		t.Fatalf("remarshal unmarshal: %v", err)
	}
}
