// Package codexws composes Codex security modules at WebSocket transport
// boundaries. It owns no persistence, credential, or wire-format policy.
package codexws

import (
	"errors"
	"fmt"

	"github.com/doraemonkeys/switch-a/internal/codex/continuity"
	"github.com/doraemonkeys/switch-a/internal/codex/headers"
)

type FailureClass string

const (
	FailureIdentity FailureClass = "identity"
	FailureProtocol FailureClass = "protocol"
	FailureStorage  FailureClass = "storage"
)

// Failure carries only stable diagnostic labels. Opaque state remains confined
// to the deep modules that produced the underlying error.
type Failure struct {
	Class FailureClass
	Stage string
	Cause error
}

func (e *Failure) Error() string {
	if e == nil {
		return "codex websocket failure"
	}
	if e.Cause == nil {
		return fmt.Sprintf("codex websocket %s failure at %s", e.Class, e.Stage)
	}
	return fmt.Sprintf("codex websocket %s failure at %s: %v", e.Class, e.Stage, e.Cause)
}

func (e *Failure) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func Classify(err error) FailureClass {
	var failure *Failure
	if errors.As(err, &failure) && failure != nil {
		return failure.Class
	}
	return ""
}

func protocolFailure(stage string, result codexheaders.Result) error {
	for _, decision := range result.Decisions() {
		if decision.Action() != codexheaders.ActionReject {
			continue
		}
		class := FailureProtocol
		switch decision.Reason() {
		case codexheaders.ReasonOwnerConflict,
			codexheaders.ReasonOwnerUnknown,
			codexheaders.ReasonOwnerUnavailable,
			codexheaders.ReasonOperationConflict,
			codexheaders.ReasonOperationUnavailable:
			class = FailureIdentity
		}
		return &Failure{
			Class: class,
			Stage: stage,
			Cause: fmt.Errorf("%s rejected: %s", decision.Field(), decision.Reason()),
		}
	}
	return &Failure{Class: FailureProtocol, Stage: stage, Cause: errors.New("protocol decision rejected")}
}

func continuityFailure(stage string, err error) error {
	class := FailureIdentity
	if codexcontinuity.IsError(err, codexcontinuity.ErrorUnavailable) ||
		codexcontinuity.IsError(err, codexcontinuity.ErrorCapacity) {
		class = FailureStorage
	}
	return &Failure{Class: class, Stage: stage, Cause: err}
}

func cookieFailure(stage string, err error) error {
	return &Failure{Class: FailureStorage, Stage: stage, Cause: err}
}
