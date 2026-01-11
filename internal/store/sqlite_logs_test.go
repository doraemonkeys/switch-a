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
	logs, err := store.ListLogs(ctx, 3, 0)
	if err != nil {
		t.Fatalf("ListLogs failed: %v", err)
	}
	if len(logs) != 3 {
		t.Errorf("ListLogs len = %d, want 3", len(logs))
	}

	// List second page
	logs, err = store.ListLogs(ctx, 3, 3)
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

	logs, err = store.ListLogs(ctx, 10, 0)
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
	count, err := store.CountLogs(ctx)
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
	count, err = store.CountLogs(ctx)
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
	logs, err := store.ListLogs(ctx, 10, 0)
	if err != nil {
		t.Fatalf("ListLogs failed: %v", err)
	}
	if len(logs) != 1 {
		t.Errorf("expected 1 log after clean, got %d", len(logs))
	}
}
