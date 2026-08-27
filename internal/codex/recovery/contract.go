// Package codexrecovery defines the carrier-independent client recovery
// contract shared by Codex HTTP and WebSocket adapters.
package codexrecovery

import (
	"errors"
	"net/http"

	"github.com/coder/websocket"
	"github.com/doraemonkeys/switch-a/internal/codex/continuity"
	"github.com/doraemonkeys/switch-a/internal/codex/cookie"
)

type CarrierPhase string

const (
	PhaseHTTP                CarrierPhase = "http"
	PhaseWebSocketPreUpgrade CarrierPhase = "websocket_pre_upgrade"
	PhaseWebSocketAccepted   CarrierPhase = "websocket_accepted"
)

// Condition is stable decision context for traces and adapter behavior. It is
// intentionally narrower than the lower-layer error vocabularies so transport
// adapters cannot accidentally publish persistence implementation details.
type Condition string

const (
	ConditionStateConflict         Condition = "state_conflict"
	ConditionReconnectRequired     Condition = "reconnect_required"
	ConditionNewThreadRequired     Condition = "new_thread_required"
	ConditionStateStoreUnavailable Condition = "state_store_unavailable"
	ConditionProtocolInvalid       Condition = "protocol_invalid"
	ConditionInternalFailure       Condition = "internal_failure"
)

type ErrorCode string

const (
	ErrorCodeStateConflict         ErrorCode = "CODEX_STATE_CONFLICT"
	ErrorCodeReconnectRequired     ErrorCode = "CODEX_RECONNECT_REQUIRED"
	ErrorCodeNewThreadRequired     ErrorCode = "CODEX_NEW_THREAD_REQUIRED"
	ErrorCodeStateStoreUnavailable ErrorCode = "CODEX_STATE_STORE_UNAVAILABLE"
	ErrorCodeProtocolInvalid       ErrorCode = "CODEX_PROTOCOL_INVALID"
	ErrorCodeInternal              ErrorCode = "INTERNAL_ERROR"
)

type RecoveryAction string

const (
	RecoveryActionNewThread      RecoveryAction = "new_thread"
	RecoveryActionReconnect      RecoveryAction = "reconnect"
	RecoveryActionRetry          RecoveryAction = "retry"
	RecoveryActionCorrectRequest RecoveryAction = "correct_request"
)

// Error lets an adapter attach a precise semantic condition when no lower
// layer owns one, while Unwrap keeps the original diagnostic and retry cause.
type Error struct {
	condition Condition
	cause     error
}

// Mark is reserved for adapter evidence that has no lower-layer typed error,
// such as a recognized malformed control field or a missing live connection.
func Mark(condition Condition, cause error) error {
	return &Error{condition: normalizedCondition(condition), cause: cause}
}

func (e *Error) Error() string {
	if e == nil {
		return "codex recovery failure"
	}
	return "codex recovery failure: " + string(e.condition)
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func (e *Error) Condition() Condition {
	if e == nil {
		return ConditionInternalFailure
	}
	return e.condition
}

// Decision carries both carrier surfaces from the single frozen table. Keeping
// both values together makes HTTP/pre-upgrade and accepted-WS adapters consume
// the same mapping; Phase records which surface the caller is applying.
type Decision struct {
	phase              CarrierPhase
	condition          Condition
	httpStatus         int
	errorCode          ErrorCode
	webSocketCloseCode websocket.StatusCode
	recoveryAction     RecoveryAction
}

func (d Decision) Phase() CarrierPhase                      { return d.phase }
func (d Decision) Condition() Condition                     { return d.condition }
func (d Decision) HTTPStatus() int                          { return d.httpStatus }
func (d Decision) ErrorCode() ErrorCode                     { return d.errorCode }
func (d Decision) WebSocketCloseCode() websocket.StatusCode { return d.webSocketCloseCode }
func (d Decision) RecoveryAction() RecoveryAction           { return d.recoveryAction }

type contract struct {
	condition          Condition
	httpStatus         int
	errorCode          ErrorCode
	webSocketCloseCode websocket.StatusCode
	recoveryAction     RecoveryAction
}

var internalFailureContract = contract{
	condition:          ConditionInternalFailure,
	httpStatus:         http.StatusInternalServerError,
	errorCode:          ErrorCodeInternal,
	webSocketCloseCode: websocket.StatusInternalError,
	recoveryAction:     RecoveryActionRetry,
}

// Classify is pure: it observes wrapped causes but neither mutates nor replaces
// the supplied error. Unknown causes deliberately use the adapter-default row.
func Classify(root error, phase CarrierPhase) Decision {
	condition, classified := classifyCondition(root)
	if !classified {
		condition = ConditionInternalFailure
	}
	return decisionFor(condition, phase)
}

// ClassifyWithFallback lets a carrier adapter translate its own stable error
// vocabulary without weakening deeper semantic evidence. In particular, a
// future typed continuity or Cookie error remains an internal failure instead
// of being overwritten by a coarser adapter category.
func ClassifyWithFallback(root error, phase CarrierPhase, fallback Condition) Decision {
	condition, classified := classifyCondition(root)
	if !classified {
		condition = normalizedCondition(fallback)
	}
	return decisionFor(condition, phase)
}

func decisionFor(condition Condition, phase CarrierPhase) Decision {
	mapping := contractFor(condition)
	return Decision{
		phase:              phase,
		condition:          mapping.condition,
		httpStatus:         mapping.httpStatus,
		errorCode:          mapping.errorCode,
		webSocketCloseCode: mapping.webSocketCloseCode,
		recoveryAction:     mapping.recoveryAction,
	}
}

func classifyCondition(root error) (Condition, bool) {
	var marked *Error
	if errors.As(root, &marked) && marked != nil {
		return normalizedCondition(marked.condition), true
	}

	var continuityError *codexcontinuity.Error
	if errors.As(root, &continuityError) && continuityError != nil {
		switch continuityError.Kind {
		case codexcontinuity.ErrorInvalidInput:
			return ConditionProtocolInvalid, true
		case codexcontinuity.ErrorUnknown, codexcontinuity.ErrorExpired:
			return ConditionNewThreadRequired, true
		case codexcontinuity.ErrorConflict, codexcontinuity.ErrorInvalidTransition:
			return ConditionStateConflict, true
		case codexcontinuity.ErrorUnavailable, codexcontinuity.ErrorCapacity:
			return ConditionStateStoreUnavailable, true
		case codexcontinuity.ErrorInactiveGeneration:
			return ConditionReconnectRequired, true
		default:
			return ConditionInternalFailure, true
		}
	}

	var persistenceError *providercookie.PersistenceError
	if errors.As(root, &persistenceError) && persistenceError != nil {
		switch persistenceError.Kind {
		case providercookie.PersistenceUnavailable,
			providercookie.PersistenceCorrupt,
			providercookie.PersistenceCrypto,
			providercookie.PersistenceDecrypt:
			return ConditionStateStoreUnavailable, true
		default:
			return ConditionInternalFailure, true
		}
	}

	if errors.Is(root, providercookie.ErrStorage) ||
		errors.Is(root, providercookie.ErrStorageCorrupt) ||
		errors.Is(root, providercookie.ErrCrypto) ||
		errors.Is(root, providercookie.ErrDecrypt) ||
		errors.Is(root, providercookie.ErrLimitExceeded) ||
		errors.Is(root, providercookie.ErrIdentifierClash) {
		return ConditionStateStoreUnavailable, true
	}
	return ConditionInternalFailure, false
}

func normalizedCondition(condition Condition) Condition {
	switch condition {
	case ConditionStateConflict,
		ConditionReconnectRequired,
		ConditionNewThreadRequired,
		ConditionStateStoreUnavailable,
		ConditionProtocolInvalid,
		ConditionInternalFailure:
		return condition
	default:
		return ConditionInternalFailure
	}
}

func contractFor(condition Condition) contract {
	switch condition {
	case ConditionStateConflict:
		return contract{
			condition:          condition,
			httpStatus:         http.StatusConflict,
			errorCode:          ErrorCodeStateConflict,
			webSocketCloseCode: websocket.StatusPolicyViolation,
			recoveryAction:     RecoveryActionNewThread,
		}
	case ConditionReconnectRequired:
		return contract{
			condition:          condition,
			httpStatus:         http.StatusConflict,
			errorCode:          ErrorCodeReconnectRequired,
			webSocketCloseCode: websocket.StatusServiceRestart,
			recoveryAction:     RecoveryActionReconnect,
		}
	case ConditionNewThreadRequired:
		return contract{
			condition:          condition,
			httpStatus:         http.StatusGone,
			errorCode:          ErrorCodeNewThreadRequired,
			webSocketCloseCode: websocket.StatusPolicyViolation,
			recoveryAction:     RecoveryActionNewThread,
		}
	case ConditionStateStoreUnavailable:
		return contract{
			condition:          condition,
			httpStatus:         http.StatusServiceUnavailable,
			errorCode:          ErrorCodeStateStoreUnavailable,
			webSocketCloseCode: websocket.StatusTryAgainLater,
			recoveryAction:     RecoveryActionRetry,
		}
	case ConditionProtocolInvalid:
		return contract{
			condition:          condition,
			httpStatus:         http.StatusBadRequest,
			errorCode:          ErrorCodeProtocolInvalid,
			webSocketCloseCode: websocket.StatusPolicyViolation,
			recoveryAction:     RecoveryActionCorrectRequest,
		}
	case ConditionInternalFailure:
		return internalFailureContract
	default:
		return internalFailureContract
	}
}
