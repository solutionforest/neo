package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/vxero/neo/internal/operations"
)

// Log streaming methods: logs.subscribe / logs.unsubscribe, with streaming
// logs.line events and a terminal logs.closed event.
//
// The bridge owns cancellation and backpressure (plan "Log streaming"):
//   - Each subscription runs on its own goroutine with a context derived from
//     the bridge's run context, so unsubscribe and shutdown both stop the SSH
//     stream and free its session.
//   - Lines are batched before being written to the webview so a high-volume
//     container cannot flood the protocol stream with one event per line.
//   - Each desktop process is limited to MaxSubscriptions simultaneous streams.

const (
	// MaxSubscriptions caps simultaneous log streams per desktop process
	// (plan: "Limit each desktop process to five simultaneous log
	// subscriptions").
	MaxSubscriptions = 5
	// logBatchInterval is how often buffered lines are flushed to the webview
	// when the batch has not already filled. Small enough to feel live, large
	// enough to coalesce a burst into one event.
	logBatchInterval = 100 * time.Millisecond
	// logBatchMaxLines forces a flush once this many lines are buffered, so a
	// firehose is bounded by size as well as time.
	logBatchMaxLines = 200
)

// logLineData is the payload of a logs.line event. Batching means it carries a
// slice of lines rather than a single line — the frontend appends them all.
type logLineData struct {
	Lines []string `json:"lines"`
}

// logClosedData is the payload of the terminal logs.closed event. Reason is
// "eof" (the stream ended on its own), "cancelled" (unsubscribe/shutdown), or
// "error"; Code/Message carry the stable error code on the error path so the UI
// can branch without parsing prose.
type logClosedData struct {
	Reason  string `json:"reason"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

// subscription is one live log stream. Cancelling ctx stops it; done closes once
// the stream goroutine has fully unwound (its SSH session freed).
type subscription struct {
	id     string
	cancel context.CancelFunc
	done   chan struct{}
}

// subManager tracks live log subscriptions and writes their events to the shared
// protocol output. It is created with the Server and bound to the output stream
// at the start of Run.
type subManager struct {
	log *slog.Logger
	ops *operations.Service

	mu      sync.Mutex
	writer  *syncWriter
	subs    map[string]*subscription
	counter uint64

	// batching knobs (overridable in tests).
	batchInterval time.Duration
	batchMax      int
}

func newSubManager(logger *slog.Logger, ops *operations.Service) *subManager {
	return &subManager{
		log:           logger,
		ops:           ops,
		subs:          map[string]*subscription{},
		batchInterval: logBatchInterval,
		batchMax:      logBatchMaxLines,
	}
}

// bind attaches the output stream. Called once at the start of Run before any
// request is dispatched.
func (m *subManager) bind(w *syncWriter) {
	m.mu.Lock()
	m.writer = w
	m.mu.Unlock()
}

// start registers a new subscription and launches its stream goroutine. parent
// is the bridge's run context, so a shutdown cancels every stream. It returns
// the assigned id and the clamped tail, or a protocol error if the per-process
// limit is reached.
func (m *subManager) start(parent context.Context, opts operations.LogOptions) (id string, tail int, rerr *RPCError) {
	m.mu.Lock()
	if len(m.subs) >= MaxSubscriptions {
		m.mu.Unlock()
		return "", 0, newError(ErrInvalidRequest,
			fmt.Sprintf("too many log subscriptions (max %d)", MaxSubscriptions), false,
			map[string]interface{}{"limit": MaxSubscriptions})
	}
	m.counter++
	id = fmt.Sprintf("log-%d", m.counter)
	ctx, cancel := context.WithCancel(parent)
	sub := &subscription{id: id, cancel: cancel, done: make(chan struct{})}
	m.subs[id] = sub
	m.mu.Unlock()

	tail = operations.ClampTail(opts.Tail)
	opts.Tail = tail
	go m.run(ctx, sub, opts)
	return id, tail, nil
}

// run drives one stream: it batches emitted lines and, when the stream ends,
// flushes the remainder and writes the terminal logs.closed event. It always
// deregisters the subscription and signals done so unsubscribe/shutdown can wait
// for a clean teardown.
func (m *subManager) run(ctx context.Context, sub *subscription, opts operations.LogOptions) {
	defer close(sub.done)
	defer m.remove(sub.id)

	batch := &lineBatch{max: m.batchMax, flush: func(lines []string) { m.emitLines(sub.id, lines) }}

	// A ticker flushes partial batches so a trickle of lines is not held back
	// waiting for the batch to fill.
	ticker := time.NewTicker(m.batchInterval)
	defer ticker.Stop()
	stop := make(chan struct{})
	var tick sync.WaitGroup
	tick.Add(1)
	go func() {
		defer tick.Done()
		for {
			select {
			case <-ticker.C:
				batch.flushNow()
			case <-stop:
				return
			}
		}
	}()

	streamErr := m.ops.StreamLogs(ctx, opts, batch.add)

	close(stop)
	tick.Wait()
	batch.flushNow() // deliver anything buffered when the stream ended
	m.emitClosed(sub.id, streamErr)
}

// stop cancels a subscription by id. It returns false if no such subscription is
// live. It does not block on teardown — the stream goroutine unwinds on its own
// and emits logs.closed when done.
func (m *subManager) stop(id string) bool {
	m.mu.Lock()
	sub, ok := m.subs[id]
	m.mu.Unlock()
	if !ok {
		return false
	}
	sub.cancel()
	return true
}

// closeAll cancels every live subscription and waits for each to unwind. Called
// as Run returns so no SSH session outlives the bridge.
func (m *subManager) closeAll() {
	m.mu.Lock()
	subs := make([]*subscription, 0, len(m.subs))
	for _, s := range m.subs {
		subs = append(subs, s)
	}
	m.mu.Unlock()

	for _, s := range subs {
		s.cancel()
	}
	for _, s := range subs {
		<-s.done
	}
}

func (m *subManager) remove(id string) {
	m.mu.Lock()
	delete(m.subs, id)
	m.mu.Unlock()
}

func (m *subManager) emitLines(id string, lines []string) {
	if len(lines) == 0 {
		return
	}
	m.write(Event{Version: ProtocolVersion, Event: "logs.line", Subscription: id, Data: logLineData{Lines: lines}})
}

func (m *subManager) emitClosed(id string, err error) {
	data := logClosedData{Reason: "eof"}
	if err != nil {
		rerr := rpcFromOpError(err)
		switch rerr.Code {
		case ErrOperationCancel:
			data.Reason = "cancelled"
		default:
			data.Reason = "error"
			data.Code = string(rerr.Code)
			data.Message = rerr.Message
		}
	}
	m.write(Event{Version: ProtocolVersion, Event: "logs.closed", Subscription: id, Data: data})
}

func (m *subManager) write(ev Event) {
	m.mu.Lock()
	w := m.writer
	m.mu.Unlock()
	if w == nil {
		return
	}
	if err := w.writeMessage(ev); err != nil {
		m.log.Error("writing log event", "subscription", ev.Subscription, "err", err)
	}
}

// activeCount reports how many subscriptions are live (used by tests).
func (m *subManager) activeCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.subs)
}

// lineBatch coalesces emitted lines and flushes them as one event, either when
// it fills (max) or when flushNow is called (the ticker or end of stream). add
// is called from the operations stream goroutine; flushNow from the ticker
// goroutine and the run goroutine — so it is mutex-guarded.
type lineBatch struct {
	mu    sync.Mutex
	lines []string
	max   int
	flush func([]string)
}

func (b *lineBatch) add(line string) {
	b.mu.Lock()
	b.lines = append(b.lines, line)
	full := b.max > 0 && len(b.lines) >= b.max
	var out []string
	if full {
		out = b.lines
		b.lines = nil
	}
	b.mu.Unlock()
	if out != nil {
		b.flush(out)
	}
}

func (b *lineBatch) flushNow() {
	b.mu.Lock()
	out := b.lines
	b.lines = nil
	b.mu.Unlock()
	if len(out) > 0 {
		b.flush(out)
	}
}

// --- request handlers -----------------------------------------------------

// logsSubscribeParams is the params payload of a logs.subscribe request.
type logsSubscribeParams struct {
	Server string `json:"server"`
	Target string `json:"target"`
	Tail   int    `json:"tail"`
	Follow bool   `json:"follow"`
}

// logsSubscribeResult acknowledges a subscription with its id and the effective
// (clamped) tail so the UI can show how much backlog to expect.
type logsSubscribeResult struct {
	Subscription string `json:"subscription"`
	Tail         int    `json:"tail"`
	Follow       bool   `json:"follow"`
}

func (s *Server) handleLogsSubscribe(ctx context.Context, w *syncWriter, req Request) {
	if s.ops == nil {
		s.writeError(w, req.ID, newError(ErrInternal, "bridge is not configured for data methods", false, nil))
		return
	}

	var p logsSubscribeParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &p); err != nil {
			s.writeError(w, req.ID, newError(ErrInvalidRequest, "invalid params for logs.subscribe", false, nil))
			return
		}
	}
	if p.Server == "" {
		s.writeError(w, req.ID, newError(ErrInvalidRequest, "logs.subscribe requires a 'server' param", false, nil))
		return
	}
	if p.Target == "" {
		s.writeError(w, req.ID, newError(ErrInvalidRequest, "logs.subscribe requires a 'target' param", false, nil))
		return
	}

	id, tail, rerr := s.subs.start(ctx, operations.LogOptions{
		Server: p.Server,
		Target: p.Target,
		Tail:   p.Tail,
		Follow: p.Follow,
	})
	if rerr != nil {
		s.writeError(w, req.ID, rerr)
		return
	}
	s.writeResult(w, req.ID, logsSubscribeResult{Subscription: id, Tail: tail, Follow: p.Follow})
}

// logsUnsubscribeParams is the params payload of a logs.unsubscribe request.
type logsUnsubscribeParams struct {
	Subscription string `json:"subscription"`
}

// logsUnsubscribeResult reports whether a live subscription was found and
// cancelled. Unsubscribing an unknown id is not an error — it is idempotent so a
// double unsubscribe (e.g. window close racing an explicit cancel) is harmless.
type logsUnsubscribeResult struct {
	OK    bool `json:"ok"`
	Found bool `json:"found"`
}

func (s *Server) handleLogsUnsubscribe(w *syncWriter, req Request) {
	var p logsUnsubscribeParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &p); err != nil {
			s.writeError(w, req.ID, newError(ErrInvalidRequest, "invalid params for logs.unsubscribe", false, nil))
			return
		}
	}
	if p.Subscription == "" {
		s.writeError(w, req.ID, newError(ErrInvalidRequest, "logs.unsubscribe requires a 'subscription' param", false, nil))
		return
	}
	found := s.subs.stop(p.Subscription)
	s.writeResult(w, req.ID, logsUnsubscribeResult{OK: true, Found: found})
}
