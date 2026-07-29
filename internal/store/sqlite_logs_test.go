package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"
)

func newNormalizedLog(providerID, apiType string, createdAt time.Time) model.RequestLog {
	return model.RequestLog{
		ProviderID:       providerID,
		APIType:          apiType,
		SemanticsVersion: model.RequestSemanticsVersionNormalizedV1,
		CompletionState:  completionStatePtr(model.CompletionStateCompleted),
		ServiceOutcome:   serviceOutcomePtr(model.ServiceOutcomeCompleted),
		ClientAction:     clientActionPtr(model.ClientActionNone),
		CreatedAt:        createdAt,
	}
}

func TestRequestLogs(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	now := time.Now()
	for i := range 5 {
		log := newNormalizedLog("p1", "claude", now.Add(time.Duration(i)*time.Second))
		if err := store.InsertLog(ctx, &log); err != nil {
			t.Fatalf("InsertLog failed: %v", err)
		}
	}

	logs, err := store.ListLogs(ctx, model.LogFilter{Limit: 3, Offset: 0})
	if err != nil {
		t.Fatalf("ListLogs failed: %v", err)
	}
	if len(logs) != 3 {
		t.Errorf("ListLogs len = %d, want 3", len(logs))
	}

	logs, err = store.ListLogs(ctx, model.LogFilter{Limit: 3, Offset: 3})
	if err != nil {
		t.Fatalf("ListLogs page 2 failed: %v", err)
	}
	if len(logs) != 2 {
		t.Errorf("ListLogs page 2 len = %d, want 2", len(logs))
	}

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

	count, err := store.CountLogs(ctx, model.LogFilter{})
	if err != nil {
		t.Fatalf("CountLogs failed: %v", err)
	}
	if count != 0 {
		t.Errorf("CountLogs = %d, want 0", count)
	}

	now := time.Now()
	for i := range 5 {
		log := newNormalizedLog("p1", "claude", now.Add(time.Duration(i)*time.Second))
		if err := store.InsertLog(ctx, &log); err != nil {
			t.Fatalf("InsertLog failed: %v", err)
		}
	}

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

	_, err := store.GetLogByID(ctx, 99999)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}

	reasoningTokens := int64(37)
	log := newNormalizedLog("p1", "claude", time.Now())
	log.ReasoningTokens = &reasoningTokens
	if err := store.InsertLog(ctx, &log); err != nil {
		t.Fatalf("InsertLog failed: %v", err)
	}

	found, err := store.GetLogByID(ctx, log.ID)
	if err != nil {
		t.Fatalf("GetLogByID failed: %v", err)
	}
	if found.ID != log.ID {
		t.Errorf("expected ID %d, got %d", log.ID, found.ID)
	}
	if found.ReasoningTokens == nil || *found.ReasoningTokens != reasoningTokens {
		t.Errorf("expected ReasoningTokens=%d, got %v", reasoningTokens, found.ReasoningTokens)
	}
}

func TestRequestLogRequestedReasoningRoundTrip(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()
	state := model.ReasoningObservationCaptured
	effort := " high "
	mode := "enabled"
	budget := int64(8192)
	log := newNormalizedLog("p1", "claude", time.Now())
	log.RequestedReasoningObservation = model.RequestedReasoningObservation{
		State:        &state,
		Effort:       &effort,
		Mode:         &mode,
		BudgetTokens: &budget,
	}

	if err := store.InsertLog(ctx, &log); err != nil {
		t.Fatalf("InsertLog failed: %v", err)
	}
	found, err := store.GetLogByID(ctx, log.ID)
	if err != nil {
		t.Fatalf("GetLogByID failed: %v", err)
	}
	if found.State == nil || *found.State != state {
		t.Fatalf("State = %v, want %q", found.State, state)
	}
	if found.Effort == nil || *found.Effort != effort {
		t.Fatalf("Effort = %v, want %q", found.Effort, effort)
	}
	if found.Mode == nil || *found.Mode != mode {
		t.Fatalf("Mode = %v, want %q", found.Mode, mode)
	}
	if found.BudgetTokens == nil || *found.BudgetTokens != budget {
		t.Fatalf("BudgetTokens = %v, want %d", found.BudgetTokens, budget)
	}
}

func TestCleanOldLogs_CascadesAttemptsDelete(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	oldTime := time.Now().AddDate(0, 0, -10)
	oldLog := newNormalizedLog("p1", "claude", oldTime)
	oldLog.RequestID = "old-req-123"
	if err := store.InsertLog(ctx, &oldLog); err != nil {
		t.Fatalf("InsertLog failed: %v", err)
	}

	attempts := []model.RequestAttempt{
		{
			RequestID:        "old-req-123",
			ProviderID:       "p1",
			SemanticsVersion: model.RequestSemanticsVersionNormalizedV1,
			Attempt:          1,
			StatusCode:       503,
			CreatedAt:        oldTime,
		},
		{
			RequestID:        "old-req-123",
			ProviderID:       "p2",
			SemanticsVersion: model.RequestSemanticsVersionNormalizedV1,
			Attempt:          2,
			StatusCode:       200,
			CreatedAt:        oldTime,
		},
	}
	if err := store.InsertAttempts(ctx, attempts); err != nil {
		t.Fatalf("InsertAttempts failed: %v", err)
	}

	if err := store.CleanOldLogs(ctx, 7); err != nil {
		t.Fatalf("CleanOldLogs failed: %v", err)
	}

	logs, err := store.ListLogs(ctx, model.LogFilter{Limit: 10, Offset: 0})
	if err != nil {
		t.Fatalf("ListLogs failed: %v", err)
	}
	if len(logs) != 0 {
		t.Errorf("expected 0 logs after clean, got %d", len(logs))
	}

	attemptsAfter, err := store.GetAttemptsByRequestID(ctx, "old-req-123")
	if err != nil {
		t.Fatalf("GetAttemptsByRequestID after cleanup failed: %v", err)
	}
	if len(attemptsAfter) != 0 {
		t.Errorf("expected 0 attempts after cleanup, got %d", len(attemptsAfter))
	}
}

func TestCleanOldLogs_RetainsRecentLogAttempts(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	oldTime := time.Now().AddDate(0, 0, -10)
	recentTime := time.Now()

	oldLog := newNormalizedLog("p1", "claude", oldTime)
	oldLog.RequestID = "old-req"
	if err := store.InsertLog(ctx, &oldLog); err != nil {
		t.Fatalf("InsertLog failed: %v", err)
	}
	if err := store.InsertAttempts(ctx, []model.RequestAttempt{{
		RequestID:        "old-req",
		ProviderID:       "p1",
		SemanticsVersion: model.RequestSemanticsVersionNormalizedV1,
		Attempt:          1,
		CreatedAt:        oldTime,
	}}); err != nil {
		t.Fatalf("InsertAttempts failed: %v", err)
	}

	recentLog := newNormalizedLog("p2", "claude", recentTime)
	recentLog.RequestID = "recent-req"
	if err := store.InsertLog(ctx, &recentLog); err != nil {
		t.Fatalf("InsertLog failed: %v", err)
	}
	if err := store.InsertAttempts(ctx, []model.RequestAttempt{{
		RequestID:        "recent-req",
		ProviderID:       "p2",
		SemanticsVersion: model.RequestSemanticsVersionNormalizedV1,
		Attempt:          1,
		CreatedAt:        recentTime,
	}}); err != nil {
		t.Fatalf("InsertAttempts failed: %v", err)
	}

	if err := store.CleanOldLogs(ctx, 7); err != nil {
		t.Fatalf("CleanOldLogs failed: %v", err)
	}

	logs, err := store.ListLogs(ctx, model.LogFilter{Limit: 10})
	if err != nil {
		t.Fatalf("ListLogs failed: %v", err)
	}
	if len(logs) != 1 || logs[0].RequestID != "recent-req" {
		t.Fatalf("expected only recent-req log to remain, got %+v", logs)
	}

	oldAttemptsAfter, err := store.GetAttemptsByRequestID(ctx, "old-req")
	if err != nil {
		t.Fatalf("GetAttemptsByRequestID failed: %v", err)
	}
	if len(oldAttemptsAfter) != 0 {
		t.Errorf("expected old attempts to be deleted, got %d", len(oldAttemptsAfter))
	}

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

	err := store.CleanOldLogs(ctx, -1)
	if err == nil {
		t.Fatal("expected error for negative beforeDays, got nil")
	}
	if !strings.Contains(err.Error(), "beforeDays must be non-negative") {
		t.Errorf("error message should indicate invalid input, got: %v", err)
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
	if stats.AvgLatencyMs != 0 {
		t.Errorf("AvgLatencyMs = %d, want 0", stats.AvgLatencyMs)
	}
	if len(stats.OutcomeCounts) != 0 {
		t.Errorf("len(OutcomeCounts) = %d, want 0", len(stats.OutcomeCounts))
	}
	if len(stats.ByAPIType) != 0 {
		t.Errorf("len(ByAPIType) = %d, want 0", len(stats.ByAPIType))
	}
	if len(stats.ByProvider) != 0 {
		t.Errorf("len(ByProvider) = %d, want 0", len(stats.ByProvider))
	}
}

func TestGetLogStats_UsesNormalizedOutcomeCountsAndExcludesLegacy(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	now := time.Now()
	logs := []model.RequestLog{
		{
			ProviderID:       "p1",
			APIType:          "claude",
			SemanticsVersion: model.RequestSemanticsVersionNormalizedV1,
			ServiceOutcome:   serviceOutcomePtr(model.ServiceOutcomeCompleted),
			ClientAction:     clientActionPtr(model.ClientActionNone),
			LatencyMs:        100,
			CreatedAt:        now.Add(-1 * time.Hour),
		},
		{
			ProviderID:       "p1",
			APIType:          "claude",
			SemanticsVersion: model.RequestSemanticsVersionNormalizedV1,
			ServiceOutcome:   serviceOutcomePtr(model.ServiceOutcomeCompleted),
			ClientAction:     clientActionPtr(model.ClientActionNone),
			LatencyMs:        200,
			CreatedAt:        now.Add(-2 * time.Hour),
		},
		{
			ProviderID:       "p2",
			APIType:          "codex",
			SemanticsVersion: model.RequestSemanticsVersionNormalizedV1,
			ServiceOutcome:   serviceOutcomePtr(model.ServiceOutcomeInterrupted),
			ClientAction:     clientActionPtr(model.ClientActionReconnectRequired),
			LatencyMs:        300,
			CreatedAt:        now.Add(-3 * time.Hour),
		},
		{
			ProviderID:       "legacy-provider",
			APIType:          "claude",
			SemanticsVersion: model.RequestSemanticsVersionLegacyPreAssessment,
			LatencyMs:        999,
			CreatedAt:        now.Add(-4 * time.Hour),
		},
	}
	insertLogFixtures(t, store, ctx, logs)

	stats, err := store.GetLogStats(ctx, now.Add(-24*time.Hour), now)
	if err != nil {
		t.Fatalf("GetLogStats failed: %v", err)
	}

	if stats.TotalRequests != 3 {
		t.Fatalf("TotalRequests = %d, want 3", stats.TotalRequests)
	}
	if stats.AvgLatencyMs != 200 {
		t.Fatalf("AvgLatencyMs = %d, want 200", stats.AvgLatencyMs)
	}
	if stats.OutcomeCounts[model.ServiceOutcomeCompleted] != 2 {
		t.Fatalf("completed count = %d, want 2", stats.OutcomeCounts[model.ServiceOutcomeCompleted])
	}
	if stats.OutcomeCounts[model.ServiceOutcomeInterrupted] != 1 {
		t.Fatalf("interrupted count = %d, want 1", stats.OutcomeCounts[model.ServiceOutcomeInterrupted])
	}
	if stats.ByAPIType["claude"] != 2 {
		t.Fatalf("ByAPIType[claude] = %d, want 2", stats.ByAPIType["claude"])
	}
	if stats.ByAPIType["codex"] != 1 {
		t.Fatalf("ByAPIType[codex] = %d, want 1", stats.ByAPIType["codex"])
	}
	if len(stats.ByProvider) != 2 {
		t.Fatalf("len(ByProvider) = %d, want 2", len(stats.ByProvider))
	}
	if stats.ByProvider[0].ProviderID != "p1" || stats.ByProvider[0].Count != 2 {
		t.Fatalf("first provider stats = %+v, want p1 count=2", stats.ByProvider[0])
	}
	if stats.ByProvider[0].OutcomeCounts[model.ServiceOutcomeCompleted] != 2 {
		t.Fatalf("p1 completed count = %d, want 2", stats.ByProvider[0].OutcomeCounts[model.ServiceOutcomeCompleted])
	}
}

func TestGetLogStats_TimeFilterAndAllPeriod(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	now := time.Now()
	insertLogFixtures(t, store, ctx, []model.RequestLog{
		{
			ProviderID:       "p1",
			APIType:          "claude",
			SemanticsVersion: model.RequestSemanticsVersionNormalizedV1,
			ServiceOutcome:   serviceOutcomePtr(model.ServiceOutcomeCompleted),
			CreatedAt:        now.Add(-1 * time.Hour),
		},
		{
			ProviderID:       "p1",
			APIType:          "claude",
			SemanticsVersion: model.RequestSemanticsVersionNormalizedV1,
			ServiceOutcome:   serviceOutcomePtr(model.ServiceOutcomeCompleted),
			CreatedAt:        now.Add(-30 * 24 * time.Hour),
		},
		{
			ProviderID:       "p1",
			APIType:          "claude",
			SemanticsVersion: model.RequestSemanticsVersionLegacyPreAssessment,
			CreatedAt:        now.Add(-60 * 24 * time.Hour),
		},
	})

	stats, err := store.GetLogStats(ctx, now.Add(-24*time.Hour), now)
	if err != nil {
		t.Fatalf("GetLogStats failed: %v", err)
	}
	if stats.TotalRequests != 1 {
		t.Fatalf("TotalRequests = %d, want 1", stats.TotalRequests)
	}

	stats, err = store.GetLogStats(ctx, time.Time{}, now)
	if err != nil {
		t.Fatalf("GetLogStats failed: %v", err)
	}
	if stats.TotalRequests != 2 {
		t.Fatalf("TotalRequests = %d, want 2", stats.TotalRequests)
	}
	if stats.EarliestLog.IsZero() {
		t.Fatal("EarliestLog should not be zero for normalized all-period stats")
	}
	if !stats.EarliestLog.Equal(now.Add(-30 * 24 * time.Hour)) {
		t.Fatalf("EarliestLog = %v, want %v", stats.EarliestLog, now.Add(-30*24*time.Hour))
	}
}

func TestGetLogTimeSeries_Empty(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Hour)
	startTime := now.Add(-6 * time.Hour)

	result, err := store.GetLogTimeSeries(ctx, startTime, now, time.Hour)
	if err != nil {
		t.Fatalf("GetLogTimeSeries failed: %v", err)
	}
	if len(result) != 6 {
		t.Fatalf("len(result) = %d, want 6", len(result))
	}
	for i, point := range result {
		if point.Requests != 0 {
			t.Errorf("point[%d].Requests = %d, want 0", i, point.Requests)
		}
		if len(point.OutcomeCounts) != 0 {
			t.Errorf("point[%d].OutcomeCounts = %+v, want empty map", i, point.OutcomeCounts)
		}
	}
}

func TestGetLogTimeSeries_UsesOutcomeCountsAndZeroFills(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Hour)
	insertLogFixtures(t, store, ctx, []model.RequestLog{
		{
			ProviderID:       "p1",
			APIType:          "claude",
			SemanticsVersion: model.RequestSemanticsVersionNormalizedV1,
			ServiceOutcome:   serviceOutcomePtr(model.ServiceOutcomeCompleted),
			LatencyMs:        100,
			CreatedAt:        now.Add(-1*time.Hour + 10*time.Minute),
		},
		{
			ProviderID:       "p1",
			APIType:          "claude",
			SemanticsVersion: model.RequestSemanticsVersionNormalizedV1,
			ServiceOutcome:   serviceOutcomePtr(model.ServiceOutcomeCompleted),
			LatencyMs:        200,
			CreatedAt:        now.Add(-1*time.Hour + 30*time.Minute),
		},
		{
			ProviderID:       "p1",
			APIType:          "claude",
			SemanticsVersion: model.RequestSemanticsVersionNormalizedV1,
			ServiceOutcome:   serviceOutcomePtr(model.ServiceOutcomeInterrupted),
			LatencyMs:        300,
			CreatedAt:        now.Add(-2*time.Hour + 15*time.Minute),
		},
		{
			ProviderID:       "legacy",
			APIType:          "claude",
			SemanticsVersion: model.RequestSemanticsVersionLegacyPreAssessment,
			LatencyMs:        999,
			CreatedAt:        now.Add(-2*time.Hour + 20*time.Minute),
		},
		{
			ProviderID:       "p1",
			APIType:          "claude",
			SemanticsVersion: model.RequestSemanticsVersionNormalizedV1,
			ServiceOutcome:   serviceOutcomePtr(model.ServiceOutcomeAbandonedByClient),
			LatencyMs:        400,
			CreatedAt:        now.Add(-4*time.Hour + 45*time.Minute),
		},
	})

	result, err := store.GetLogTimeSeries(ctx, now.Add(-6*time.Hour), now, time.Hour)
	if err != nil {
		t.Fatalf("GetLogTimeSeries failed: %v", err)
	}
	if len(result) != 6 {
		t.Fatalf("len(result) = %d, want 6", len(result))
	}

	bucketMap := make(map[time.Time]model.TimeSeriesPoint, len(result))
	for _, point := range result {
		bucketMap[point.Time] = point
	}

	bucket4 := bucketMap[now.Add(-4*time.Hour)]
	if bucket4.Requests != 1 || bucket4.OutcomeCounts[model.ServiceOutcomeAbandonedByClient] != 1 {
		t.Fatalf("bucket4 = %+v, want 1 abandoned_by_client request", bucket4)
	}

	gapBucket := bucketMap[now.Add(-3*time.Hour)]
	if gapBucket.Requests != 0 || len(gapBucket.OutcomeCounts) != 0 {
		t.Fatalf("gap bucket = %+v, want zero-filled bucket", gapBucket)
	}

	bucket2 := bucketMap[now.Add(-2*time.Hour)]
	if bucket2.Requests != 1 {
		t.Fatalf("bucket2.Requests = %d, want 1", bucket2.Requests)
	}
	if bucket2.OutcomeCounts[model.ServiceOutcomeInterrupted] != 1 {
		t.Fatalf("bucket2 outcome counts = %+v, want interrupted=1", bucket2.OutcomeCounts)
	}

	bucket1 := bucketMap[now.Add(-1*time.Hour)]
	if bucket1.Requests != 2 {
		t.Fatalf("bucket1.Requests = %d, want 2", bucket1.Requests)
	}
	if bucket1.OutcomeCounts[model.ServiceOutcomeCompleted] != 2 {
		t.Fatalf("bucket1 outcome counts = %+v, want completed=2", bucket1.OutcomeCounts)
	}
	if bucket1.AvgLatencyMs != 150 {
		t.Fatalf("bucket1.AvgLatencyMs = %d, want 150", bucket1.AvgLatencyMs)
	}
}

func TestGetLogTimeSeries_DifferentGranularities(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Hour)
	insertLogFixtures(t, store, ctx, []model.RequestLog{
		{
			ProviderID:       "p1",
			SemanticsVersion: model.RequestSemanticsVersionNormalizedV1,
			ServiceOutcome:   serviceOutcomePtr(model.ServiceOutcomeCompleted),
			CreatedAt:        now.Add(-30 * time.Minute),
		},
		{
			ProviderID:       "p1",
			SemanticsVersion: model.RequestSemanticsVersionNormalizedV1,
			ServiceOutcome:   serviceOutcomePtr(model.ServiceOutcomeCompleted),
			CreatedAt:        now.Add(-1*time.Hour - 30*time.Minute),
		},
	})

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
			result, err := store.GetLogTimeSeries(ctx, now.Add(-tc.duration), now, tc.granularity)
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

	insertLogFixtures(t, store, ctx, []model.RequestLog{
		{ProviderID: "p1", RetryCount: 0, SemanticsVersion: model.RequestSemanticsVersionNormalizedV1, CreatedAt: time.Now()},
		{ProviderID: "p2", RetryCount: 1, SemanticsVersion: model.RequestSemanticsVersionNormalizedV1, CreatedAt: time.Now()},
		{ProviderID: "p3", RetryCount: 2, SemanticsVersion: model.RequestSemanticsVersionNormalizedV1, CreatedAt: time.Now()},
		{ProviderID: "p4", RetryCount: 3, SemanticsVersion: model.RequestSemanticsVersionNormalizedV1, CreatedAt: time.Now()},
	})

	minRetries := 2
	result, err := store.ListLogs(ctx, model.LogFilter{MinRetryCount: &minRetries, Limit: 10})
	if err != nil {
		t.Fatalf("ListLogs failed: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("expected 2 logs with retry_count >= 2, got %d", len(result))
	}

	minRetries = 1
	result, err = store.ListLogs(ctx, model.LogFilter{MinRetryCount: &minRetries, Limit: 10})
	if err != nil {
		t.Fatalf("ListLogs failed: %v", err)
	}
	if len(result) != 3 {
		t.Errorf("expected 3 logs with retry_count >= 1, got %d", len(result))
	}

	minRetries = 0
	result, err = store.ListLogs(ctx, model.LogFilter{MinRetryCount: &minRetries, Limit: 10})
	if err != nil {
		t.Fatalf("ListLogs failed: %v", err)
	}
	if len(result) != 4 {
		t.Errorf("expected 4 logs with retry_count >= 0, got %d", len(result))
	}
}
