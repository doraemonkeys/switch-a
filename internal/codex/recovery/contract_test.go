package codexrecovery_test

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/coder/websocket"
	"github.com/doraemonkeys/switch-a/internal/codex/continuity"
	"github.com/doraemonkeys/switch-a/internal/codex/cookie"
	"github.com/doraemonkeys/switch-a/internal/codex/recovery"
)

type expectedContract struct {
	condition codexrecovery.Condition
	status    int
	code      codexrecovery.ErrorCode
	closeCode websocket.StatusCode
	action    codexrecovery.RecoveryAction
}

var frozenContracts = []expectedContract{
	{codexrecovery.ConditionStateConflict, http.StatusConflict, codexrecovery.ErrorCodeStateConflict, websocket.StatusPolicyViolation, codexrecovery.RecoveryActionNewThread},
	{codexrecovery.ConditionReconnectRequired, http.StatusConflict, codexrecovery.ErrorCodeReconnectRequired, websocket.StatusServiceRestart, codexrecovery.RecoveryActionReconnect},
	{codexrecovery.ConditionNewThreadRequired, http.StatusGone, codexrecovery.ErrorCodeNewThreadRequired, websocket.StatusPolicyViolation, codexrecovery.RecoveryActionNewThread},
	{codexrecovery.ConditionStateStoreUnavailable, http.StatusServiceUnavailable, codexrecovery.ErrorCodeStateStoreUnavailable, websocket.StatusTryAgainLater, codexrecovery.RecoveryActionRetry},
	{codexrecovery.ConditionProtocolInvalid, http.StatusBadRequest, codexrecovery.ErrorCodeProtocolInvalid, websocket.StatusPolicyViolation, codexrecovery.RecoveryActionCorrectRequest},
	{codexrecovery.ConditionInternalFailure, http.StatusInternalServerError, codexrecovery.ErrorCodeInternal, websocket.StatusInternalError, codexrecovery.RecoveryActionRetry},
}

var carrierPhases = []codexrecovery.CarrierPhase{
	codexrecovery.PhaseHTTP,
	codexrecovery.PhaseWebSocketPreUpgrade,
	codexrecovery.PhaseWebSocketAccepted,
}

func TestFrozenRecoveryContractEveryRowAndCarrierPhase(t *testing.T) {
	for _, want := range frozenContracts {
		for _, phase := range carrierPhases {
			t.Run(string(want.condition)+"/"+string(phase), func(t *testing.T) {
				cause := errors.New("underlying cause")
				root := error(cause)
				if want.condition != codexrecovery.ConditionInternalFailure {
					root = fmt.Errorf("adapter context: %w", codexrecovery.Mark(want.condition, cause))
					if !errors.Is(root, cause) {
						t.Fatal("Mark did not preserve the wrapped cause")
					}
				}
				assertDecision(t, codexrecovery.Classify(root, phase), phase, want)
			})
		}
	}
}

func TestContinuityErrorKindsHaveExactRecoveryConditions(t *testing.T) {
	tests := []struct {
		kind codexcontinuity.ErrorKind
		want codexrecovery.Condition
	}{
		{codexcontinuity.ErrorInvalidInput, codexrecovery.ConditionProtocolInvalid},
		{codexcontinuity.ErrorUnknown, codexrecovery.ConditionNewThreadRequired},
		{codexcontinuity.ErrorExpired, codexrecovery.ConditionNewThreadRequired},
		{codexcontinuity.ErrorConflict, codexrecovery.ConditionStateConflict},
		{codexcontinuity.ErrorUnavailable, codexrecovery.ConditionStateStoreUnavailable},
		{codexcontinuity.ErrorCapacity, codexrecovery.ConditionStateStoreUnavailable},
		{codexcontinuity.ErrorInvalidTransition, codexrecovery.ConditionStateConflict},
		{codexcontinuity.ErrorInactiveGeneration, codexrecovery.ConditionReconnectRequired},
		{codexcontinuity.ErrorKind("future_kind"), codexrecovery.ConditionInternalFailure},
	}

	for _, test := range tests {
		t.Run(string(test.kind), func(t *testing.T) {
			root := fmt.Errorf("transport wrapper: %w", &codexcontinuity.Error{Kind: test.kind})
			want := contractByCondition(t, test.want)
			for _, phase := range carrierPhases {
				assertDecision(t, codexrecovery.Classify(root, phase), phase, want)
			}
		})
	}
}

func TestCookiePersistenceKindsHaveExactRecoveryCondition(t *testing.T) {
	kinds := []providercookie.PersistenceErrorKind{
		providercookie.PersistenceUnavailable,
		providercookie.PersistenceCorrupt,
		providercookie.PersistenceCrypto,
		providercookie.PersistenceDecrypt,
	}
	for _, kind := range kinds {
		t.Run(string(kind), func(t *testing.T) {
			root := fmt.Errorf("transport wrapper: %w", &providercookie.PersistenceError{Kind: kind, Operation: "test"})
			want := contractByCondition(t, codexrecovery.ConditionStateStoreUnavailable)
			for _, phase := range carrierPhases {
				assertDecision(t, codexrecovery.Classify(root, phase), phase, want)
			}
		})
	}

	unknown := &providercookie.PersistenceError{Kind: providercookie.PersistenceErrorKind("future_kind"), Operation: "test"}
	if got := codexrecovery.Classify(unknown, codexrecovery.PhaseHTTP).Condition(); got != codexrecovery.ConditionInternalFailure {
		t.Fatalf("unknown persistence kind Condition() = %q, want %q", got, codexrecovery.ConditionInternalFailure)
	}
}

func TestCookieStorageAndCapacityCategoriesRemainClassifiableWhenWrappedDirectly(t *testing.T) {
	tests := []error{
		providercookie.ErrStorage,
		providercookie.ErrStorageCorrupt,
		providercookie.ErrCrypto,
		providercookie.ErrDecrypt,
		providercookie.ErrLimitExceeded,
		providercookie.ErrIdentifierClash,
	}
	for _, cause := range tests {
		t.Run(cause.Error(), func(t *testing.T) {
			root := fmt.Errorf("cookie operation: %w", cause)
			got := codexrecovery.Classify(root, codexrecovery.PhaseWebSocketPreUpgrade)
			if got.Condition() != codexrecovery.ConditionStateStoreUnavailable {
				t.Fatalf("Condition() = %q, want %q", got.Condition(), codexrecovery.ConditionStateStoreUnavailable)
			}
		})
	}
}

func TestUnclassifiedAndInvalidMarkedConditionsUseInternalDefault(t *testing.T) {
	tests := []error{
		nil,
		errors.New("unclassified"),
		codexrecovery.Mark(codexrecovery.Condition("future_condition"), nil),
	}
	for index, root := range tests {
		t.Run(fmt.Sprintf("case_%d", index), func(t *testing.T) {
			assertDecision(t, codexrecovery.Classify(root, codexrecovery.PhaseHTTP), codexrecovery.PhaseHTTP, frozenContracts[len(frozenContracts)-1])
		})
	}
}

func TestClassifyWithFallbackOnlyAppliesWithoutTypedEvidence(t *testing.T) {
	t.Parallel()

	fallback := codexrecovery.ConditionProtocolInvalid
	tests := []struct {
		name string
		root error
		want codexrecovery.Condition
	}{
		{name: "unclassified", root: errors.New("adapter failure"), want: fallback},
		{
			name: "marked condition wins",
			root: codexrecovery.Mark(codexrecovery.ConditionReconnectRequired, errors.New("adapter failure")),
			want: codexrecovery.ConditionReconnectRequired,
		},
		{
			name: "marked internal remains internal",
			root: codexrecovery.Mark(codexrecovery.ConditionInternalFailure, errors.New("adapter failure")),
			want: codexrecovery.ConditionInternalFailure,
		},
		{
			name: "typed continuity wins",
			root: &codexcontinuity.Error{Kind: codexcontinuity.ErrorUnavailable},
			want: codexrecovery.ConditionStateStoreUnavailable,
		},
		{
			name: "future typed continuity remains internal",
			root: &codexcontinuity.Error{Kind: codexcontinuity.ErrorKind("future_kind")},
			want: codexrecovery.ConditionInternalFailure,
		},
		{
			name: "typed cookie wins",
			root: &providercookie.PersistenceError{
				Kind: providercookie.PersistenceUnavailable, Operation: "test",
			},
			want: codexrecovery.ConditionStateStoreUnavailable,
		},
		{
			name: "future typed cookie remains internal",
			root: &providercookie.PersistenceError{
				Kind: providercookie.PersistenceErrorKind("future_kind"), Operation: "test",
			},
			want: codexrecovery.ConditionInternalFailure,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := codexrecovery.ClassifyWithFallback(
				test.root, codexrecovery.PhaseWebSocketAccepted, fallback,
			)
			if got.Condition() != test.want {
				t.Fatalf("Condition() = %q, want %q", got.Condition(), test.want)
			}
		})
	}
}

func TestRecoveryErrorAccessorsAreNilSafe(t *testing.T) {
	var recoveryError *codexrecovery.Error
	if got := recoveryError.Error(); got != "codex recovery failure" {
		t.Fatalf("Error() = %q", got)
	}
	if recoveryError.Unwrap() != nil {
		t.Fatal("Unwrap() on nil recovery error must return nil")
	}
	if got := recoveryError.Condition(); got != codexrecovery.ConditionInternalFailure {
		t.Fatalf("Condition() = %q, want %q", got, codexrecovery.ConditionInternalFailure)
	}
}

func assertDecision(t *testing.T, got codexrecovery.Decision, phase codexrecovery.CarrierPhase, want expectedContract) {
	t.Helper()
	if got.Phase() != phase {
		t.Errorf("Phase() = %q, want %q", got.Phase(), phase)
	}
	if got.Condition() != want.condition {
		t.Errorf("Condition() = %q, want %q", got.Condition(), want.condition)
	}
	if got.HTTPStatus() != want.status {
		t.Errorf("HTTPStatus() = %d, want %d", got.HTTPStatus(), want.status)
	}
	if got.ErrorCode() != want.code {
		t.Errorf("ErrorCode() = %q, want %q", got.ErrorCode(), want.code)
	}
	if got.WebSocketCloseCode() != want.closeCode {
		t.Errorf("WebSocketCloseCode() = %d, want %d", got.WebSocketCloseCode(), want.closeCode)
	}
	if got.RecoveryAction() != want.action {
		t.Errorf("RecoveryAction() = %q, want %q", got.RecoveryAction(), want.action)
	}
}

func contractByCondition(t *testing.T, condition codexrecovery.Condition) expectedContract {
	t.Helper()
	for _, contract := range frozenContracts {
		if contract.condition == condition {
			return contract
		}
	}
	t.Fatalf("frozen contract for %q is missing", condition)
	return expectedContract{}
}
