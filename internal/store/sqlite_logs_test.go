package store

import (
	"context"
	"errors"
	"strings"
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

func TestGetLogByID(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	// Test not found case
	_, err := store.GetLogByID(ctx, 99999)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}

	// Test found case
	log := &model.RequestLog{
		ProviderID: "p1",
		APIType:    "claude",
		CreatedAt:  time.Now(),
	}
	if err := store.InsertLog(ctx, log); err != nil {
		t.Fatalf("InsertLog failed: %v", err)
	}

	found, err := store.GetLogByID(ctx, log.ID)
	if err != nil {
		t.Fatalf("GetLogByID failed: %v", err)
	}
	if found.ID != log.ID {
		t.Errorf("expected ID %d, got %d", log.ID, found.ID)
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

func TestCleanOldLogs_CascadesAttemptsDelete(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	oldTime := time.Now().AddDate(0, 0, -10) // 10 days ago

	// Create an old log with a request_id
	oldLog := &model.RequestLog{
		RequestID:  "old-req-123",
		ProviderID: "p1",
		APIType:    "claude",
		CreatedAt:  oldTime,
	}
	if err := store.InsertLog(ctx, oldLog); err != nil {
		t.Fatalf("InsertLog failed: %v", err)
	}

	// Create attempts associated with the old log
	attempts := []model.RequestAttempt{
		{
			RequestID:  "old-req-123",
			ProviderID: "p1",
			Attempt:    1,
			StatusCode: 503,
			CreatedAt:  oldTime,
		},
		{
			RequestID:  "old-req-123",
			ProviderID: "p2",
			Attempt:    2,
			StatusCode: 200,
			CreatedAt:  oldTime,
		},
	}
	if err := store.InsertAttempts(ctx, attempts); err != nil {
		t.Fatalf("InsertAttempts failed: %v", err)
	}

	// Verify attempts exist before cleanup
	attemptsBefore, err := store.GetAttemptsByRequestID(ctx, "old-req-123")
	if err != nil {
		t.Fatalf("GetAttemptsByRequestID failed: %v", err)
	}
	if len(attemptsBefore) != 2 {
		t.Fatalf("expected 2 attempts before cleanup, got %d", len(attemptsBefore))
	}

	// Clean logs older than 7 days (should cascade delete attempts)
	if err := store.CleanOldLogs(ctx, 7); err != nil {
		t.Fatalf("CleanOldLogs failed: %v", err)
	}

	// Verify log was deleted
	logs, err := store.ListLogs(ctx, model.LogFilter{Limit: 10, Offset: 0})
	if err != nil {
		t.Fatalf("ListLogs failed: %v", err)
	}
	if len(logs) != 0 {
		t.Errorf("expected 0 logs after clean, got %d", len(logs))
	}

	// Verify associated attempts were also deleted (cascading delete)
	attemptsAfter, err := store.GetAttemptsByRequestID(ctx, "old-req-123")
	if err != nil {
		t.Fatalf("GetAttemptsByRequestID after cleanup failed: %v", err)
	}
	if len(attemptsAfter) != 0 {
		t.Errorf("expected 0 attempts after cleanup (cascading delete), got %d", len(attemptsAfter))
	}
}

func TestCleanOldLogs_RetainsRecentLogAttempts(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	oldTime := time.Now().AddDate(0, 0, -10) // 10 days ago
	recentTime := time.Now()

	// Create an old log with attempts (should be deleted)
	oldLog := &model.RequestLog{
		RequestID:  "old-req",
		ProviderID: "p1",
		APIType:    "claude",
		CreatedAt:  oldTime,
	}
	if err := store.InsertLog(ctx, oldLog); err != nil {
		t.Fatalf("InsertLog failed: %v", err)
	}
	oldAttempts := []model.RequestAttempt{
		{RequestID: "old-req", ProviderID: "p1", Attempt: 1, CreatedAt: oldTime},
	}
	if err := store.InsertAttempts(ctx, oldAttempts); err != nil {
		t.Fatalf("InsertAttempts failed: %v", err)
	}

	// Create a recent log with attempts (should be retained)
	recentLog := &model.RequestLog{
		RequestID:  "recent-req",
		ProviderID: "p2",
		APIType:    "claude",
		CreatedAt:  recentTime,
	}
	if err := store.InsertLog(ctx, recentLog); err != nil {
		t.Fatalf("InsertLog failed: %v", err)
	}
	recentAttempts := []model.RequestAttempt{
		{RequestID: "recent-req", ProviderID: "p2", Attempt: 1, CreatedAt: recentTime},
	}
	if err := store.InsertAttempts(ctx, recentAttempts); err != nil {
		t.Fatalf("InsertAttempts failed: %v", err)
	}

	// Clean logs older than 7 days
	if err := store.CleanOldLogs(ctx, 7); err != nil {
		t.Fatalf("CleanOldLogs failed: %v", err)
	}

	// Verify only recent log remains
	logs, err := store.ListLogs(ctx, model.LogFilter{Limit: 10})
	if err != nil {
		t.Fatalf("ListLogs failed: %v", err)
	}
	if len(logs) != 1 || logs[0].RequestID != "recent-req" {
		t.Errorf("expected only recent-req log to remain, got %d logs", len(logs))
	}

	// Verify old attempts were deleted
	oldAttemptsAfter, err := store.GetAttemptsByRequestID(ctx, "old-req")
	if err != nil {
		t.Fatalf("GetAttemptsByRequestID failed: %v", err)
	}
	if len(oldAttemptsAfter) != 0 {
		t.Errorf("expected old attempts to be deleted, got %d", len(oldAttemptsAfter))
	}

	// Verify recent attempts were retained
	recentAttemptsAfter, err := store.GetAttemptsByRequestID(ctx, "recent-req")
	if err != nil {
		t.Fatalf("GetAttemptsByRequestID failed: %v", err)
	}
	if len(recentAttemptsAfter) != 1 {
		t.Errorf("expected recent attempts to be retained, got %d", len(recentAttemptsAfter))
	}
}

func TestCleanOldLogs_NegativeBeforeDaysError(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	// CleanOldLogs with negative beforeDays should return an error
	err := store.CleanOldLogs(ctx, -1)
	if err == nil {
		t.Fatal("expected error for negative beforeDays, got nil")
	}

	// Verify error message contains useful information
	if !strings.Contains(err.Error(), "beforeDays must be non-negative") {
		t.Errorf("error message should indicate invalid input, got: %v", err)
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

func TestListLogs_FilterByIsSSE(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	logs := []model.RequestLog{
		{ProviderID: "p1", IsSSE: true, CreatedAt: time.Now()},
		{ProviderID: "p2", IsSSE: false, CreatedAt: time.Now()},
		{ProviderID: "p3", IsSSE: true, CreatedAt: time.Now()},
	}
	for i := range logs {
		if err := store.InsertLog(ctx, &logs[i]); err != nil {
			t.Fatalf("InsertLog failed: %v", err)
		}
	}

	// Filter by IsSSE=true
	isSSETrue := true
	result, err := store.ListLogs(ctx, model.LogFilter{IsSSE: &isSSETrue, Limit: 10})
	if err != nil {
		t.Fatalf("ListLogs failed: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("expected 2 SSE logs, got %d", len(result))
	}

	// Filter by IsSSE=false
	isSSEFalse := false
	result, err = store.ListLogs(ctx, model.LogFilter{IsSSE: &isSSEFalse, Limit: 10})
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

	logs := []model.RequestLog{
		{ProviderID: "p1", IsWebSocket: true, CreatedAt: time.Now()},
		{ProviderID: "p2", IsWebSocket: false, CreatedAt: time.Now()},
		{ProviderID: "p3", IsWebSocket: true, CreatedAt: time.Now()},
	}
	for i := range logs {
		if err := store.InsertLog(ctx, &logs[i]); err != nil {
			t.Fatalf("InsertLog failed: %v", err)
		}
	}

	// Filter by IsWebSocket=true
	isWSTrue := true
	result, err := store.ListLogs(ctx, model.LogFilter{IsWebSocket: &isWSTrue, Limit: 10})
	if err != nil {
		t.Fatalf("ListLogs failed: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("expected 2 WebSocket logs, got %d", len(result))
	}

	// Filter by IsWebSocket=false
	isWSFalse := false
	result, err = store.ListLogs(ctx, model.LogFilter{IsWebSocket: &isWSFalse, Limit: 10})
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

func TestGetLogStats_Empty(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	now := time.Now()
	startTime := now.Add(-24 * time.Hour)
	stats, err := store.GetLogStats(ctx, startTime, now)
	if err != nil {
		t.Fatalf("GetLogStats failed: %v", err)
	}

	if stats.TotalRequests != 0 {
		t.Errorf("TotalRequests = %d, want 0", stats.TotalRequests)
	}
	if stats.SuccessCount != 0 {
		t.Errorf("SuccessCount = %d, want 0", stats.SuccessCount)
	}
	if stats.FailCount != 0 {
		t.Errorf("FailCount = %d, want 0", stats.FailCount)
	}
	if stats.SuccessRate != 0 {
		t.Errorf("SuccessRate = %f, want 0", stats.SuccessRate)
	}
	if len(stats.ByAPIType) != 0 {
		t.Errorf("len(ByAPIType) = %d, want 0", len(stats.ByAPIType))
	}
	if len(stats.ByProvider) != 0 {
		t.Errorf("len(ByProvider) = %d, want 0", len(stats.ByProvider))
	}
}

func TestGetLogStats_WithData(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	now := time.Now()
	logs := []model.RequestLog{
		{ProviderID: "p1", APIType: "claude", Success: true, LatencyMs: 100, CreatedAt: now.Add(-1 * time.Hour)},
		{ProviderID: "p1", APIType: "claude", Success: true, LatencyMs: 200, CreatedAt: now.Add(-2 * time.Hour)},
		{ProviderID: "p2", APIType: "codex", Success: false, LatencyMs: 300, CreatedAt: now.Add(-3 * time.Hour)},
		{ProviderID: "p2", APIType: "codex", Success: true, LatencyMs: 400, CreatedAt: now.Add(-4 * time.Hour)},
	}
	for i := range logs {
		if err := store.InsertLog(ctx, &logs[i]); err != nil {
			t.Fatalf("InsertLog failed: %v", err)
		}
	}

	startTime := now.Add(-24 * time.Hour)
	stats, err := store.GetLogStats(ctx, startTime, now)
	if err != nil {
		t.Fatalf("GetLogStats failed: %v", err)
	}

	// Check overall statistics
	if stats.TotalRequests != 4 {
		t.Errorf("TotalRequests = %d, want 4", stats.TotalRequests)
	}
	if stats.SuccessCount != 3 {
		t.Errorf("SuccessCount = %d, want 3", stats.SuccessCount)
	}
	if stats.FailCount != 1 {
		t.Errorf("FailCount = %d, want 1", stats.FailCount)
	}
	if stats.SuccessRate < 0.74 || stats.SuccessRate > 0.76 {
		t.Errorf("SuccessRate = %f, want ~0.75", stats.SuccessRate)
	}
	// Average latency should be (100+200+300+400)/4 = 250
	if stats.AvgLatencyMs != 250 {
		t.Errorf("AvgLatencyMs = %d, want 250", stats.AvgLatencyMs)
	}

	// Check by API type
	if stats.ByAPIType["claude"] != 2 {
		t.Errorf("ByAPIType[claude] = %d, want 2", stats.ByAPIType["claude"])
	}
	if stats.ByAPIType["codex"] != 2 {
		t.Errorf("ByAPIType[codex] = %d, want 2", stats.ByAPIType["codex"])
	}

	// Check by provider
	if len(stats.ByProvider) != 2 {
		t.Errorf("len(ByProvider) = %d, want 2", len(stats.ByProvider))
	}
}

func TestGetLogStats_TimeFilter(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	now := time.Now()
	logs := []model.RequestLog{
		{ProviderID: "p1", APIType: "claude", Success: true, CreatedAt: now.Add(-1 * time.Hour)},
		{ProviderID: "p1", APIType: "claude", Success: true, CreatedAt: now.Add(-23 * time.Hour)},
		{ProviderID: "p1", APIType: "claude", Success: false, CreatedAt: now.Add(-25 * time.Hour)}, // Outside 24h
	}
	for i := range logs {
		if err := store.InsertLog(ctx, &logs[i]); err != nil {
			t.Fatalf("InsertLog failed: %v", err)
		}
	}

	// Get stats for last 24 hours
	startTime := now.Add(-24 * time.Hour)
	stats, err := store.GetLogStats(ctx, startTime, now)
	if err != nil {
		t.Fatalf("GetLogStats failed: %v", err)
	}

	// Only 2 logs should be counted
	if stats.TotalRequests != 2 {
		t.Errorf("TotalRequests = %d, want 2", stats.TotalRequests)
	}
	if stats.SuccessCount != 2 {
		t.Errorf("SuccessCount = %d, want 2", stats.SuccessCount)
	}
	if stats.FailCount != 0 {
		t.Errorf("FailCount = %d, want 0", stats.FailCount)
	}
}

func TestGetLogStats_AllPeriod(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	now := time.Now()
	logs := []model.RequestLog{
		{ProviderID: "p1", APIType: "claude", Success: true, CreatedAt: now.Add(-1 * time.Hour)},
		{ProviderID: "p1", APIType: "claude", Success: true, CreatedAt: now.Add(-30 * 24 * time.Hour)},  // 30 days ago
		{ProviderID: "p1", APIType: "claude", Success: false, CreatedAt: now.Add(-60 * 24 * time.Hour)}, // 60 days ago
	}
	for i := range logs {
		if err := store.InsertLog(ctx, &logs[i]); err != nil {
			t.Fatalf("InsertLog failed: %v", err)
		}
	}

	// Get stats for all time (zero start time)
	stats, err := store.GetLogStats(ctx, time.Time{}, now)
	if err != nil {
		t.Fatalf("GetLogStats failed: %v", err)
	}

	// All 3 logs should be counted
	if stats.TotalRequests != 3 {
		t.Errorf("TotalRequests = %d, want 3", stats.TotalRequests)
	}

	// Earliest log should be set
	if stats.EarliestLog.IsZero() {
		t.Error("EarliestLog should not be zero for 'all' period")
	}
}

func TestGetLogStats_ProviderSuccessRate(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	now := time.Now()
	logs := []model.RequestLog{
		{ProviderID: "p1", APIType: "claude", Success: true, CreatedAt: now.Add(-1 * time.Hour)},
		{ProviderID: "p1", APIType: "claude", Success: true, CreatedAt: now.Add(-2 * time.Hour)},
		{ProviderID: "p2", APIType: "codex", Success: false, CreatedAt: now.Add(-3 * time.Hour)},
		{ProviderID: "p2", APIType: "codex", Success: false, CreatedAt: now.Add(-4 * time.Hour)},
	}
	for i := range logs {
		if err := store.InsertLog(ctx, &logs[i]); err != nil {
			t.Fatalf("InsertLog failed: %v", err)
		}
	}

	startTime := now.Add(-24 * time.Hour)
	stats, err := store.GetLogStats(ctx, startTime, now)
	if err != nil {
		t.Fatalf("GetLogStats failed: %v", err)
	}

	// Find stats for each provider
	var p1Stats, p2Stats *model.ProviderLogStats
	for i := range stats.ByProvider {
		if stats.ByProvider[i].ProviderID == "p1" {
			p1Stats = &stats.ByProvider[i]
		} else if stats.ByProvider[i].ProviderID == "p2" {
			p2Stats = &stats.ByProvider[i]
		}
	}

	if p1Stats == nil {
		t.Fatal("p1 stats not found")
	}
	if p2Stats == nil {
		t.Fatal("p2 stats not found")
	}

	// p1 should have 100% success rate
	if p1Stats.SuccessRate != 1.0 {
		t.Errorf("p1.SuccessRate = %f, want 1.0", p1Stats.SuccessRate)
	}
	if p1Stats.Count != 2 {
		t.Errorf("p1.Count = %d, want 2", p1Stats.Count)
	}

	// p2 should have 0% success rate
	if p2Stats.SuccessRate != 0 {
		t.Errorf("p2.SuccessRate = %f, want 0", p2Stats.SuccessRate)
	}
	if p2Stats.Count != 2 {
		t.Errorf("p2.Count = %d, want 2", p2Stats.Count)
	}
}

func TestGetLogTimeSeries_Empty(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Hour)
	startTime := now.Add(-6 * time.Hour)
	granularity := time.Hour

	result, err := store.GetLogTimeSeries(ctx, startTime, now, granularity)
	if err != nil {
		t.Fatalf("GetLogTimeSeries failed: %v", err)
	}

	// Should have 6 buckets (one per hour)
	if len(result) != 6 {
		t.Errorf("len(result) = %d, want 6", len(result))
	}

	// All buckets should have zero values
	for i, point := range result {
		if point.Requests != 0 {
			t.Errorf("point[%d].Requests = %d, want 0", i, point.Requests)
		}
		if point.SuccessCount != 0 {
			t.Errorf("point[%d].SuccessCount = %d, want 0", i, point.SuccessCount)
		}
	}
}

func TestGetLogTimeSeries_WithData(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Hour)
	logs := []model.RequestLog{
		// 2 logs in the -1 hour bucket
		{ProviderID: "p1", APIType: "claude", Success: true, LatencyMs: 100, CreatedAt: now.Add(-1*time.Hour + 10*time.Minute)},
		{ProviderID: "p1", APIType: "claude", Success: true, LatencyMs: 200, CreatedAt: now.Add(-1*time.Hour + 30*time.Minute)},
		// 1 log in the -2 hour bucket
		{ProviderID: "p1", APIType: "claude", Success: false, LatencyMs: 300, CreatedAt: now.Add(-2*time.Hour + 15*time.Minute)},
		// Gap at -3 hour bucket
		// 1 log in the -4 hour bucket
		{ProviderID: "p1", APIType: "claude", Success: true, LatencyMs: 400, CreatedAt: now.Add(-4*time.Hour + 45*time.Minute)},
	}
	for i := range logs {
		if err := store.InsertLog(ctx, &logs[i]); err != nil {
			t.Fatalf("InsertLog failed: %v", err)
		}
	}

	startTime := now.Add(-6 * time.Hour)
	granularity := time.Hour

	result, err := store.GetLogTimeSeries(ctx, startTime, now, granularity)
	if err != nil {
		t.Fatalf("GetLogTimeSeries failed: %v", err)
	}

	// Should have 6 buckets
	if len(result) != 6 {
		t.Fatalf("len(result) = %d, want 6", len(result))
	}

	// Check that buckets are ordered by time
	for i := 1; i < len(result); i++ {
		if !result[i].Time.After(result[i-1].Time) {
			t.Errorf("result not ordered: point %d (%v) should be after point %d (%v)",
				i, result[i].Time, i-1, result[i-1].Time)
		}
	}

	// Build a map of bucket times to results for easier lookup
	bucketMap := make(map[time.Time]*model.TimeSeriesPoint)
	for i := range result {
		bucketMap[result[i].Time] = &result[i]
	}

	// Bucket time calculations:
	// If now is 10:00:00 (truncated):
	// - Log at -4h +45m = 6:45:00 falls in bucket starting at 6:00:00 = now.Add(-4*time.Hour)
	// - Log at -2h +15m = 8:15:00 falls in bucket starting at 8:00:00 = now.Add(-2*time.Hour)
	// - Logs at -1h +10m/30m = 9:10/9:30 fall in bucket starting at 9:00:00 = now.Add(-1*time.Hour)
	// - Gap at 7:00:00 = now.Add(-3*time.Hour)

	// Bucket containing -4h+45m log (starts at -4h)
	bucket4Time := now.Add(-4 * time.Hour)
	if bucket4, ok := bucketMap[bucket4Time]; ok {
		if bucket4.Requests != 1 {
			t.Errorf("bucket at %v Requests = %d, want 1", bucket4Time, bucket4.Requests)
		}
		if bucket4.SuccessCount != 1 {
			t.Errorf("bucket at %v SuccessCount = %d, want 1", bucket4Time, bucket4.SuccessCount)
		}
	} else {
		t.Errorf("bucket at %v not found", bucket4Time)
	}

	// Gap bucket (starts at -3h)
	bucket3Time := now.Add(-3 * time.Hour)
	if bucket3, ok := bucketMap[bucket3Time]; ok {
		if bucket3.Requests != 0 {
			t.Errorf("bucket at %v Requests = %d, want 0 (gap)", bucket3Time, bucket3.Requests)
		}
	} else {
		t.Errorf("bucket at %v not found", bucket3Time)
	}

	// Bucket containing -2h+15m log (starts at -2h)
	bucket2Time := now.Add(-2 * time.Hour)
	if bucket2, ok := bucketMap[bucket2Time]; ok {
		if bucket2.Requests != 1 {
			t.Errorf("bucket at %v Requests = %d, want 1", bucket2Time, bucket2.Requests)
		}
		if bucket2.SuccessCount != 0 {
			t.Errorf("bucket at %v SuccessCount = %d, want 0 (failure)", bucket2Time, bucket2.SuccessCount)
		}
		if bucket2.FailCount != 1 {
			t.Errorf("bucket at %v FailCount = %d, want 1", bucket2Time, bucket2.FailCount)
		}
	} else {
		t.Errorf("bucket at %v not found", bucket2Time)
	}

	// Bucket containing -1h+10m and -1h+30m logs (starts at -1h)
	bucket1Time := now.Add(-1 * time.Hour)
	if bucket1, ok := bucketMap[bucket1Time]; ok {
		if bucket1.Requests != 2 {
			t.Errorf("bucket at %v Requests = %d, want 2", bucket1Time, bucket1.Requests)
		}
		if bucket1.SuccessCount != 2 {
			t.Errorf("bucket at %v SuccessCount = %d, want 2", bucket1Time, bucket1.SuccessCount)
		}
		if bucket1.SuccessRate != 1.0 {
			t.Errorf("bucket at %v SuccessRate = %f, want 1.0", bucket1Time, bucket1.SuccessRate)
		}
		// Average latency should be (100+200)/2 = 150
		if bucket1.AvgLatencyMs != 150 {
			t.Errorf("bucket at %v AvgLatencyMs = %d, want 150", bucket1Time, bucket1.AvgLatencyMs)
		}
	} else {
		t.Errorf("bucket at %v not found", bucket1Time)
	}
}

func TestGetLogTimeSeries_DifferentGranularities(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Hour)
	logs := []model.RequestLog{
		{ProviderID: "p1", Success: true, CreatedAt: now.Add(-30 * time.Minute)},
		{ProviderID: "p1", Success: true, CreatedAt: now.Add(-1*time.Hour - 30*time.Minute)},
	}
	for i := range logs {
		if err := store.InsertLog(ctx, &logs[i]); err != nil {
			t.Fatalf("InsertLog failed: %v", err)
		}
	}

	tests := []struct {
		name        string
		granularity time.Duration
		duration    time.Duration
		wantBuckets int
	}{
		{"1h_granularity_6h", time.Hour, 6 * time.Hour, 6},
		{"15m_granularity_1h", 15 * time.Minute, time.Hour, 4},
		{"6h_granularity_24h", 6 * time.Hour, 24 * time.Hour, 4},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			startTime := now.Add(-tc.duration)
			result, err := store.GetLogTimeSeries(ctx, startTime, now, tc.granularity)
			if err != nil {
				t.Fatalf("GetLogTimeSeries failed: %v", err)
			}
			if len(result) != tc.wantBuckets {
				t.Errorf("len(result) = %d, want %d", len(result), tc.wantBuckets)
			}
		})
	}
}

func TestListLogs_FilterByMinRetryCount(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	logs := []model.RequestLog{
		{ProviderID: "p1", RetryCount: 0, CreatedAt: time.Now()},
		{ProviderID: "p2", RetryCount: 1, CreatedAt: time.Now()},
		{ProviderID: "p3", RetryCount: 2, CreatedAt: time.Now()},
		{ProviderID: "p4", RetryCount: 3, CreatedAt: time.Now()},
	}
	for i := range logs {
		if err := store.InsertLog(ctx, &logs[i]); err != nil {
			t.Fatalf("InsertLog failed: %v", err)
		}
	}

	// Filter by MinRetryCount=2 (should match logs with retry_count >= 2)
	minRetries := 2
	result, err := store.ListLogs(ctx, model.LogFilter{MinRetryCount: &minRetries, Limit: 10})
	if err != nil {
		t.Fatalf("ListLogs failed: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("expected 2 logs with retry_count >= 2, got %d", len(result))
	}
	for _, log := range result {
		if log.RetryCount < 2 {
			t.Errorf("expected retry_count >= 2, got %d", log.RetryCount)
		}
	}

	// Filter by MinRetryCount=1 (should match logs with retry_count >= 1)
	minRetries = 1
	result, err = store.ListLogs(ctx, model.LogFilter{MinRetryCount: &minRetries, Limit: 10})
	if err != nil {
		t.Fatalf("ListLogs failed: %v", err)
	}
	if len(result) != 3 {
		t.Errorf("expected 3 logs with retry_count >= 1, got %d", len(result))
	}

	// Filter by MinRetryCount=0 (should match all logs)
	minRetries = 0
	result, err = store.ListLogs(ctx, model.LogFilter{MinRetryCount: &minRetries, Limit: 10})
	if err != nil {
		t.Fatalf("ListLogs failed: %v", err)
	}
	if len(result) != 4 {
		t.Errorf("expected 4 logs with retry_count >= 0, got %d", len(result))
	}
}

func TestGetLogTimeSeries_SuccessRateCalculation(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Hour)
	// Create 4 logs: 3 success, 1 failure in same hour bucket
	// Logs at -1h+10m, -1h+20m, etc. fall in the bucket starting at now.Add(-1*time.Hour)
	logs := []model.RequestLog{
		{ProviderID: "p1", Success: true, CreatedAt: now.Add(-1*time.Hour + 10*time.Minute)},
		{ProviderID: "p1", Success: true, CreatedAt: now.Add(-1*time.Hour + 20*time.Minute)},
		{ProviderID: "p1", Success: true, CreatedAt: now.Add(-1*time.Hour + 30*time.Minute)},
		{ProviderID: "p1", Success: false, CreatedAt: now.Add(-1*time.Hour + 40*time.Minute)},
	}
	for i := range logs {
		if err := store.InsertLog(ctx, &logs[i]); err != nil {
			t.Fatalf("InsertLog failed: %v", err)
		}
	}

	startTime := now.Add(-2 * time.Hour)
	result, err := store.GetLogTimeSeries(ctx, startTime, now, time.Hour)
	if err != nil {
		t.Fatalf("GetLogTimeSeries failed: %v", err)
	}

	if len(result) != 2 {
		t.Fatalf("len(result) = %d, want 2", len(result))
	}

	// Build a map for lookup
	bucketMap := make(map[time.Time]*model.TimeSeriesPoint)
	for i := range result {
		bucketMap[result[i].Time] = &result[i]
	}

	// The bucket starting at -1h should have all the data
	bucketTime := now.Add(-1 * time.Hour)
	bucket, ok := bucketMap[bucketTime]
	if !ok {
		t.Fatalf("bucket at %v not found", bucketTime)
	}

	if bucket.Requests != 4 {
		t.Errorf("bucket.Requests = %d, want 4", bucket.Requests)
	}
	if bucket.SuccessCount != 3 {
		t.Errorf("bucket.SuccessCount = %d, want 3", bucket.SuccessCount)
	}
	if bucket.FailCount != 1 {
		t.Errorf("bucket.FailCount = %d, want 1", bucket.FailCount)
	}
	// Success rate should be 3/4 = 0.75
	if bucket.SuccessRate < 0.74 || bucket.SuccessRate > 0.76 {
		t.Errorf("bucket.SuccessRate = %f, want ~0.75", bucket.SuccessRate)
	}
}
