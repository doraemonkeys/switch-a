package store

import (
	"context"
	"testing"
	"time"

	"switch-a/internal/model"
)

func probeOutcomePtr(v model.WebSocketProbeOutcome) *model.WebSocketProbeOutcome {
	return &v
}

func TestGetLogByID_PreservesLifecycleFields(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	committed := true
	probeOutcome := model.WebSocketProbeOutcomeUnsupported
	log := &model.RequestLog{
		ProviderID:       "p1",
		APIType:          "claude",
		IsWebSocket:      true,
		Success:          true,
		IsSticky:         true,
		StickyWritten:    boolPtr(true),
		SessionCommitted: &committed,
		ClientVisible:    boolPtr(true),
		ProbeOutcome:     &probeOutcome,
		TerminalCause:    terminalCausePtr(model.TerminalCleanClose),
		CommitSource:     commitSourcePtr(model.CommitSemantic),
		RecoveryAction:   recoveryActionPtr(model.RecoveryActionTransparentRetry),
		CreatedAt:        time.Now(),
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
	if found.StickyWritten == nil || !*found.StickyWritten {
		t.Fatalf("sticky_written = %v, want true", found.StickyWritten)
	}
	if found.ClientVisible == nil || !*found.ClientVisible {
		t.Fatalf("client_visible = %v, want true", found.ClientVisible)
	}
	if found.ProbeOutcome == nil || *found.ProbeOutcome != model.WebSocketProbeOutcomeUnsupported {
		t.Fatalf("probe_outcome = %v, want %q", found.ProbeOutcome, model.WebSocketProbeOutcomeUnsupported)
	}
	if found.TerminalCause == nil || *found.TerminalCause != model.TerminalCleanClose {
		t.Fatalf("terminal_cause = %v, want %q", found.TerminalCause, model.TerminalCleanClose)
	}
	if found.CommitSource == nil || *found.CommitSource != model.CommitSemantic {
		t.Fatalf("commit_source = %v, want %q", found.CommitSource, model.CommitSemantic)
	}
	if found.RecoveryAction == nil || *found.RecoveryAction != model.RecoveryActionTransparentRetry {
		t.Fatalf("recovery_action = %v, want %q", found.RecoveryAction, model.RecoveryActionTransparentRetry)
	}

	regular := &model.RequestLog{
		ProviderID: "p2",
		APIType:    "claude",
		Success:    false,
		CreatedAt:  time.Now(),
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
	if regularFound.StickyWritten != nil {
		t.Fatalf("regular sticky_written = %v, want nil", regularFound.StickyWritten)
	}
	if regularFound.ClientVisible != nil {
		t.Fatalf("regular client_visible = %v, want nil", regularFound.ClientVisible)
	}
	if regularFound.ProbeOutcome != nil {
		t.Fatalf("regular probe_outcome = %v, want nil", regularFound.ProbeOutcome)
	}
	if regularFound.TerminalCause != nil {
		t.Fatalf("regular terminal_cause = %v, want nil", regularFound.TerminalCause)
	}
	if regularFound.CommitSource != nil {
		t.Fatalf("regular commit_source = %v, want nil", regularFound.CommitSource)
	}
	if regularFound.RecoveryAction != nil {
		t.Fatalf("regular recovery_action = %v, want nil", regularFound.RecoveryAction)
	}
}

func TestListLogs_FilterByLifecycleFields(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	committed := true
	uncommitted := false
	logs := []model.RequestLog{
		{
			ProviderID:       "p1",
			IsWebSocket:      true,
			StickyWritten:    boolPtr(true),
			SessionCommitted: &committed,
			ClientVisible:    boolPtr(true),
			ProbeOutcome:     probeOutcomePtr(model.WebSocketProbeOutcomeBypassed),
			TerminalCause:    terminalCausePtr(model.TerminalCleanClose),
			CommitSource:     commitSourcePtr(model.CommitSemantic),
			RecoveryAction:   recoveryActionPtr(model.RecoveryActionNone),
			CreatedAt:        time.Now(),
		},
		{
			ProviderID:       "p2",
			IsWebSocket:      true,
			StickyWritten:    boolPtr(false),
			SessionCommitted: &uncommitted,
			ClientVisible:    boolPtr(true),
			ProbeOutcome:     probeOutcomePtr(model.WebSocketProbeOutcomeTransportFailed),
			TerminalCause:    terminalCausePtr(model.TerminalUpstreamSemanticError),
			CommitSource:     commitSourcePtr(model.CommitUpstreamMessage),
			RecoveryAction:   recoveryActionPtr(model.RecoveryActionReconnectRequired),
			CreatedAt:        time.Now(),
		},
		{
			ProviderID:       "p3",
			IsWebSocket:      true,
			StickyWritten:    boolPtr(false),
			SessionCommitted: &committed,
			ClientVisible:    boolPtr(true),
			ProbeOutcome:     probeOutcomePtr(model.WebSocketProbeOutcomeObservedUsableModel),
			TerminalCause:    terminalCausePtr(model.TerminalCleanClose),
			CommitSource:     commitSourcePtr(model.CommitSemantic),
			RecoveryAction:   recoveryActionPtr(model.RecoveryActionTransparentRetry),
			CreatedAt:        time.Now(),
		},
		{
			ProviderID:       "p4",
			IsWebSocket:      true,
			StickyWritten:    boolPtr(false),
			SessionCommitted: &uncommitted,
			ClientVisible:    boolPtr(false),
			ProbeOutcome:     probeOutcomePtr(model.WebSocketProbeOutcomeCompletedWithoutUsableModel),
			TerminalCause:    terminalCausePtr(model.TerminalUnknown),
			CommitSource:     commitSourcePtr(model.CommitUnknown),
			RecoveryAction:   recoveryActionPtr(model.RecoveryActionNone),
			CreatedAt:        time.Now(),
		},
		{
			ProviderID: "http-1",
			Success:    true,
			CreatedAt:  time.Now(),
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
			name:          "filter by sticky_written true",
			filter:        model.LogFilter{StickyWritten: &committed, Limit: 10},
			expectedCount: 1,
		},
		{
			name:          "filter by sticky_written false",
			filter:        model.LogFilter{StickyWritten: &uncommitted, Limit: 10},
			expectedCount: 3,
		},
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
			name:          "filter by terminal_cause",
			filter:        model.LogFilter{TerminalCause: model.TerminalUpstreamSemanticError, Limit: 10},
			expectedCount: 1,
		},
		{
			name:          "filter by commit_source",
			filter:        model.LogFilter{CommitSource: model.CommitUpstreamMessage, Limit: 10},
			expectedCount: 1,
		},
		{
			name:          "filter by probe_outcome",
			filter:        model.LogFilter{ProbeOutcome: model.WebSocketProbeOutcomeTransportFailed, Limit: 10},
			expectedCount: 1,
		},
		{
			name:          "filter by recovery_action reconnect_required",
			filter:        model.LogFilter{RecoveryAction: model.RecoveryActionReconnectRequired, Limit: 10},
			expectedCount: 1,
		},
		{
			name:          "filter by recovery_action transparent_retry",
			filter:        model.LogFilter{RecoveryAction: model.RecoveryActionTransparentRetry, Limit: 10},
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
		RequestID:        "req-ws-attribution",
		ProviderID:       "provider-final",
		APIType:          "codex",
		IsWebSocket:      true,
		Success:          true,
		SessionCommitted: &committed,
		ClientVisible:    boolPtr(true),
		TerminalCause:    terminalCausePtr(model.TerminalCleanClose),
		CommitSource:     commitSourcePtr(model.CommitSemantic),
		RecoveryAction:   recoveryActionPtr(model.RecoveryActionTransparentRetry),
		CreatedAt:        time.Now(),
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
