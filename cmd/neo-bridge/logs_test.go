package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/vxero/neo/internal/config"
	"github.com/vxero/neo/internal/operations"
)

// streamStubExecutor is a bridge-side fake whose Stream emits scripted lines and
// then, in follow mode, blocks until the context is cancelled — mirroring a live
// `docker logs -f`. It tracks how many streams are live so a test can prove a
// cancelled subscription frees its SSH session.
type streamStubExecutor struct {
	user  string
	state string
	lines []string
	live  atomic.Int32
}

func (e *streamStubExecutor) Run(context.Context, string) (string, error) { return "", nil }

func (e *streamStubExecutor) Stream(ctx context.Context, cmd string, w io.Writer) error {
	e.live.Add(1)
	defer e.live.Add(-1)
	for _, ln := range e.lines {
		_, _ = io.WriteString(w, ln+"\n")
	}
	if strings.Contains(cmd, " -f ") || strings.HasSuffix(cmd, " -f") {
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}

func (e *streamStubExecutor) ReadFileElevated(context.Context, string) ([]byte, error) {
	return []byte(e.state), nil
}
func (e *streamStubExecutor) User() string { return e.user }
func (e *streamStubExecutor) Close() error { return nil }

const logsTestState = `{"apps":{"ghost":{"status":"running","image":"ghost:5"}},"services":{}}`

func logServer(t *testing.T, exec operations.Executor) *Server {
	t.Helper()
	cfg := &config.Config{
		Current: "production",
		Servers: map[string]config.Server{"production": {Name: "production", Host: "root@10.0.0.1", Port: 22}},
	}
	ops := operations.NewService(stubConfigStore{cfg: cfg}, stubConnector{exec: exec}, operations.SystemClock(), operations.Options{})
	return NewServer("1", "1", slog.New(slog.NewTextHandler(io.Discard, nil)), WithOperations(ops))
}

// wireMessage decodes either a response or a streaming event off the protocol
// stream, so a test can assert on both in the order they were written.
type wireMessage struct {
	ID           string          `json:"id"`
	Event        string          `json:"event"`
	Subscription string          `json:"subscription"`
	Result       json.RawMessage `json:"result"`
	Error        *RPCError       `json:"error"`
	Data         json.RawMessage `json:"data"`
}

// driveMessages runs the server to completion on the given input and returns
// every protocol line written. Because Run's deferred closeAll waits for each
// stream goroutine to unwind, all log events are present by the time it returns.
func driveMessages(t *testing.T, srv *Server, input string) []wireMessage {
	t.Helper()
	var out bytes.Buffer
	if err := srv.Run(context.Background(), strings.NewReader(input), &out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var msgs []wireMessage
	sc := bufio.NewScanner(bytes.NewReader(out.Bytes()))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var m wireMessage
		if err := json.Unmarshal(line, &m); err != nil {
			t.Fatalf("decode line %q: %v", line, err)
		}
		msgs = append(msgs, m)
	}
	return msgs
}

func TestLogsSubscribeStreamsBatchedLinesThenCloses(t *testing.T) {
	exec := &streamStubExecutor{user: "root", state: logsTestState, lines: []string{"one", "two", "three"}}
	srv := logServer(t, exec)

	// A recent (non-follow) subscription: emit the backlog and end on its own.
	msgs := driveMessages(t, srv,
		`{"version":1,"id":"r1","method":"logs.subscribe","params":{"server":"production","target":"ghost","tail":10}}`+"\n")

	// The subscribe response comes first, carrying the id and clamped tail.
	if len(msgs) == 0 || msgs[0].ID != "r1" || msgs[0].Error != nil {
		t.Fatalf("unexpected subscribe response: %+v", msgs)
	}
	var res logsSubscribeResult
	remarshal(t, msgs[0].Result, &res)
	if res.Subscription == "" || res.Tail != 10 {
		t.Fatalf("subscribe result wrong: %+v", res)
	}

	// Collect the batched lines and the terminal event for this subscription.
	var lines []string
	var closedReason string
	for _, m := range msgs[1:] {
		if m.Subscription != res.Subscription {
			continue
		}
		switch m.Event {
		case "logs.line":
			var d logLineData
			remarshal(t, m.Data, &d)
			lines = append(lines, d.Lines...)
		case "logs.closed":
			var d logClosedData
			remarshal(t, m.Data, &d)
			closedReason = d.Reason
		}
	}
	if strings.Join(lines, ",") != "one,two,three" {
		t.Errorf("streamed lines = %v", lines)
	}
	if closedReason != "eof" {
		t.Errorf("closed reason = %q, want eof", closedReason)
	}
	if exec.live.Load() != 0 {
		t.Errorf("stream leaked: %d still live", exec.live.Load())
	}
}

func TestLogsUnsubscribeCancelsFollowStream(t *testing.T) {
	exec := &streamStubExecutor{user: "root", state: logsTestState, lines: []string{"tail-line"}}
	srv := logServer(t, exec)

	// The subscription id is deterministic (log-1); a follow stream stays open
	// until the explicit unsubscribe cancels it.
	input := `{"version":1,"id":"s1","method":"logs.subscribe","params":{"server":"production","target":"ghost","follow":true}}` + "\n" +
		`{"version":1,"id":"u1","method":"logs.unsubscribe","params":{"subscription":"log-1"}}` + "\n"
	msgs := driveMessages(t, srv, input)

	var unsub logsUnsubscribeResult
	var closedReason string
	for _, m := range msgs {
		if m.ID == "u1" {
			remarshal(t, m.Result, &unsub)
		}
		if m.Event == "logs.closed" && m.Subscription == "log-1" {
			var d logClosedData
			remarshal(t, m.Data, &d)
			closedReason = d.Reason
		}
	}
	if !unsub.OK || !unsub.Found {
		t.Errorf("unsubscribe result = %+v, want ok+found", unsub)
	}
	if closedReason != "cancelled" {
		t.Errorf("closed reason = %q, want cancelled", closedReason)
	}
	if exec.live.Load() != 0 {
		t.Errorf("follow stream leaked after unsubscribe: %d live", exec.live.Load())
	}
}

func TestLogsSubscribeEnforcesMaxSubscriptions(t *testing.T) {
	exec := &streamStubExecutor{user: "root", state: logsTestState}
	srv := logServer(t, exec)

	var b strings.Builder
	for i := 1; i <= MaxSubscriptions+1; i++ {
		b.WriteString(`{"version":1,"id":"m`)
		b.WriteString(itoa(i))
		b.WriteString(`","method":"logs.subscribe","params":{"server":"production","target":"ghost","follow":true}}`)
		b.WriteString("\n")
	}
	msgs := driveMessages(t, srv, b.String())

	// Exactly one subscribe must be rejected — the (MaxSubscriptions+1)th.
	var rejected int
	for _, m := range msgs {
		if strings.HasPrefix(m.ID, "m") && m.Error != nil {
			if m.Error.Code != ErrInvalidRequest {
				t.Errorf("rejection code = %s, want invalid_request", m.Error.Code)
			}
			if m.Error.Details["limit"] == nil {
				t.Errorf("rejection should report the limit: %+v", m.Error.Details)
			}
			rejected++
		}
	}
	if rejected != 1 {
		t.Fatalf("want exactly 1 rejected subscribe, got %d", rejected)
	}
}

func TestLogsUnsubscribeUnknownIsIdempotent(t *testing.T) {
	srv := logServer(t, &streamStubExecutor{user: "root", state: logsTestState})
	msgs := driveMessages(t, srv,
		`{"version":1,"id":"u1","method":"logs.unsubscribe","params":{"subscription":"nope"}}`+"\n")
	if len(msgs) != 1 || msgs[0].Error != nil {
		t.Fatalf("unknown unsubscribe should succeed: %+v", msgs)
	}
	var res logsUnsubscribeResult
	remarshal(t, msgs[0].Result, &res)
	if !res.OK || res.Found {
		t.Errorf("unknown unsubscribe = %+v, want ok and not found", res)
	}
}

func TestLogsSubscribeMissingTarget(t *testing.T) {
	srv := logServer(t, &streamStubExecutor{user: "root", state: logsTestState})
	msgs := driveMessages(t, srv,
		`{"version":1,"id":"t1","method":"logs.subscribe","params":{"server":"production"}}`+"\n")
	if len(msgs) != 1 || msgs[0].Error == nil || msgs[0].Error.Code != ErrInvalidRequest {
		t.Fatalf("want invalid_request for missing target, got %+v", msgs)
	}
}

func TestLogsSubscribeUnknownTargetIsAppNotFound(t *testing.T) {
	srv := logServer(t, &streamStubExecutor{user: "root", state: logsTestState})
	msgs := driveMessages(t, srv,
		`{"version":1,"id":"t2","method":"logs.subscribe","params":{"server":"production","target":"ghost/worker/nope"}}`+"\n")

	// The subscribe request itself is accepted; the failure surfaces on the
	// stream as a terminal logs.closed(error) event with the stable code.
	var res logsSubscribeResult
	var code string
	for _, m := range msgs {
		if m.ID == "t2" && m.Error == nil {
			remarshal(t, m.Result, &res)
		}
		if m.Event == "logs.closed" {
			var d logClosedData
			remarshal(t, m.Data, &d)
			code = d.Code
		}
	}
	if res.Subscription == "" {
		t.Fatalf("subscribe should be accepted, got %+v", msgs)
	}
	if code != string(ErrAppNotFound) {
		t.Errorf("closed code = %q, want app_not_found", code)
	}
}

func TestLogsSubscribeUnconfigured(t *testing.T) {
	srv := NewServer("1", "1", slog.New(slog.NewTextHandler(io.Discard, nil)))
	msgs := driveMessages(t, srv,
		`{"version":1,"id":"n1","method":"logs.subscribe","params":{"server":"production","target":"ghost"}}`+"\n")
	if len(msgs) != 1 || msgs[0].Error == nil || msgs[0].Error.Code != ErrInternal {
		t.Fatalf("want internal_error when unconfigured, got %+v", msgs)
	}
}

// itoa is a tiny local int→string to avoid pulling strconv into the test file's
// import set just for building request ids.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
