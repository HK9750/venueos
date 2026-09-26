// Package apperror defines stable, transport-neutral application error codes.
package apperror

import "errors"

// MaxDetails is the maximum safe number of structured details on one error.
const MaxDetails = 50

// Code is a stable machine-readable failure classification.
type Code string

const (
	CodeMalformedRequest      Code = "malformed_request"
	CodeInvalidRequest        Code = "invalid_request"
	CodeValidationFailed      Code = "validation_failed"
	CodeInvalidCursor         Code = "invalid_cursor"
	CodeUnauthenticated       Code = "unauthenticated"
	CodePermissionDenied      Code = "permission_denied"
	CodeNotFound              Code = "not_found"
	CodeConflict              Code = "conflict"
	CodeIdempotencyKeyReused  Code = "idempotency_key_reused"
	CodePreconditionFailed    Code = "precondition_failed"
	CodePayloadTooLarge       Code = "payload_too_large"
	CodeUnsupportedMediaType  Code = "unsupported_media_type"
	CodeRateLimited           Code = "rate_limited"
	CodeInternal              Code = "internal_error"
	CodeDependencyUnavailable Code = "dependency_unavailable"
	CodeDependencyTimeout     Code = "dependency_timeout"
)

// Valid reports whether code is part of the stable platform code set.
func (code Code) Valid() bool {
	switch code {
	case CodeMalformedRequest, CodeInvalidRequest, CodeValidationFailed,
		CodeInvalidCursor, CodeUnauthenticated, CodePermissionDenied,
		CodeNotFound, CodeConflict, CodeIdempotencyKeyReused,
		CodePreconditionFailed, CodePayloadTooLarge, CodeUnsupportedMediaType,
		CodeRateLimited, CodeInternal, CodeDependencyUnavailable,
		CodeDependencyTimeout:
		return true
	default:
		return false
	}
}

// Detail is safe bounded context for one invalid field or rule.
type Detail struct {
	Field   string
	Code    string
	Message string
}

// Error is a safe application error. Its cause is available to internal callers
// through errors.Unwrap but is not included in Error's public-safe message.
type Error struct {
	code    Code
	message string
	details []Detail
	cause   error
}

func New(code Code, message string, details ...Detail) *Error {
	return &Error{code: code, message: message, details: cloneDetails(details)}
}

func Wrap(cause error, code Code, message string, details ...Detail) *Error {
	return &Error{code: code, message: message, details: cloneDetails(details), cause: cause}
}

func (err *Error) Error() string { return string(err.code) + ": " + err.message }

func (err *Error) Unwrap() error { return err.cause }

func (err *Error) Code() Code { return err.code }

func (err *Error) Message() string { return err.message }

func (err *Error) Details() []Detail { return append([]Detail(nil), err.details...) }

// CodeOf finds the outermost classified application error in an error chain.
func CodeOf(err error) (Code, bool) {
	var classified *Error
	if !errors.As(err, &classified) {
		return "", false
	}
	return classified.Code(), true
}

func cloneDetails(details []Detail) []Detail {
	if len(details) > MaxDetails {
		details = details[:MaxDetails]
	}
	return append([]Detail(nil), details...)
}
