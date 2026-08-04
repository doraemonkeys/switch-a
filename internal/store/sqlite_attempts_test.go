package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"
)

func attemptBoolPtr(value bool) *bool {
	return &value
}

func phasePtr(value model.RequestAttemptPhase) *model.RequestAttemptPhase {
	return &value
}

func outcomePtr(value model.RequestAttemptOutcome) *model.RequestAttemptOutcome {
	return &value
}

func attemptInt64Ptr(value int64) *int64 {
	return &value
}

func TestInsertAttempts_EmptySlice(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	// Empty slice should not error
	err := store.InsertAttempts(ctx, []model.RequestAttempt{})
	if err != nil {
		t.Fatalf("InsertAttempts with empty slice should not error: %v", err)
	}

	// Nil slice should also not error
	err = store.InsertAttempts(ctx, nil)
	if err != nil {
		t.Fatalf("InsertAttempts with nil slice should not error: %v", err)
	}
}

func TestInsertAttempts_SingleAttempt(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	attempt := model.RequestAttempt{
		RequestID:  "req-123",
		ProviderID: "provider-1",
		Attempt:    1,
		StatusCode: 200,
		Error:      "",
		LatencyMs:  150,
		CreatedAt:  time.Now(),
	}

	err := store.InsertAttempts(ctx, []model.RequestAttempt{attempt})
	if err != nil {
		t.Fatalf("InsertAttempts failed: %v", err)
	}

	// Verify the attempt was inserted
	attempts, err := store.GetAttemptsByRequestID(ctx, "req-123")
	if err != nil {
		t.Fatalf("GetAttemptsByRequestID failed: %v", err)
	}
	if len(attempts) != 1 {
		t.Fatalf("expected 1 attempt, got %d", len(attempts))
	}
	if attempts[0].ProviderID != "provider-1" {
		t.Errorf("expected provider_id=provider-1, got %s", attempts[0].ProviderID)
	}
	if attempts[0].StatusCode != 200 {
		t.Errorf("expected status_code=200, got %d", attempts[0].StatusCode)
	}
}

func TestInsertAttempts_MultipleAttempts(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	now := time.Now()
	attempts := []model.RequestAttempt{
		{
			RequestID:  "req-456",
			ProviderID: "provider-1",
			Attempt:    1,
			StatusCode: 503,
			Error:      "service unavailable",
			LatencyMs:  100,
			CreatedAt:  now,
		},
		{
			RequestID:  "req-456",
			ProviderID: "provider-2",
			Attempt:    2,
			StatusCode: 429,
			Error:      "rate limited",
			LatencyMs:  50,
			CreatedAt:  now.Add(100 * time.Millisecond),
		},
		{
			RequestID:  "req-456",
			ProviderID: "provider-3",
			Attempt:    3,
			StatusCode: 200,
			Error:      "",
			LatencyMs:  200,
			CreatedAt:  now.Add(200 * time.Millisecond),
		},
	}

	err := store.InsertAttempts(ctx, attempts)
	if err != nil {
		t.Fatalf("InsertAttempts failed: %v", err)
	}

	// Verify all attempts were inserted
	result, err := store.GetAttemptsByRequestID(ctx, "req-456")
	if err != nil {
		t.Fatalf("GetAttemptsByRequestID failed: %v", err)
	}
	if len(result) != 3 {
		t.Fatalf("expected 3 attempts, got %d", len(result))
	}
}

func TestInsertAttempts_HeterogeneousDefaultFields(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	semanticOutcome := model.RequestAttemptOutcomeUpstreamSemanticError
	completedOutcome := model.RequestAttemptOutcomeUpstreamCompleted
	failureVerdict := model.RequestAttemptHealthFailure
	successVerdict := model.RequestAttemptHealthSuccess
	semanticCause := model.RequestAttemptHealthCauseSemanticRetryThenSwitch
	successCause := model.RequestAttemptHealthCauseNormalCompletion
	hidden, visible := false, true
	clientStatus := 200
	evidence := `{"v":2,"semantic_error":{"schema_version":1}}`
	attempts := []model.RequestAttempt{
		{
			RequestID:             "req-heterogeneous",
			ProviderID:            "provider-1",
			Attempt:               0,
			SwitchMode:            model.RequestAttemptSwitchModeInitial,
			Outcome:               &semanticOutcome,
			ResultVisibleToClient: &hidden,
			HealthVerdict:         &failureVerdict,
			HealthCause:           &semanticCause,
			AttemptEvidenceJSON:   &evidence,
			CreatedAt:             time.Now(),
		},
		{
			RequestID:                 "req-heterogeneous",
			ProviderID:                "provider-2",
			Attempt:                   1,
			SwitchMode:                model.RequestAttemptSwitchModeReplacement,
			Outcome:                   &completedOutcome,
			ResultVisibleToClient:     &visible,
			ClientTransportStatusCode: &clientStatus,
			HealthVerdict:             &successVerdict,
			HealthCause:               &successCause,
			CreatedAt:                 time.Now(),
		},
	}

	if err := store.InsertAttempts(ctx, attempts); err != nil {
		t.Fatalf("InsertAttempts failed: %v", err)
	}

	result, err := store.GetAttemptsByRequestID(ctx, "req-heterogeneous")
	if err != nil {
		t.Fatalf("GetAttemptsByRequestID failed: %v", err)
	}
	if len(result) != len(attempts) {
		t.Fatalf("expected %d attempts, got %d", len(attempts), len(result))
	}
	if result[0].AttemptEvidenceJSON == nil || *result[0].AttemptEvidenceJSON != evidence {
		t.Fatalf("first attempt evidence = %#v, want %q", result[0].AttemptEvidenceJSON, evidence)
	}
	if result[0].ClientTransportStatusCode != nil {
		t.Fatalf("first attempt client status = %#v, want nil", result[0].ClientTransportStatusCode)
	}
	if result[1].AttemptEvidenceJSON != nil {
		t.Fatalf("second attempt evidence = %#v, want nil", result[1].AttemptEvidenceJSON)
	}
	if result[1].ClientTransportStatusCode == nil || *result[1].ClientTransportStatusCode != clientStatus {
		t.Fatalf("second attempt client status = %#v, want %d", result[1].ClientTransportStatusCode, clientStatus)
	}
}

func TestInsertAttempts_RollsBackBatchWhenLaterRowFails(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	const duplicateAttemptID uint = 4242
	attempts := []model.RequestAttempt{
		{
			ID:         duplicateAttemptID,
			RequestID:  "req-batch-rollback",
			ProviderID: "provider-1",
			Attempt:    0,
			CreatedAt:  time.Now(),
		},
		{
			ID:         duplicateAttemptID,
			RequestID:  "req-batch-rollback",
			ProviderID: "provider-2",
			Attempt:    1,
			CreatedAt:  time.Now(),
		},
	}

	err := store.InsertAttempts(ctx, attempts)
	if err == nil {
		t.Fatal("InsertAttempts succeeded, want duplicate primary-key failure")
	}
	if !strings.Contains(err.Error(), `attempt row 2/2 for request "req-batch-rollback"`) {
		t.Fatalf("InsertAttempts error = %q, want failing row and request context", err)
	}

	result, getErr := store.GetAttemptsByRequestID(ctx, "req-batch-rollback")
	if getErr != nil {
		t.Fatalf("GetAttemptsByRequestID failed: %v", getErr)
	}
	if len(result) != 0 {
		t.Fatalf("persisted %d attempts after rollback, want 0", len(result))
	}
}

func TestInsertAttempts_PreservesWebSocketAttemptSemantics(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	now := time.Now()
	attempts := []model.RequestAttempt{
		{
			RequestID:             "req-ws",
			ProviderID:            "provider-1",
			Attempt:               0,
			StatusCode:            101,
			Error:                 "provider semantic error suppressed",
			Phase:                 phasePtr(model.RequestAttemptPhasePostUpgradePreVisible),
			Outcome:               outcomePtr(model.RequestAttemptOutcomeUpstreamSemanticError),
			ResultVisibleToClient: attemptBoolPtr(false),
			SwitchReason:          "provider_scoped_semantic_error",
			LatencyMs:             42,
			CreatedAt:             now,
		},
		{
			RequestID:             "req-ws",
			ProviderID:            "provider-2",
			Attempt:               1,
			StatusCode:            101,
			Error:                 "",
			Phase:                 phasePtr(model.RequestAttemptPhaseVisible),
			Outcome:               outcomePtr(model.RequestAttemptOutcomeVisibleSession),
			ResultVisibleToClient: attemptBoolPtr(true),
			LatencyMs:             84,
			CreatedAt:             now.Add(100 * time.Millisecond),
		},
	}

	if err := store.InsertAttempts(ctx, attempts); err != nil {
		t.Fatalf("InsertAttempts failed: %v", err)
	}

	result, err := store.GetAttemptsByRequestID(ctx, "req-ws")
	if err != nil {
		t.Fatalf("GetAttemptsByRequestID failed: %v", err)
	}
	if len(result) != len(attempts) {
		t.Fatalf("expected %d attempts, got %d", len(attempts), len(result))
	}

	if result[0].Phase == nil || *result[0].Phase != model.RequestAttemptPhasePostUpgradePreVisible {
		t.Fatalf("expected first attempt phase to round-trip, got %#v", result[0].Phase)
	}
	if result[0].Outcome == nil || *result[0].Outcome != model.RequestAttemptOutcomeUpstreamSemanticError {
		t.Fatalf("expected first attempt outcome to round-trip, got %#v", result[0].Outcome)
	}
	if result[0].ResultVisibleToClient == nil || *result[0].ResultVisibleToClient {
		t.Fatalf("expected first attempt to remain not visible to client, got %#v", result[0].ResultVisibleToClient)
	}
	if result[1].Phase == nil || *result[1].Phase != model.RequestAttemptPhaseVisible {
		t.Fatalf("expected second attempt phase to round-trip, got %#v", result[1].Phase)
	}
	if result[1].Outcome == nil || *result[1].Outcome != model.RequestAttemptOutcomeVisibleSession {
		t.Fatalf("expected second attempt outcome to round-trip, got %#v", result[1].Outcome)
	}
	if result[1].ResultVisibleToClient == nil || !*result[1].ResultVisibleToClient {
		t.Fatalf("expected second attempt to remain visible to client, got %#v", result[1].ResultVisibleToClient)
	}
}

func TestInsertAttempts_PersistsSwitchModeAndContinuityProvenance(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	const continuitySeedAgeMs int64 = 345
	attempt := model.RequestAttempt{
		RequestID:                  "req-semantics",
		ProviderID:                 "provider-b",
		Attempt:                    2,
		SwitchMode:                 model.RequestAttemptSwitchModeFailover,
		ProviderAttempt:            1,
		ProviderSwitchCount:        1,
		StatusCode:                 429,
		Error:                      "capacity exceeded",
		LatencyMs:                  95,
		SwitchReason:               model.RequestAttemptSwitchReasonProviderScopedSemanticError,
		ContinuitySeeded:           true,
		ContinuityOriginProviderID: "provider-a",
		ContinuitySeedAgeMs:        attemptInt64Ptr(continuitySeedAgeMs),
		CreatedAt:                  time.Now(),
	}

	if err := store.InsertAttempts(ctx, []model.RequestAttempt{attempt}); err != nil {
		t.Fatalf("InsertAttempts failed: %v", err)
	}

	result, err := store.GetAttemptsByRequestID(ctx, "req-semantics")
	if err != nil {
		t.Fatalf("GetAttemptsByRequestID failed: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 attempt, got %d", len(result))
	}
	if result[0].SwitchMode != model.RequestAttemptSwitchModeFailover {
		t.Fatalf("SwitchMode = %q, want %q", result[0].SwitchMode, model.RequestAttemptSwitchModeFailover)
	}
	if result[0].ProviderAttempt != 1 {
		t.Fatalf("ProviderAttempt = %d, want 1", result[0].ProviderAttempt)
	}
	if result[0].ProviderSwitchCount != 1 {
		t.Fatalf("ProviderSwitchCount = %d, want 1", result[0].ProviderSwitchCount)
	}
	if !result[0].ContinuitySeeded {
		t.Fatal("ContinuitySeeded = false, want true")
	}
	if result[0].ContinuityOriginProviderID != "provider-a" {
		t.Fatalf("ContinuityOriginProviderID = %q, want %q", result[0].ContinuityOriginProviderID, "provider-a")
	}
	if result[0].ContinuitySeedAgeMs == nil || *result[0].ContinuitySeedAgeMs != continuitySeedAgeMs {
		t.Fatalf("ContinuitySeedAgeMs = %#v, want %d", result[0].ContinuitySeedAgeMs, continuitySeedAgeMs)
	}
}

func TestGetAttemptsByRequestID_NotFound(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	// Query for a non-existent request ID should return empty slice, not error
	attempts, err := store.GetAttemptsByRequestID(ctx, "non-existent-id")
	if err != nil {
		t.Fatalf("GetAttemptsByRequestID should not error for non-existent ID: %v", err)
	}
	if len(attempts) != 0 {
		t.Errorf("expected empty slice for non-existent request, got %d attempts", len(attempts))
	}
}

func TestGetAttemptsByRequestID_Ordering(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	now := time.Now()
	// Insert attempts out of order to verify sorting
	attempts := []model.RequestAttempt{
		{
			RequestID:  "req-order",
			ProviderID: "provider-3",
			Attempt:    3,
			StatusCode: 200,
			CreatedAt:  now.Add(200 * time.Millisecond),
		},
		{
			RequestID:  "req-order",
			ProviderID: "provider-1",
			Attempt:    1,
			StatusCode: 503,
			CreatedAt:  now,
		},
		{
			RequestID:  "req-order",
			ProviderID: "provider-2",
			Attempt:    2,
			StatusCode: 429,
			CreatedAt:  now.Add(100 * time.Millisecond),
		},
	}

	err := store.InsertAttempts(ctx, attempts)
	if err != nil {
		t.Fatalf("InsertAttempts failed: %v", err)
	}

	// Verify attempts are returned ordered by attempt number ASC
	result, err := store.GetAttemptsByRequestID(ctx, "req-order")
	if err != nil {
		t.Fatalf("GetAttemptsByRequestID failed: %v", err)
	}
	if len(result) != 3 {
		t.Fatalf("expected 3 attempts, got %d", len(result))
	}

	// Check ordering
	for i, attempt := range result {
		expectedAttempt := i + 1
		if attempt.Attempt != expectedAttempt {
			t.Errorf("attempt[%d].Attempt = %d, want %d", i, attempt.Attempt, expectedAttempt)
		}
	}
}

func TestGetAttemptsByRequestID_StableOrderingOnAttemptTie(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	attempts := []model.RequestAttempt{
		{
			RequestID:  "req-tie",
			ProviderID: "provider-2",
			Attempt:    1,
			StatusCode: 403,
			CreatedAt:  time.Now(),
		},
		{
			RequestID:  "req-tie",
			ProviderID: "provider-1",
			Attempt:    1,
			StatusCode: 101,
			CreatedAt:  time.Now(),
		},
	}

	if err := store.InsertAttempts(ctx, attempts); err != nil {
		t.Fatalf("InsertAttempts failed: %v", err)
	}

	result, err := store.GetAttemptsByRequestID(ctx, "req-tie")
	if err != nil {
		t.Fatalf("GetAttemptsByRequestID failed: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 attempts, got %d", len(result))
	}
	if result[0].ProviderID != "provider-2" || result[1].ProviderID != "provider-1" {
		t.Fatalf("provider order = [%s %s], want [provider-2 provider-1]", result[0].ProviderID, result[1].ProviderID)
	}
}

func TestCleanOldAttempts_RemovesOldRecords(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	now := time.Now()
	oldTime := now.Add(-48 * time.Hour) // 2 days ago
	recentTime := now.Add(-1 * time.Hour)

	attempts := []model.RequestAttempt{
		{
			RequestID:  "req-old",
			ProviderID: "provider-1",
			Attempt:    1,
			StatusCode: 200,
			CreatedAt:  oldTime,
		},
		{
			RequestID:  "req-recent",
			ProviderID: "provider-1",
			Attempt:    1,
			StatusCode: 200,
			CreatedAt:  recentTime,
		},
	}

	err := store.InsertAttempts(ctx, attempts)
	if err != nil {
		t.Fatalf("InsertAttempts failed: %v", err)
	}

	// Clean attempts older than 24 hours
	cutoff := now.Add(-24 * time.Hour)
	deleted, err := store.CleanOldAttempts(ctx, cutoff)
	if err != nil {
		t.Fatalf("CleanOldAttempts failed: %v", err)
	}
	if deleted != 1 {
		t.Errorf("expected 1 deleted record, got %d", deleted)
	}

	// Verify old attempt was deleted
	oldAttempts, err := store.GetAttemptsByRequestID(ctx, "req-old")
	if err != nil {
		t.Fatalf("GetAttemptsByRequestID failed: %v", err)
	}
	if len(oldAttempts) != 0 {
		t.Errorf("expected old attempt to be deleted, but found %d", len(oldAttempts))
	}

	// Verify recent attempt was retained
	recentAttempts, err := store.GetAttemptsByRequestID(ctx, "req-recent")
	if err != nil {
		t.Fatalf("GetAttemptsByRequestID failed: %v", err)
	}
	if len(recentAttempts) != 1 {
		t.Errorf("expected recent attempt to be retained, found %d", len(recentAttempts))
	}
}

func TestCleanOldAttempts_RetainsRecent(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	now := time.Now()
	attempts := []model.RequestAttempt{
		{
			RequestID:  "req-keep-1",
			ProviderID: "provider-1",
			Attempt:    1,
			StatusCode: 200,
			CreatedAt:  now.Add(-1 * time.Hour),
		},
		{
			RequestID:  "req-keep-2",
			ProviderID: "provider-2",
			Attempt:    1,
			StatusCode: 200,
			CreatedAt:  now.Add(-2 * time.Hour),
		},
	}

	err := store.InsertAttempts(ctx, attempts)
	if err != nil {
		t.Fatalf("InsertAttempts failed: %v", err)
	}

	// Clean attempts older than 24 hours (none should be deleted)
	cutoff := now.Add(-24 * time.Hour)
	deleted, err := store.CleanOldAttempts(ctx, cutoff)
	if err != nil {
		t.Fatalf("CleanOldAttempts failed: %v", err)
	}
	if deleted != 0 {
		t.Errorf("expected 0 deleted records, got %d", deleted)
	}

	// Verify all attempts were retained
	attempts1, _ := store.GetAttemptsByRequestID(ctx, "req-keep-1")
	attempts2, _ := store.GetAttemptsByRequestID(ctx, "req-keep-2")
	if len(attempts1) != 1 || len(attempts2) != 1 {
		t.Error("expected all recent attempts to be retained")
	}
}

func TestCleanOldAttempts_ReturnsCorrectCount(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	now := time.Now()
	oldTime := now.Add(-48 * time.Hour)

	// Insert multiple old attempts
	attempts := []model.RequestAttempt{
		{RequestID: "req-1", ProviderID: "p1", Attempt: 1, StatusCode: 200, CreatedAt: oldTime},
		{RequestID: "req-1", ProviderID: "p2", Attempt: 2, StatusCode: 200, CreatedAt: oldTime},
		{RequestID: "req-1", ProviderID: "p3", Attempt: 3, StatusCode: 200, CreatedAt: oldTime},
		{RequestID: "req-2", ProviderID: "p1", Attempt: 1, StatusCode: 200, CreatedAt: now}, // Recent, should not be deleted
	}

	err := store.InsertAttempts(ctx, attempts)
	if err != nil {
		t.Fatalf("InsertAttempts failed: %v", err)
	}

	// Clean attempts older than 24 hours
	cutoff := now.Add(-24 * time.Hour)
	deleted, err := store.CleanOldAttempts(ctx, cutoff)
	if err != nil {
		t.Fatalf("CleanOldAttempts failed: %v", err)
	}
	if deleted != 3 {
		t.Errorf("expected 3 deleted records, got %d", deleted)
	}
}
