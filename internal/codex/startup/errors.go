package codexstartup

import (
	"errors"
	"fmt"
)

type ErrorKind string

const (
	ErrorConfigUnavailable ErrorKind = "config_unavailable"
	ErrorInvalidConfig     ErrorKind = "invalid_config"
	ErrorDependency        ErrorKind = "dependency_unsatisfied"
	ErrorCapabilityMissing ErrorKind = "capability_missing"
)

type Error struct {
	Kind       ErrorKind
	Feature    string
	Capability string
	cause      error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	message := "codex startup: " + string(e.Kind)
	if e.Feature != "" {
		message += " (feature=" + e.Feature + ")"
	}
	if e.Capability != "" {
		message += " (capability=" + e.Capability + ")"
	}
	return message
}

func (e *Error) Unwrap() error { return e.cause }

func (e *Error) Is(target error) bool {
	var other *Error
	return errors.As(target, &other) && e.Kind == other.Kind
}

func IsError(err error, kind ErrorKind) bool {
	return errors.Is(err, &Error{Kind: kind})
}

func wrap(kind ErrorKind, feature, capability string, cause error) error {
	return &Error{Kind: kind, Feature: feature, Capability: capability, cause: cause}
}

func invalidBoolean(key, value string) error {
	return wrap(
		ErrorInvalidConfig,
		key,
		"boolean_value",
		fmt.Errorf("value %q must be true, false, 1, or 0", value),
	)
}
