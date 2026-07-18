package main

import "encoding/json"

// ProtocolVersion is the wire protocol version this bridge speaks. It is
// mirrored by PROTOCOL_VERSION in apps/desktop/src/lib/protocol.ts and
// PROTOCOL_VERSION in apps/desktop/src-tauri/src/bridge.rs. Bump all three
// together when the envelope changes incompatibly.
const ProtocolVersion = 1

// ErrorCode is a stable, machine-facing error identifier. The desktop UI
// branches on these codes and never parses the human-readable message. This set
// mirrors ERROR_CODES in apps/desktop/src/lib/protocol.ts exactly.
type ErrorCode string

const (
	ErrInvalidRequest   ErrorCode = "invalid_request"
	ErrProtocolMismatch ErrorCode = "protocol_mismatch"
	ErrNotActivated     ErrorCode = "not_activated"
	ErrServerNotFound   ErrorCode = "server_not_found"
	ErrAppNotFound      ErrorCode = "app_not_found"
	ErrSSHUnknownHost   ErrorCode = "ssh_unknown_host"
	ErrSSHAuthFailed    ErrorCode = "ssh_auth_failed"
	ErrSSHUnreachable   ErrorCode = "ssh_unreachable"
	ErrRemoteStateBad   ErrorCode = "remote_state_invalid"
	ErrOperationTimeout ErrorCode = "operation_timeout"
	ErrOperationCancel  ErrorCode = "operation_cancelled"
	ErrActionNotAllowed ErrorCode = "action_not_allowed"
	ErrInternal         ErrorCode = "internal_error"
)

// ErrorCodes is the full, ordered set of stable error codes. It exists so tests
// (and future code generation) can assert parity with the TypeScript mirror.
var ErrorCodes = []ErrorCode{
	ErrInvalidRequest,
	ErrProtocolMismatch,
	ErrNotActivated,
	ErrServerNotFound,
	ErrAppNotFound,
	ErrSSHUnknownHost,
	ErrSSHAuthFailed,
	ErrSSHUnreachable,
	ErrRemoteStateBad,
	ErrOperationTimeout,
	ErrOperationCancel,
	ErrActionNotAllowed,
	ErrInternal,
}

// Request is one newline-delimited JSON request read from stdin.
//
//	{"version":1,"id":"req-123","method":"bridge.hello","params":{...}}
type Request struct {
	Version int             `json:"version"`
	ID      string          `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response is one newline-delimited JSON response written to stdout. Exactly one
// of Result or Error is populated.
//
//	{"version":1,"id":"req-123","result":{...}}
//	{"version":1,"id":"req-123","error":{"code":"...","message":"...",...}}
type Response struct {
	Version int         `json:"version"`
	ID      string      `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *RPCError   `json:"error,omitempty"`
}

// RPCError is the structured error payload. Details is always a (possibly empty)
// object so the wire shape stays stable, matching the plan's envelope.
type RPCError struct {
	Code      ErrorCode              `json:"code"`
	Message   string                 `json:"message"`
	Retryable bool                   `json:"retryable"`
	Details   map[string]interface{} `json:"details"`
}

// Event is an unsolicited streaming message (e.g. a log line). No method or ID
// is implemented in slice 2; the type is defined so the envelope contract and
// its encoding are pinned by tests from the start.
//
//	{"version":1,"event":"logs.line","subscription":"log-45","data":{...}}
type Event struct {
	Version      int         `json:"version"`
	Event        string      `json:"event"`
	Subscription string      `json:"subscription,omitempty"`
	Data         interface{} `json:"data,omitempty"`
}

// newError builds an RPCError with a guaranteed non-nil Details map.
func newError(code ErrorCode, message string, retryable bool, details map[string]interface{}) *RPCError {
	if details == nil {
		details = map[string]interface{}{}
	}
	return &RPCError{Code: code, Message: message, Retryable: retryable, Details: details}
}
