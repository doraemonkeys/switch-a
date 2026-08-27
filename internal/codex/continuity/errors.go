// Package codexcontinuity owns durable Codex opaque-value ownership and the
// process-local connection capability needed by response.inject.
package codexcontinuity

import (
	"errors"
	"fmt"
)

type ErrorKind string

const (
	ErrorInvalidInput       ErrorKind = "invalid_input"
	ErrorUnknown            ErrorKind = "unknown"
	ErrorExpired            ErrorKind = "expired"
	ErrorConflict           ErrorKind = "conflict"
	ErrorUnavailable        ErrorKind = "unavailable"
	ErrorCapacity           ErrorKind = "capacity"
	ErrorInvalidTransition  ErrorKind = "invalid_transition"
	ErrorInactiveGeneration ErrorKind = "inactive_generation"
)

// Error carries stable, log-safe decision context. Opaque values, HMAC sums,
// client credentials, and credential subjects are deliberately absent.
type Error struct {
	Kind        ErrorKind
	BindingKind Kind
	OperationID string
	Reason      string
	cause       error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	message := "codex continuity: " + string(e.Kind)
	if e.BindingKind != "" {
		message += " (binding_kind=" + string(e.BindingKind) + ")"
	}
	if e.OperationID != "" {
		message += " (operation_id=" + safeLabel(e.OperationID) + ")"
	}
	if e.Reason != "" {
		message += ": " + e.Reason
	}
	return message
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func (e *Error) Is(target error) bool {
	var other *Error
	return errors.As(target, &other) && e.Kind == other.Kind
}

func errorOf(kind ErrorKind, bindingKind Kind, operationID, reason string, cause error) error {
	return &Error{
		Kind:        kind,
		BindingKind: bindingKind,
		OperationID: operationID,
		Reason:      reason,
		cause:       cause,
	}
}

func IsError(err error, kind ErrorKind) bool {
	return errors.Is(err, &Error{Kind: kind})
}

func safeLabel(value string) string {
	for _, char := range value {
		if char < 0x20 || char == 0x7f {
			return "invalid"
		}
	}
	if value == "" {
		return "invalid"
	}
	return value
}

func unavailable(kind Kind, operationID, action string, cause error) error {
	return errorOf(ErrorUnavailable, kind, operationID, fmt.Sprintf("%s failed", action), cause)
}
