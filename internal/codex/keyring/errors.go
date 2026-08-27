package codexkeyring

import (
	"errors"
	"fmt"
)

// ErrorKind lets callers distinguish operator configuration failures from
// runtime authentication failures without inspecting error text.
type ErrorKind string

const (
	ErrorInvalidDocument      ErrorKind = "invalid_document"
	ErrorInvalidPurpose       ErrorKind = "invalid_purpose"
	ErrorMissingVersion       ErrorKind = "missing_version"
	ErrorUnknownVersion       ErrorKind = "unknown_version"
	ErrorAuthenticationFailed ErrorKind = "authentication_failed"
	ErrorRandomSource         ErrorKind = "random_source_failed"
	ErrorInvalidInput         ErrorKind = "invalid_input"
	ErrorCapabilityMissing    ErrorKind = "capability_missing"
)

// Error intentionally carries only non-secret decision context. In particular,
// key material, plaintext, ciphertext, nonces, and digests are never formatted.
type Error struct {
	Kind      ErrorKind
	Component string
	Version   string
	Detail    string
	cause     error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	message := "codex keyring: " + string(e.Kind)
	if e.Component != "" {
		message += " (component=" + e.Component + ")"
	}
	if e.Version != "" {
		message += " (version=" + e.Version + ")"
	}
	if e.Detail != "" {
		message += ": " + e.Detail
	}
	return message
}

func (e *Error) Unwrap() error { return e.cause }

func (e *Error) Is(target error) bool {
	var other *Error
	return errors.As(target, &other) && e.Kind == other.Kind
}

func errorOf(kind ErrorKind, component, version, detail string, cause error) error {
	return &Error{
		Kind:      kind,
		Component: component,
		Version:   version,
		Detail:    detail,
		cause:     cause,
	}
}

// IsError reports whether err belongs to a stable keyring error category.
func IsError(err error, kind ErrorKind) bool {
	return errors.Is(err, &Error{Kind: kind})
}

func invalidDocument(component, version, format string, args ...any) error {
	return errorOf(ErrorInvalidDocument, component, version, fmt.Sprintf(format, args...), nil)
}
