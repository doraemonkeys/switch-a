package store

import (
	"context"
	"testing"
	"time"

	"switch-a/internal/model"
)

func insertLogFixtures(t *testing.T, store *SQLiteStore, ctx context.Context, logs []model.RequestLog) {
	t.Helper()

	for i := range logs {
		if err := store.InsertLog(ctx, &logs[i]); err != nil {
			t.Fatalf("InsertLog failed: %v", err)
		}
	}
}

func boolPtr(v bool) *bool {
	return &v
}

func terminalCausePtr(v model.TerminalCause) *model.TerminalCause {
	return &v
}

func commitSourcePtr(v model.CommitSource) *model.CommitSource {
	return &v
}

func recoveryActionPtr(v model.RecoveryAction) *model.RecoveryAction {
	return &v
}

func TestListLogs_FilterByProviderID(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	insertLogFixtures(t, store, ctx, []model.RequestLog{
		{ProviderID: "p1", APIType: "claude", Success: true, CreatedAt: time.Now()},
		{ProviderID: "p1", APIType: "claude", Success: true, CreatedAt: time.Now()},
		{ProviderID: "p2", APIType: "codex", Success: false, CreatedAt: time.Now()},
	})

	result, err := store.ListLogs(ctx, model.LogFilter{ProviderID: "p1", Limit: 10})
	if err != nil {
		t.Fatalf("ListLogs failed: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("expected 2 logs for p1, got %d", len(result))
	}

	count, err := store.CountLogs(ctx, model.LogFilter{ProviderID: "p1"})
	if err != nil {
		t.Fatalf("CountLogs failed: %v", err)
	}
	if count != 2 {
		t.Errorf("CountLogs = %d, want 2", count)
	}
}

func TestListLogs_FilterByAPIType(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	insertLogFixtures(t, store, ctx, []model.RequestLog{
		{ProviderID: "p1", APIType: "claude", CreatedAt: time.Now()},
		{ProviderID: "p2", APIType: "codex", CreatedAt: time.Now()},
		{ProviderID: "p3", APIType: "claude", CreatedAt: time.Now()},
	})

	result, err := store.ListLogs(ctx, model.LogFilter{APIType: "claude", Limit: 10})
	if err != nil {
		t.Fatalf("ListLogs failed: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("expected 2 logs for claude, got %d", len(result))
	}
}

func TestListLogs_FilterBySuccess(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	insertLogFixtures(t, store, ctx, []model.RequestLog{
		{ProviderID: "p1", Success: true, CreatedAt: time.Now()},
		{ProviderID: "p2", Success: false, CreatedAt: time.Now()},
		{ProviderID: "p3", Success: true, CreatedAt: time.Now()},
	})

	result, err := store.ListLogs(ctx, model.LogFilter{Success: boolPtr(true), Limit: 10})
	if err != nil {
		t.Fatalf("ListLogs failed: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("expected 2 successful logs, got %d", len(result))
	}

	result, err = store.ListLogs(ctx, model.LogFilter{Success: boolPtr(false), Limit: 10})
	if err != nil {
		t.Fatalf("ListLogs failed: %v", err)
	}
	if len(result) != 1 {
		t.Errorf("expected 1 failed log, got %d", len(result))
	}
}

func TestListLogs_FilterByIsSSE(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	insertLogFixtures(t, store, ctx, []model.RequestLog{
		{ProviderID: "p1", IsSSE: true, CreatedAt: time.Now()},
		{ProviderID: "p2", IsSSE: false, CreatedAt: time.Now()},
		{ProviderID: "p3", IsSSE: true, CreatedAt: time.Now()},
	})

	result, err := store.ListLogs(ctx, model.LogFilter{IsSSE: boolPtr(true), Limit: 10})
	if err != nil {
		t.Fatalf("ListLogs failed: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("expected 2 SSE logs, got %d", len(result))
	}

	result, err = store.ListLogs(ctx, model.LogFilter{IsSSE: boolPtr(false), Limit: 10})
	if err != nil {
		t.Fatalf("ListLogs failed: %v", err)
	}
	if len(result) != 1 {
		t.Errorf("expected 1 non-SSE log, got %d", len(result))
	}
}

func TestListLogs_FilterByIsWebSocket(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	insertLogFixtures(t, store, ctx, []model.RequestLog{
		{ProviderID: "p1", IsWebSocket: true, CreatedAt: time.Now()},
		{ProviderID: "p2", IsWebSocket: false, CreatedAt: time.Now()},
		{ProviderID: "p3", IsWebSocket: true, CreatedAt: time.Now()},
	})

	result, err := store.ListLogs(ctx, model.LogFilter{IsWebSocket: boolPtr(true), Limit: 10})
	if err != nil {
		t.Fatalf("ListLogs failed: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("expected 2 WebSocket logs, got %d", len(result))
	}

	result, err = store.ListLogs(ctx, model.LogFilter{IsWebSocket: boolPtr(false), Limit: 10})
	if err != nil {
		t.Fatalf("ListLogs failed: %v", err)
	}
	if len(result) != 1 {
		t.Errorf("expected 1 non-WebSocket log, got %d", len(result))
	}
}

func TestListLogs_FilterByUserID(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	insertLogFixtures(t, store, ctx, []model.RequestLog{
		{ProviderID: "p1", UserID: "user1", CreatedAt: time.Now()},
		{ProviderID: "p2", UserID: "user2", CreatedAt: time.Now()},
		{ProviderID: "p3", UserID: "user1", CreatedAt: time.Now()},
	})

	result, err := store.ListLogs(ctx, model.LogFilter{UserID: "user1", Limit: 10})
	if err != nil {
		t.Fatalf("ListLogs failed: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("expected 2 logs for user1, got %d", len(result))
	}
}

func TestListLogs_FilterByTimeRange(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	now := time.Now()
	insertLogFixtures(t, store, ctx, []model.RequestLog{
		{ProviderID: "p1", CreatedAt: now.Add(-3 * time.Hour)},
		{ProviderID: "p2", CreatedAt: now.Add(-2 * time.Hour)},
		{ProviderID: "p3", CreatedAt: now.Add(-1 * time.Hour)},
	})

	startTime := now.Add(-2*time.Hour - 30*time.Minute)
	result, err := store.ListLogs(ctx, model.LogFilter{StartTime: &startTime, Limit: 10})
	if err != nil {
		t.Fatalf("ListLogs failed: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("expected 2 logs after start_time, got %d", len(result))
	}

	endTime := now.Add(-1*time.Hour - 30*time.Minute)
	result, err = store.ListLogs(ctx, model.LogFilter{EndTime: &endTime, Limit: 10})
	if err != nil {
		t.Fatalf("ListLogs failed: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("expected 2 logs before end_time, got %d", len(result))
	}

	startTime = now.Add(-2*time.Hour - 30*time.Minute)
	endTime = now.Add(-1*time.Hour - 30*time.Minute)
	result, err = store.ListLogs(ctx, model.LogFilter{StartTime: &startTime, EndTime: &endTime, Limit: 10})
	if err != nil {
		t.Fatalf("ListLogs failed: %v", err)
	}
	if len(result) != 1 {
		t.Errorf("expected 1 log in time range, got %d", len(result))
	}
}

func TestListLogs_FilterByMinLatency(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	insertLogFixtures(t, store, ctx, []model.RequestLog{
		{ProviderID: "p1", LatencyMs: 100, CreatedAt: time.Now()},
		{ProviderID: "p2", LatencyMs: 500, CreatedAt: time.Now()},
		{ProviderID: "p3", LatencyMs: 1500, CreatedAt: time.Now()},
	})

	minLatency := int64(500)
	result, err := store.ListLogs(ctx, model.LogFilter{MinLatency: &minLatency, Limit: 10})
	if err != nil {
		t.Fatalf("ListLogs failed: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("expected 2 slow logs (>=500ms), got %d", len(result))
	}
}

func TestListLogs_SortByLatency(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	insertLogFixtures(t, store, ctx, []model.RequestLog{
		{ProviderID: "p1", LatencyMs: 100, CreatedAt: time.Now()},
		{ProviderID: "p2", LatencyMs: 500, CreatedAt: time.Now()},
		{ProviderID: "p3", LatencyMs: 200, CreatedAt: time.Now()},
	})

	result, err := store.ListLogs(ctx, model.LogFilter{SortBy: "latency_ms", SortOrder: "desc", Limit: 10})
	if err != nil {
		t.Fatalf("ListLogs failed: %v", err)
	}
	if len(result) != 3 {
		t.Fatalf("expected 3 logs, got %d", len(result))
	}
	if result[0].LatencyMs != 500 {
		t.Errorf("expected first log latency=500, got %d", result[0].LatencyMs)
	}
	if result[2].LatencyMs != 100 {
		t.Errorf("expected last log latency=100, got %d", result[2].LatencyMs)
	}

	result, err = store.ListLogs(ctx, model.LogFilter{SortBy: "latency_ms", SortOrder: "asc", Limit: 10})
	if err != nil {
		t.Fatalf("ListLogs failed: %v", err)
	}
	if result[0].LatencyMs != 100 {
		t.Errorf("expected first log latency=100 (asc), got %d", result[0].LatencyMs)
	}
}

func TestListLogs_MultipleFilters(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	now := time.Now()
	insertLogFixtures(t, store, ctx, []model.RequestLog{
		{ProviderID: "p1", APIType: "claude", Success: true, LatencyMs: 100, UserID: "user1", CreatedAt: now},
		{ProviderID: "p1", APIType: "claude", Success: false, LatencyMs: 500, UserID: "user1", CreatedAt: now},
		{ProviderID: "p1", APIType: "codex", Success: true, LatencyMs: 200, UserID: "user1", CreatedAt: now},
		{ProviderID: "p2", APIType: "claude", Success: true, LatencyMs: 300, UserID: "user2", CreatedAt: now},
	})

	result, err := store.ListLogs(ctx, model.LogFilter{
		ProviderID: "p1",
		APIType:    "claude",
		Success:    boolPtr(true),
		Limit:      10,
	})
	if err != nil {
		t.Fatalf("ListLogs failed: %v", err)
	}
	if len(result) != 1 {
		t.Errorf("expected 1 log matching all filters, got %d", len(result))
	}
}

func TestListLogs_EmptyResult(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	log := &model.RequestLog{ProviderID: "p1", APIType: "claude", CreatedAt: time.Now()}
	if err := store.InsertLog(ctx, log); err != nil {
		t.Fatalf("InsertLog failed: %v", err)
	}

	result, err := store.ListLogs(ctx, model.LogFilter{ProviderID: "nonexistent", Limit: 10})
	if err != nil {
		t.Fatalf("ListLogs failed: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected 0 logs, got %d", len(result))
	}

	count, err := store.CountLogs(ctx, model.LogFilter{ProviderID: "nonexistent"})
	if err != nil {
		t.Fatalf("CountLogs failed: %v", err)
	}
	if count != 0 {
		t.Errorf("expected count=0, got %d", count)
	}
}
