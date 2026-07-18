package operations

// ErrorCode is a stable, machine-facing error identifier returned by the shared
// operation layer. The desktop UI branches on these codes, never on the
// human-readable message. The string values are a SUBSET of, and identical to,
// the wire codes defined in cmd/neo-bridge (which mirror ERROR_CODES in
// apps/desktop/src/lib/protocol.ts). The bridge maps an operations.Error
// straight onto a protocol error by reusing this code string — see
// cmd/neo-bridge/data.go.
type ErrorCode string

const (
	ErrInvalidRequest     ErrorCode = "invalid_request"
	ErrServerNotFound     ErrorCode = "server_not_found"
	ErrAppNotFound        ErrorCode = "app_not_found"
	ErrSSHUnknownHost     ErrorCode = "ssh_unknown_host"
	ErrSSHAuthFailed      ErrorCode = "ssh_auth_failed"
	ErrSSHUnreachable     ErrorCode = "ssh_unreachable"
	ErrRemoteStateInvalid ErrorCode = "remote_state_invalid"
	ErrOperationTimeout   ErrorCode = "operation_timeout"
	ErrOperationCancelled ErrorCode = "operation_cancelled"
	ErrActionNotAllowed   ErrorCode = "action_not_allowed"
	ErrNotActivated       ErrorCode = "not_activated"
	ErrInternal           ErrorCode = "internal_error"
)

// Error is a typed operation failure carrying a stable code, a retryability
// hint, and optional structured details. It implements error and unwraps to the
// underlying cause so callers can still inspect it with errors.As/Is.
type Error struct {
	Code      ErrorCode
	Message   string
	Retryable bool
	Details   map[string]interface{}
	wrapped   error
}

func (e *Error) Error() string {
	if e.wrapped != nil {
		return string(e.Code) + ": " + e.Message + ": " + e.wrapped.Error()
	}
	return string(e.Code) + ": " + e.Message
}

func (e *Error) Unwrap() error { return e.wrapped }

// newError builds an *Error with a guaranteed non-nil (possibly empty) Details
// map so the wire shape stays stable.
func newError(code ErrorCode, message string, retryable bool, wrapped error, details map[string]interface{}) *Error {
	if details == nil {
		details = map[string]interface{}{}
	}
	return &Error{Code: code, Message: message, Retryable: retryable, wrapped: wrapped, Details: details}
}
