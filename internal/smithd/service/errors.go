package service

import "errors"

// Kind classifies an operation error so transports can map it, e.g. to an
// HTTP status code
type Kind int

const (
	// KindInvalid means the request was invalid (HTTP 400)
	KindInvalid Kind = iota + 1
	// KindNotFound means a referenced resource does not exist (HTTP 404)
	KindNotFound
	// KindConflict means the request conflicts with existing state (HTTP 409)
	KindConflict
)

// Error is an error caused by the caller's request rather than by smithd.
// Any other error returned by the service is an internal failure.
type Error struct {
	Kind    Kind
	Code    string // machine-readable code, e.g. "invalid_environment"
	Message string // human-readable message, safe to show to users
}

func (e *Error) Error() string { return e.Message }

func invalid(code, message string) error {
	return &Error{Kind: KindInvalid, Code: code, Message: message}
}

func notFound(message string) error {
	return &Error{Kind: KindNotFound, Code: "not_found", Message: message}
}

func conflict(message string) error {
	return &Error{Kind: KindConflict, Code: "conflict", Message: message}
}

// AsError returns the *Error in err's chain, if any
func AsError(err error) (*Error, bool) {
	var e *Error
	ok := errors.As(err, &e)
	return e, ok
}
