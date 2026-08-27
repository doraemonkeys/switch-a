package codexidentity

import (
	"errors"
	"fmt"
)

// ErrorKind is stable decision context. Error values intentionally never carry
// client credentials, credential subjects, or keyed digests.
type ErrorKind string

const (
	ErrorInvalidInput            ErrorKind = "invalid_input"
	ErrorInvalidOrigin           ErrorKind = "invalid_origin"
	ErrorSubjectPending          ErrorKind = "subject_pending"
	ErrorSnapshotConflict        ErrorKind = "snapshot_conflict"
	ErrorAppliedIdentityMismatch ErrorKind = "applied_identity_mismatch"
	ErrorDigestUnavailable       ErrorKind = "digest_unavailable"
)

// Error provides typed, log-safe failure information without reflecting the
// rejected value into an error string.
type Error struct {
	Kind   ErrorKind
	Field  string
	Reason string
	cause  error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	message := "codex identity: " + string(e.Kind)
	if e.Field != "" {
		message += " (field=" + e.Field + ")"
	}
	if e.Reason != "" {
		message += ": " + e.Reason
	}
	return message
}

func (e *Error) Unwrap() error { return e.cause }

func (e *Error) Is(target error) bool {
	var other *Error
	return errors.As(target, &other) && e.Kind == other.Kind
}

func errorOf(kind ErrorKind, field, reason string, cause error) error {
	return &Error{Kind: kind, Field: field, Reason: reason, cause: cause}
}

func IsError(err error, kind ErrorKind) bool {
	return errors.Is(err, &Error{Kind: kind})
}

// AppliedIdentityMismatch describes which authority dimensions disagreed while
// deliberately omitting both expected and actual subject values.
type AppliedIdentityMismatch struct {
	Vendor  bool
	Origin  bool
	Subject bool
}

func (e *AppliedIdentityMismatch) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf(
		"codex identity: %s (vendor=%t origin=%t subject=%t)",
		ErrorAppliedIdentityMismatch,
		e.Vendor,
		e.Origin,
		e.Subject,
	)
}

func (e *AppliedIdentityMismatch) Is(target error) bool {
	var identityError *Error
	if errors.As(target, &identityError) && identityError.Kind == ErrorAppliedIdentityMismatch {
		return true
	}
	var other *AppliedIdentityMismatch
	return errors.As(target, &other)
}
