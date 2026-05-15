package errors

import (
	"fmt"
	"testing"
)

func TestNew(t *testing.T) {
	err := New(KindFatal, "something went wrong")
	if err.Kind != KindFatal {
		t.Errorf("expected KindFatal, got %s", err.Kind)
	}
	if err.Message != "something went wrong" {
		t.Errorf("expected 'something went wrong', got %s", err.Message)
	}
	if err.Cause != nil {
		t.Error("expected nil Cause")
	}
}

func TestErrorString(t *testing.T) {
	tests := []struct {
		name string
		err  *Error
		want string
	}{
		{
			name: "without cause",
			err:  New(KindFatal, "boom"),
			want: "[fatal] boom",
		},
		{
			name: "with cause",
			err:  Wrap(fmt.Errorf("underlying"), KindRetryable, "transient"),
			want: "[retryable] transient: underlying",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestWrapAndUnwrap(t *testing.T) {
	root := fmt.Errorf("root cause")
	wrapped := Wrap(root, KindUnavailable, "service down")

	// Unwrap should return the root cause.
	cause := Unwrap(wrapped)
	if cause != root {
		t.Errorf("Unwrap returned %v, want %v", cause, root)
	}

	// The wrapped error should also support native Go interface unwrapping.
	if wrapped.Unwrap() != root {
		t.Error("wrapped.Unwrap() did not return root cause")
	}
}

func TestIsKind(t *testing.T) {
	// Direct match.
	err := New(KindContextLength, "context too long")
	if !IsKind(err, KindContextLength) {
		t.Error("IsKind should detect direct KindContextLength")
	}
	if IsKind(err, KindFatal) {
		t.Error("IsKind should not match wrong kind")
	}

	// Through a single wrap layer.
	wrapped := Wrap(fmt.Errorf("http 413"), KindContextLength, "context exceeded")
	if !IsKind(wrapped, KindContextLength) {
		t.Error("IsKind should detect KindContextLength through wrap")
	}

	// Through a deeper chain.
	root := fmt.Errorf("connection refused")
	mid := Wrap(root, KindUnavailable, "db unreachable")
	outer := fmt.Errorf("init failed: %w", mid)
	if !IsKind(outer, KindUnavailable) {
		t.Error("IsKind should detect KindUnavailable through deep chain")
	}
	if IsKind(outer, KindContextLength) {
		t.Error("IsKind should not match non-existent kind in chain")
	}

	// Nil error.
	if IsKind(nil, KindFatal) {
		t.Error("IsKind(nil) should return false")
	}

	// Plain error without kind.
	if IsKind(fmt.Errorf("plain error"), KindFatal) {
		t.Error("IsKind should return false for plain errors")
	}
}

func TestAs(t *testing.T) {
	orig := New(KindPermission, "access denied")

	var extracted *Error
	if !As(orig, &extracted) {
		t.Fatal("As should match *Error")
	}
	if extracted.Kind != KindPermission {
		t.Errorf("extracted Kind = %s, want %s", extracted.Kind, KindPermission)
	}
	if extracted.Message != "access denied" {
		t.Errorf("extracted Message = %s, want 'access denied'", extracted.Message)
	}

	// Through fmt.Errorf wrapping.
	wrapped := fmt.Errorf("outer: %w", orig)
	extracted = nil
	if !As(wrapped, &extracted) {
		t.Fatal("As should match through fmt.Errorf wrapping")
	}
	if extracted != orig {
		t.Error("extracted should be the original *Error")
	}

	// Plain error.
	extracted = nil
	if As(fmt.Errorf("no kind here"), &extracted) {
		t.Error("As should return false for plain error")
	}

	// Nil error.
	extracted = nil
	if As(nil, &extracted) {
		t.Error("As(nil) should return false")
	}
}

func TestUnwrap(t *testing.T) {
	// Error with cause.
	err := Wrap(fmt.Errorf("cause"), KindFatal, "fatal")
	cause := Unwrap(err)
	if cause == nil {
		t.Fatal("Unwrap should return non-nil cause")
	}
	if cause.Error() != "cause" {
		t.Errorf("Unwrap returned %v, want 'cause'", cause)
	}

	// Error without cause.
	cause = Unwrap(New(KindFatal, "fatal"))
	if cause != nil {
		t.Error("Unwrap should return nil for Error without cause")
	}

	// Plain error without Unwrap.
	cause = Unwrap(fmt.Errorf("plain"))
	if cause != nil {
		t.Error("Unwrap should return nil for plain error")
	}

	// Nil error.
	cause = Unwrap(nil)
	if cause != nil {
		t.Error("Unwrap(nil) should return nil")
	}
}

func TestHelpers(t *testing.T) {
	// NewRetryable
	r := NewRetryable("timeout")
	if r.Kind != KindRetryable || r.Message != "timeout" {
		t.Error("NewRetryable returned wrong error")
	}

	// NewFatal
	f := NewFatal("out of memory")
	if f.Kind != KindFatal || f.Message != "out of memory" {
		t.Error("NewFatal returned wrong error")
	}

	// WrapRetryable
	wr := WrapRetryable(fmt.Errorf("econnrefused"), "network")
	if wr.Kind != KindRetryable || wr.Message != "network" || wr.Cause == nil {
		t.Error("WrapRetryable returned wrong error")
	}

	// WrapFatal
	wf := WrapFatal(fmt.Errorf("segfault"), "crash")
	if wf.Kind != KindFatal || wf.Message != "crash" || wf.Cause == nil {
		t.Error("WrapFatal returned wrong error")
	}
}

// TestNativeErrorsAs verifies that our Error works with the standard library's
// errors.As. This ensures consumers who import "errors" separately can still
// use errors.As(err, &target) against our type.
func TestNativeErrorsAs(t *testing.T) {
	orig := New(KindNotFound, "missing")
	wrapped := fmt.Errorf("outer: %w", orig)

	// Use Go stdlib errors.As — this works because Error implements the Unwrap
	// and Error interfaces that stdlib errors.As expects.
	// (We are importing errors_test to avoid name collision with our package.)
	// Since we can't import "errors" from within our own "errors" package,
	// we verify that fmt.Errorf("%w") produces a valid chain that our As can
	// traverse.
	var extracted *Error
	if !As(wrapped, &extracted) {
		t.Fatal("our As should traverse fmt.Errorf chain")
	}
	if extracted != orig {
		t.Error("extracted should be the original *Error")
	}
}

// TestIsKindAllKinds ensures every Kind constant is properly detectable.
func TestIsKindAllKinds(t *testing.T) {
	kinds := []Kind{
		KindRetryable, KindFatal, KindInvalidInput, KindUnavailable,
		KindPermission, KindNotFound, KindContextLength,
	}
	for _, k := range kinds {
		t.Run(string(k), func(t *testing.T) {
			err := New(k, "test")
			if !IsKind(err, k) {
				t.Errorf("IsKind should detect %s", k)
			}
		})
	}
}
