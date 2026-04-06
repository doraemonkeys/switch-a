package store

import (
	"context"
	"testing"
	"time"

	"switch-a/internal/model"
)

func TestGetLogByID_PreservesLifecycleFields(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	committed := true
	log := &model.RequestLog{
		ProviderID:                "p1",
		APIType:                   "claude",
		SemanticsVersion:          model.RequestSemanticsVersionNormalizedV1,
		ClientTransportStatusCode: intPtr(101),
		CompletionState:           completionStatePtr(model.CompletionStateCompleted),
		ServiceOutcome:            serviceOutcomePtr(model.ServiceOutcomeCompleted),
		ClientAction:              clientActionPtr(model.ClientActionNone),
		IsWebSocket:               true,
		IsSticky:                  true,
		SessionCommitted:          &committed,
		ClientVisible:             boolPtr(true),
		CommitSource:              commitSourcePtr(model.CommitSemantic),
		CreatedAt:                 time.Now(),
	}
	if err := store.InsertLog(ctx, log); err != nil {
		t.Fatalf("InsertLog failed: %v", err)
	}

	found, err := store.GetLogByID(ctx, log.ID)
	if err != nil {
		t.Fatalf("GetLogByID failed: %v", err)
	}
	if found.SessionCommitted == nil || !*found.SessionCommitted {
		t.Fatalf("session_committed = %v, want true", found.SessionCommitted)
	}
	if found.ClientVisible == nil || !*found.ClientVisible {
		t.Fatalf("client_visible = %v, want true", found.ClientVisible)
	}
	if found.CommitSource == nil || *found.CommitSource != model.CommitSemantic {
		t.Fatalf("commit_source = %v, want %q", found.CommitSource, model.CommitSemantic)
	}
	if found.SemanticsVersion != model.RequestSemanticsVersionNormalizedV1 {
		t.Fatalf("semantics_version = %q, want %q", found.SemanticsVersion, model.RequestSemanticsVersionNormalizedV1)
	}
	if found.ClientTransportStatusCode == nil || *found.ClientTransportStatusCode != 101 {
		t.Fatalf("client_transport_status_code = %v, want 101", found.ClientTransportStatusCode)
	}
	if found.CompletionState == nil || *found.CompletionState != model.CompletionStateCompleted {
		t.Fatalf("completion_state = %v, want %q", found.CompletionState, model.CompletionStateCompleted)
	}
	if found.ServiceOutcome == nil || *found.ServiceOutcome != model.ServiceOutcomeCompleted {
		t.Fatalf("service_outcome = %v, want %q", found.ServiceOutcome, model.ServiceOutcomeCompleted)
	}
	if found.ClientAction == nil || *found.ClientAction != model.ClientActionNone {
		t.Fatalf("client_action = %v, want %q", found.ClientAction, model.ClientActionNone)
	}

	regular := &model.RequestLog{
		ProviderID:       "p2",
		APIType:          "claude",
		SemanticsVersion: model.RequestSemanticsVersionLegacyPreAssessment,
		CreatedAt:        time.Now(),
	}
	if err := store.InsertLog(ctx, regular); err != nil {
		t.Fatalf("InsertLog regular failed: %v", err)
	}

	regularFound, err := store.GetLogByID(ctx, regular.ID)
	if err != nil {
		t.Fatalf("GetLogByID regular failed: %v", err)
	}
	if regularFound.SessionCommitted != nil {
		t.Fatalf("regular session_committed = %v, want nil", regularFound.SessionCommitted)
	}
	if regularFound.ClientVisible != nil {
		t.Fatalf("regular client_visible = %v, want nil", regularFound.ClientVisible)
	}
	if regularFound.CommitSource != nil {
		t.Fatalf("regular commit_source = %v, want nil", regularFound.CommitSource)
	}
	if regularFound.ClientTransportStatusCode != nil {
		t.Fatalf("regular client_transport_status_code = %v, want nil", regularFound.ClientTransportStatusCode)
	}
	if regularFound.CompletionState != nil {
		t.Fatalf("regular completion_state = %v, want nil", regularFound.CompletionState)
	}
	if regularFound.ServiceOutcome != nil {
		t.Fatalf("regular service_outcome = %v, want nil", regularFound.ServiceOutcome)
	}
	if regularFound.TerminationActor != nil {
		t.Fatalf("regular termination_actor = %v, want nil", regularFound.TerminationActor)
	}
	if regularFound.TerminationReason != nil {
		t.Fatalf("regular termination_reason = %v, want nil", regularFound.TerminationReason)
	}
	if regularFound.ClientAction != nil {
		t.Fatalf("regular client_action = %v, want nil", regularFound.ClientAction)
	}
	if regularFound.SemanticsVersion != model.RequestSemanticsVersionLegacyPreAssessment {
		t.Fatalf("regular semantics_version = %q, want %q", regularFound.SemanticsVersion, model.RequestSemanticsVersionLegacyPreAssessment)
	}
}

func TestListLogs_FilterByLifecycleFields(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	committed := true
	uncommitted := false
	logs := []model.RequestLog{
		{
			ProviderID:                "p1",
			SemanticsVersion:          model.RequestSemanticsVersionNormalizedV1,
			ClientTransportStatusCode: intPtr(101),
			CompletionState:           completionStatePtr(model.CompletionStateCompleted),
			ServiceOutcome:            serviceOutcomePtr(model.ServiceOutcomeCompleted),
			ClientAction:              clientActionPtr(model.ClientActionNone),
			IsWebSocket:               true,
			SessionCommitted:          &committed,
			ClientVisible:             boolPtr(true),
			CommitSource:              commitSourcePtr(model.CommitSemantic),
			CreatedAt:                 time.Now(),
		},
		{
			ProviderID:                "p2",
			SemanticsVersion:          model.RequestSemanticsVersionNormalizedV1,
			ClientTransportStatusCode: intPtr(101),
			CompletionState:           completionStatePtr(model.CompletionStateIncomplete),
			ServiceOutcome:            serviceOutcomePtr(model.ServiceOutcomeInterrupted),
			TerminationActor:          terminationActorPtr(model.TerminationActorUpstream),
			TerminationReason:         terminationReasonPtr(model.TerminationReasonUsageLimitReached),
			ClientAction:              clientActionPtr(model.ClientActionReconnectRequired),
			IsWebSocket:               true,
			SessionCommitted:          &uncommitted,
			ClientVisible:             boolPtr(true),
			CommitSource:              commitSourcePtr(model.CommitUpstreamMessage),
			CreatedAt:                 time.Now(),
		},
		{
			ProviderID:                "p3",
			SemanticsVersion:          model.RequestSemanticsVersionNormalizedV1,
			ClientTransportStatusCode: intPtr(200),
			CompletionState:           completionStatePtr(model.CompletionStateCompleted),
			ServiceOutcome:            serviceOutcomePtr(model.ServiceOutcomeAbandonedByClient),
			TerminationActor:          terminationActorPtr(model.TerminationActorClient),
			TerminationReason:         terminationReasonPtr(model.TerminationReasonClientDisconnect),
			ClientAction:              clientActionPtr(model.ClientActionNone),
			IsWebSocket:               true,
			SessionCommitted:          &committed,
			ClientVisible:             boolPtr(true),
			CommitSource:              commitSourcePtr(model.CommitSemantic),
			CreatedAt:                 time.Now(),
		},
		{
			ProviderID:                "p4",
			SemanticsVersion:          model.RequestSemanticsVersionNormalizedV1,
			ClientTransportStatusCode: intPtr(503),
			CompletionState:           completionStatePtr(model.CompletionStateIncomplete),
			ServiceOutcome:            serviceOutcomePtr(model.ServiceOutcomeNeverStarted),
			TerminationActor:          terminationActorPtr(model.TerminationActorGateway),
			TerminationReason:         terminationReasonPtr(model.TerminationReasonProviderUnavailable),
			ClientAction:              clientActionPtr(model.ClientActionTransparentRetry),
			IsWebSocket:               true,
			SessionCommitted:          &uncommitted,
			ClientVisible:             boolPtr(false),
			CommitSource:              commitSourcePtr(model.CommitUnknown),
			CreatedAt:                 time.Now(),
		},
		{
			ProviderID:       "http-legacy",
			SemanticsVersion: model.RequestSemanticsVersionLegacyPreAssessment,
			CreatedAt:        time.Now(),
		},
	}
	for i := range logs {
		if err := store.InsertLog(ctx, &logs[i]); err != nil {
			t.Fatalf("InsertLog failed: %v", err)
		}
	}

	tests := []struct {
		name          string
		filter        model.LogFilter
		expectedCount int
	}{
		{
			name:          "filter by session_committed true",
			filter:        model.LogFilter{SessionCommitted: &committed, Limit: 10},
			expectedCount: 2,
		},
		{
			name:          "filter by session_committed false",
			filter:        model.LogFilter{SessionCommitted: &uncommitted, Limit: 10},
			expectedCount: 2,
		},
		{
			name:          "filter by client_visible true",
			filter:        model.LogFilter{ClientVisible: &committed, Limit: 10},
			expectedCount: 3,
		},
		{
			name:          "filter by client_visible false",
			filter:        model.LogFilter{ClientVisible: &uncommitted, Limit: 10},
			expectedCount: 1,
		},
		{
			name:          "filter by semantics_version normalized",
			filter:        model.LogFilter{SemanticsVersion: model.RequestSemanticsVersionNormalizedV1, Limit: 10},
			expectedCount: 4,
		},
		{
			name:          "filter by semantics_version legacy",
			filter:        model.LogFilter{SemanticsVersion: model.RequestSemanticsVersionLegacyPreAssessment, Limit: 10},
			expectedCount: 1,
		},
		{
			name:          "filter by completion_state",
			filter:        model.LogFilter{CompletionState: model.CompletionStateCompleted, Limit: 10},
			expectedCount: 2,
		},
		{
			name:          "filter by service_outcome",
			filter:        model.LogFilter{ServiceOutcome: model.ServiceOutcomeInterrupted, Limit: 10},
			expectedCount: 1,
		},
		{
			name:          "filter by client_action",
			filter:        model.LogFilter{ClientAction: model.ClientActionReconnectRequired, Limit: 10},
			expectedCount: 1,
		},
		{
			name:          "filter by termination_actor",
			filter:        model.LogFilter{TerminationActor: model.TerminationActorGateway, Limit: 10},
			expectedCount: 1,
		},
		{
			name:          "filter by termination_reason",
			filter:        model.LogFilter{TerminationReason: model.TerminationReasonClientDisconnect, Limit: 10},
			expectedCount: 1,
		},
		{
			name:          "filter by client_transport_status_code",
			filter:        model.LogFilter{ClientTransportStatusCode: intPtr(503), Limit: 10},
			expectedCount: 1,
		},
		{
			name:          "filter by commit_source",
			filter:        model.LogFilter{CommitSource: model.CommitUpstreamMessage, Limit: 10},
			expectedCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := store.ListLogs(ctx, tt.filter)
			if err != nil {
				t.Fatalf("ListLogs failed: %v", err)
			}
			if len(result) != tt.expectedCount {
				t.Fatalf("expected %d logs, got %d", tt.expectedCount, len(result))
			}

			count, err := store.CountLogs(ctx, tt.filter)
			if err != nil {
				t.Fatalf("CountLogs failed: %v", err)
			}
			if count != int64(tt.expectedCount) {
				t.Fatalf("expected count %d, got %d", tt.expectedCount, count)
			}
		})
	}
}

func TestListLogs_FilterByProviderUsesRequestLogLifecycleAttribution(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	committed := true
	log := &model.RequestLog{
		RequestID:                 "req-ws-attribution",
		ProviderID:                "provider-final",
		APIType:                   "codex",
		SemanticsVersion:          model.RequestSemanticsVersionNormalizedV1,
		ClientTransportStatusCode: intPtr(101),
		CompletionState:           completionStatePtr(model.CompletionStateCompleted),
		ServiceOutcome:            serviceOutcomePtr(model.ServiceOutcomeCompleted),
		ClientAction:              clientActionPtr(model.ClientActionNone),
		IsWebSocket:               true,
		SessionCommitted:          &committed,
		ClientVisible:             boolPtr(true),
		CommitSource:              commitSourcePtr(model.CommitSemantic),
		CreatedAt:                 time.Now(),
	}
	if err := store.InsertLog(ctx, log); err != nil {
		t.Fatalf("InsertLog failed: %v", err)
	}

	attempts := []model.RequestAttempt{
		{
			RequestID:  log.RequestID,
			ProviderID: "provider-origin",
			Attempt:    0,
			StatusCode: 403,
			Error:      "provider-scoped semantic error",
			CreatedAt:  time.Now(),
		},
		{
			RequestID:  log.RequestID,
			ProviderID: "provider-final",
			Attempt:    1,
			StatusCode: 101,
			CreatedAt:  time.Now().Add(time.Millisecond),
		},
	}
	if err := store.InsertAttempts(ctx, attempts); err != nil {
		t.Fatalf("InsertAttempts failed: %v", err)
	}

	finalLogs, err := store.ListLogs(ctx, model.LogFilter{ProviderID: "provider-final", Limit: 10})
	if err != nil {
		t.Fatalf("ListLogs final provider failed: %v", err)
	}
	if len(finalLogs) != 1 {
		t.Fatalf("expected 1 final-provider log, got %d", len(finalLogs))
	}

	originLogs, err := store.ListLogs(ctx, model.LogFilter{ProviderID: "provider-origin", Limit: 10})
	if err != nil {
		t.Fatalf("ListLogs origin provider failed: %v", err)
	}
	if len(originLogs) != 0 {
		t.Fatalf("expected 0 origin-provider logs, got %d", len(originLogs))
	}

	storedAttempts, err := store.GetAttemptsByRequestID(ctx, log.RequestID)
	if err != nil {
		t.Fatalf("GetAttemptsByRequestID failed: %v", err)
	}
	if len(storedAttempts) != 2 {
		t.Fatalf("expected 2 attempts, got %d", len(storedAttempts))
	}
	if storedAttempts[0].ProviderID != "provider-origin" || storedAttempts[1].ProviderID != "provider-final" {
		t.Fatalf("attempt provider order = [%s %s], want [provider-origin provider-final]", storedAttempts[0].ProviderID, storedAttempts[1].ProviderID)
	}
}
