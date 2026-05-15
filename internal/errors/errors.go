// Package errors provides structured error types with Kind classification
// for programmatic error handling. It supports Go 1.13+ error wrapping semantics
// through Unwrap, IsKind, and As helpers.
//
// Since the package is named "errors", it cannot import the standard "errors"
// package. The As and Unwrap helpers implement the same interfaces manually.
package errors

import "fmt"

// Kind represents the category of an error.
type Kind string

const (
	// KindRetryable indicates the error may succeed if retried.
	KindRetryable Kind = "retryable"
	// KindFatal indicates a non-recoverable error.
	KindFatal Kind = "fatal"
	// KindInvalidInput means the user provided bad input.
	KindInvalidInput Kind = "invalid_input"
	// KindUnavailable means an external dependency is down.
	KindUnavailable Kind = "unavailable"
	// KindPermission means the operation was denied.
	KindPermission Kind = "permission"
	// KindNotFound means a resource does not exist.
	KindNotFound Kind = "not_found"
	// KindContextLength means the context window was exceeded.
	KindContextLength Kind = "context_length"
)

// Error is a structured error with a kind and message.
type Error struct {
	Kind    Kind
	Message string
	Cause   error
}

// Error returns a formatted error string. When a cause is present, the format
// is "[kind] message: cause". Without a cause it is "[kind] message".
func (e *Error) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("[%s] %s: %v", e.Kind, e.Message, e.Cause)
	}
	return fmt.Sprintf("[%s] %s", e.Kind, e.Message)
}

// Unwrap returns the wrapped cause, supporting the errors.Unwrap interface.
func (e *Error) Unwrap() error {
	return e.Cause
}

// New creates a new Error with the given kind and message.
func New(kind Kind, message string) *Error {
	return &Error{Kind: kind, Message: message}
}

// Wrap wraps an existing error with a kind and message.
func Wrap(err error, kind Kind, message string) *Error {
	return &Error{Kind: kind, Message: message, Cause: err}
}

// NewRetryable creates a new retryable error.
func NewRetryable(message string) *Error { return New(KindRetryable, message) }

// NewFatal creates a new fatal error.
func NewFatal(message string) *Error { return New(KindFatal, message) }

// WrapRetryable wraps an error as retryable.
func WrapRetryable(err error, message string) *Error { return Wrap(err, KindRetryable, message) }

// WrapFatal wraps an error as fatal.
func WrapFatal(err error, message string) *Error { return Wrap(err, KindFatal, message) }

// IsKind checks if any error in the chain has the given kind.
// It walks the error chain via Unwrap, checking each *Error's Kind.
func IsKind(err error, kind Kind) bool {
	for {
		if err == nil {
			return false
		}
		if e, ok := err.(*Error); ok && e.Kind == kind {
			return true
		}
		// Walk to the next error in the chain.
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
}

// As finds the first *Error in the error chain that matches, and sets target
// to it. Returns true if found. This implements the standard errors.As pattern
// without importing the "errors" package.
func As(err error, target **Error) bool {
	for {
		if err == nil {
			return false
		}
		if e, ok := err.(*Error); ok {
			*target = e
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
}

// Unwrap returns the cause of an error by calling its Unwrap method.
// Returns nil if the error does not implement the Unwrap interface.
func Unwrap(err error) error {
	u, ok := err.(interface{ Unwrap() error })
	if !ok {
		return nil
	}
	return u.Unwrap()
}
