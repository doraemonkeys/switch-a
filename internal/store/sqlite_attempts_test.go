package store

import (
	"context"
	"testing"
	"time"

	"switch-a/internal/model"
)

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
