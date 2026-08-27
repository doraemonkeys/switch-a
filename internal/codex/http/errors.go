package codexhttp

import (
	"errors"
	"fmt"
)

type ErrorKind string

const (
	ErrorClientInput           ErrorKind = "client_input"
	ErrorDependencyUnavailable ErrorKind = "dependency_unavailable"
	ErrorIdentityMismatch      ErrorKind = "identity_mismatch"
)

type Error struct {
	Kind  ErrorKind
	Stage string
	Cause error
}

func (e *Error) Error() string {
	if e == nil {
		return "codex HTTP integration error"
	}
	if e.Cause == nil {
		return fmt.Sprintf("codex HTTP %s failed (%s)", e.Stage, e.Kind)
	}
	return fmt.Sprintf("codex HTTP %s failed (%s): %v", e.Stage, e.Kind, e.Cause)
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func IsKind(err error, kind ErrorKind) bool {
	var target *Error
	return errors.As(err, &target) && target.Kind == kind
}

func clientError(stage string, cause error) error {
	return &Error{Kind: ErrorClientInput, Stage: stage, Cause: cause}
}

func dependencyError(stage string, cause error) error {
	return &Error{Kind: ErrorDependencyUnavailable, Stage: stage, Cause: cause}
}

func identityError(stage string, cause error) error {
	return &Error{Kind: ErrorIdentityMismatch, Stage: stage, Cause: cause}
}
