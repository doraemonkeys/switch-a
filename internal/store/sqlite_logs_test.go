package store

import (
	"context"
	"testing"
	"time"

	"switch-a/internal/model"
)

func TestRequestLogs(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	// Insert logs
	for i := 0; i < 5; i++ {
		log := &model.RequestLog{
			ProviderID: "p1",
			APIType:    "claude",
			Model:      "claude-3",
			ClientIP:   "127.0.0.1",
			StatusCode: 200,
			Success:    true,
			CreatedAt:  time.Now(),
		}
		if err := store.InsertLog(ctx, log); err != nil {
			t.Fatalf("InsertLog failed: %v", err)
		}
	}

	// List logs with pagination
	logs, err := store.ListLogs(ctx, model.LogFilter{Limit: 3, Offset: 0})
	if err != nil {
		t.Fatalf("ListLogs failed: %v", err)
	}
	if len(logs) != 3 {
		t.Errorf("ListLogs len = %d, want 3", len(logs))
	}

	// List second page
	logs, err = store.ListLogs(ctx, model.LogFilter{Limit: 3, Offset: 3})
	if err != nil {
		t.Fatalf("ListLogs page 2 failed: %v", err)
	}
	if len(logs) != 2 {
		t.Errorf("ListLogs page 2 len = %d, want 2", len(logs))
	}

	// Clean old logs (none should be deleted as they're recent)
	if err := store.CleanOldLogs(ctx, 1); err != nil {
		t.Fatalf("CleanOldLogs failed: %v", err)
	}

	logs, err = store.ListLogs(ctx, model.LogFilter{Limit: 10, Offset: 0})
	if err != nil {
		t.Fatalf("ListLogs after clean failed: %v", err)
	}
	if len(logs) != 5 {
		t.Errorf("ListLogs after clean len = %d, want 5", len(logs))
	}
}

func TestCountLogs(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	// Initially should be empty
	count, err := store.CountLogs(ctx, model.LogFilter{})
	if err != nil {
		t.Fatalf("CountLogs failed: %v", err)
	}
	if count != 0 {
		t.Errorf("CountLogs = %d, want 0", count)
	}

	// Insert logs
	for i := 0; i < 5; i++ {
		log := &model.RequestLog{
			ProviderID: "p1",
			APIType:    "claude",
			Model:      "claude-3",
			CreatedAt:  time.Now(),
		}
		if err := store.InsertLog(ctx, log); err != nil {
			t.Fatalf("InsertLog failed: %v", err)
		}
	}

	// Count should be 5
	count, err = store.CountLogs(ctx, model.LogFilter{})
	if err != nil {
		t.Fatalf("CountLogs after insert failed: %v", err)
	}
	if count != 5 {
		t.Errorf("CountLogs = %d, want 5", count)
	}
}

func TestCleanOldLogs(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	// Insert old log
	oldLog := &model.RequestLog{
		ProviderID: "p1",
		APIType:    "claude",
		CreatedAt:  time.Now().AddDate(0, 0, -10), // 10 days ago
	}
	if err := store.InsertLog(ctx, oldLog); err != nil {
		t.Fatalf("InsertLog failed: %v", err)
	}

	// Insert recent log
	recentLog := &model.RequestLog{
		ProviderID: "p1",
		APIType:    "claude",
		CreatedAt:  time.Now(),
	}
	if err := store.InsertLog(ctx, recentLog); err != nil {
		t.Fatalf("InsertLog failed: %v", err)
	}

	// Clean logs older than 7 days
	if err := store.CleanOldLogs(ctx, 7); err != nil {
		t.Fatalf("CleanOldLogs failed: %v", err)
	}

	// Verify only recent log remains
	logs, err := store.ListLogs(ctx, model.LogFilter{Limit: 10, Offset: 0})
	if err != nil {
		t.Fatalf("ListLogs failed: %v", err)
	}
	if len(logs) != 1 {
		t.Errorf("expected 1 log after clean, got %d", len(logs))
	}
}

func TestListLogs_FilterByProviderID(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	// Insert logs for different providers
	logs := []model.RequestLog{
		{ProviderID: "p1", APIType: "claude", Success: true, CreatedAt: time.Now()},
		{ProviderID: "p1", APIType: "claude", Success: true, CreatedAt: time.Now()},
		{ProviderID: "p2", APIType: "codex", Success: false, CreatedAt: time.Now()},
	}
	for i := range logs {
		if err := store.InsertLog(ctx, &logs[i]); err != nil {
			t.Fatalf("InsertLog failed: %v", err)
		}
	}

	// Filter by provider_id
	result, err := store.ListLogs(ctx, model.LogFilter{ProviderID: "p1", Limit: 10})
	if err != nil {
		t.Fatalf("ListLogs failed: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("expected 2 logs for p1, got %d", len(result))
	}
	for _, log := range result {
		if log.ProviderID != "p1" {
			t.Errorf("expected provider_id=p1, got %s", log.ProviderID)
		}
	}

	// Count with filter
	count, err := store.CountLogs(ctx, model.LogFilter{ProviderID: "p1"})
	if err != nil {
		t.Fatalf("CountLogs failed: %v", err)
	}
	if count != 2 {
		t.Errorf("expected count=2 for p1, got %d", count)
	}
}

func TestListLogs_FilterByAPIType(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	logs := []model.RequestLog{
		{ProviderID: "p1", APIType: "claude", CreatedAt: time.Now()},
		{ProviderID: "p2", APIType: "codex", CreatedAt: time.Now()},
		{ProviderID: "p3", APIType: "claude", CreatedAt: time.Now()},
	}
	for i := range logs {
		if err := store.InsertLog(ctx, &logs[i]); err != nil {
			t.Fatalf("InsertLog failed: %v", err)
		}
	}

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

	logs := []model.RequestLog{
		{ProviderID: "p1", Success: true, CreatedAt: time.Now()},
		{ProviderID: "p2", Success: false, CreatedAt: time.Now()},
		{ProviderID: "p3", Success: true, CreatedAt: time.Now()},
	}
	for i := range logs {
		if err := store.InsertLog(ctx, &logs[i]); err != nil {
			t.Fatalf("InsertLog failed: %v", err)
		}
	}

	// Filter by success=true
	successTrue := true
	result, err := store.ListLogs(ctx, model.LogFilter{Success: &successTrue, Limit: 10})
	if err != nil {
		t.Fatalf("ListLogs failed: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("expected 2 successful logs, got %d", len(result))
	}

	// Filter by success=false
	successFalse := false
	result, err = store.ListLogs(ctx, model.LogFilter{Success: &successFalse, Limit: 10})
	if err != nil {
		t.Fatalf("ListLogs failed: %v", err)
	}
	if len(result) != 1 {
		t.Errorf("expected 1 failed log, got %d", len(result))
	}
}

func TestListLogs_FilterByUserID(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	logs := []model.RequestLog{
		{ProviderID: "p1", UserID: "user1", CreatedAt: time.Now()},
		{ProviderID: "p2", UserID: "user2", CreatedAt: time.Now()},
		{ProviderID: "p3", UserID: "user1", CreatedAt: time.Now()},
	}
	for i := range logs {
		if err := store.InsertLog(ctx, &logs[i]); err != nil {
			t.Fatalf("InsertLog failed: %v", err)
		}
	}

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
	logs := []model.RequestLog{
		{ProviderID: "p1", CreatedAt: now.Add(-3 * time.Hour)},
		{ProviderID: "p2", CreatedAt: now.Add(-1 * time.Hour)},
		{ProviderID: "p3", CreatedAt: now},
	}
	for i := range logs {
		if err := store.InsertLog(ctx, &logs[i]); err != nil {
			t.Fatalf("InsertLog failed: %v", err)
		}
	}

	// Filter by start_time (last 2 hours)
	startTime := now.Add(-2 * time.Hour)
	result, err := store.ListLogs(ctx, model.LogFilter{StartTime: &startTime, Limit: 10})
	if err != nil {
		t.Fatalf("ListLogs failed: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("expected 2 logs in last 2 hours, got %d", len(result))
	}

	// Filter by end_time
	endTime := now.Add(-30 * time.Minute)
	result, err = store.ListLogs(ctx, model.LogFilter{EndTime: &endTime, Limit: 10})
	if err != nil {
		t.Fatalf("ListLogs failed: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("expected 2 logs before 30 min ago, got %d", len(result))
	}

	// Filter by both start_time and end_time
	startTime = now.Add(-2 * time.Hour)
	endTime = now.Add(-30 * time.Minute)
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

	logs := []model.RequestLog{
		{ProviderID: "p1", LatencyMs: 100, CreatedAt: time.Now()},
		{ProviderID: "p2", LatencyMs: 500, CreatedAt: time.Now()},
		{ProviderID: "p3", LatencyMs: 1500, CreatedAt: time.Now()},
	}
	for i := range logs {
		if err := store.InsertLog(ctx, &logs[i]); err != nil {
			t.Fatalf("InsertLog failed: %v", err)
		}
	}

	// Filter by min_latency=500
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

	logs := []model.RequestLog{
		{ProviderID: "p1", LatencyMs: 100, CreatedAt: time.Now()},
		{ProviderID: "p2", LatencyMs: 500, CreatedAt: time.Now()},
		{ProviderID: "p3", LatencyMs: 200, CreatedAt: time.Now()},
	}
	for i := range logs {
		if err := store.InsertLog(ctx, &logs[i]); err != nil {
			t.Fatalf("InsertLog failed: %v", err)
		}
	}

	// Sort by latency descending (find slowest requests)
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

	// Sort by latency ascending
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
	logs := []model.RequestLog{
		{ProviderID: "p1", APIType: "claude", Success: true, LatencyMs: 100, UserID: "user1", CreatedAt: now},
		{ProviderID: "p1", APIType: "claude", Success: false, LatencyMs: 500, UserID: "user1", CreatedAt: now},
		{ProviderID: "p1", APIType: "codex", Success: true, LatencyMs: 200, UserID: "user1", CreatedAt: now},
		{ProviderID: "p2", APIType: "claude", Success: true, LatencyMs: 300, UserID: "user2", CreatedAt: now},
	}
	for i := range logs {
		if err := store.InsertLog(ctx, &logs[i]); err != nil {
			t.Fatalf("InsertLog failed: %v", err)
		}
	}

	// Combine multiple filters: provider=p1, api_type=claude, success=true
	successTrue := true
	result, err := store.ListLogs(ctx, model.LogFilter{
		ProviderID: "p1",
		APIType:    "claude",
		Success:    &successTrue,
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

	// Insert some logs
	log := &model.RequestLog{ProviderID: "p1", APIType: "claude", CreatedAt: time.Now()}
	if err := store.InsertLog(ctx, log); err != nil {
		t.Fatalf("InsertLog failed: %v", err)
	}

	// Query with filter that matches nothing
	result, err := store.ListLogs(ctx, model.LogFilter{ProviderID: "nonexistent", Limit: 10})
	if err != nil {
		t.Fatalf("ListLogs failed: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected 0 logs, got %d", len(result))
	}

	// Count should also be 0
	count, err := store.CountLogs(ctx, model.LogFilter{ProviderID: "nonexistent"})
	if err != nil {
		t.Fatalf("CountLogs failed: %v", err)
	}
	if count != 0 {
		t.Errorf("expected count=0, got %d", count)
	}
}
