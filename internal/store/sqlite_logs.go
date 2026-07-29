package store

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"

	"gorm.io/gorm"
)

// Sort field and order constants for log queries.
const (
	SortByCreatedAt = "created_at"
	SortByLatencyMs = "latency_ms"
	SortOrderAsc    = "asc"
	SortOrderDesc   = "desc"
)

func (s *SQLiteStore) InsertLog(ctx context.Context, log *model.RequestLog) error {
	if err := s.db.WithContext(ctx).Create(log).Error; err != nil {
		return fmt.Errorf("insert log: %w", err)
	}
	return nil
}

func (s *SQLiteStore) ListLogs(ctx context.Context, filter model.LogFilter) ([]model.RequestLog, error) {
	var logs []model.RequestLog
	query := s.db.WithContext(ctx).Model(&model.RequestLog{})

	// Apply filters
	query = s.applyLogFilters(query, filter)

	// Apply sorting
	sortBy := SortByCreatedAt
	if filter.SortBy == SortByLatencyMs {
		sortBy = SortByLatencyMs
	}
	sortOrder := "DESC"
	if filter.SortOrder == SortOrderAsc {
		sortOrder = "ASC"
	}
	query = query.Order(sortBy + " " + sortOrder)

	// Apply pagination
	if filter.Limit > 0 {
		query = query.Limit(filter.Limit)
	}
	if filter.Offset > 0 {
		query = query.Offset(filter.Offset)
	}

	if err := query.Find(&logs).Error; err != nil {
		return nil, fmt.Errorf("list logs: %w", err)
	}
	return logs, nil
}

// applyLogFilters applies the filter conditions to a GORM query.
func (s *SQLiteStore) applyLogFilters(query *gorm.DB, filter model.LogFilter) *gorm.DB {
	if filter.ProviderID != "" {
		query = query.Where("provider_id = ?", filter.ProviderID)
	}
	if filter.APIType != "" {
		query = query.Where("api_type = ?", filter.APIType)
	}
	if filter.SemanticsVersion != "" {
		query = query.Where("semantics_version = ?", filter.SemanticsVersion)
	}
	if filter.CompletionState != "" {
		query = query.Where("completion_state = ?", filter.CompletionState)
	}
	if filter.ServiceOutcome != "" {
		query = query.Where("service_outcome = ?", filter.ServiceOutcome)
	}
	if filter.ClientAction != "" {
		query = query.Where("client_action = ?", filter.ClientAction)
	}
	if filter.TerminationActor != "" {
		query = query.Where("termination_actor = ?", filter.TerminationActor)
	}
	if filter.TerminationReason != "" {
		query = query.Where("termination_reason = ?", filter.TerminationReason)
	}
	if filter.ClientTransportStatusCode != nil {
		query = query.Where("client_transport_status_code = ?", *filter.ClientTransportStatusCode)
	}
	if filter.IsSSE != nil {
		query = query.Where("is_sse = ?", *filter.IsSSE)
	}
	if filter.HasWebSocketLifecycleFilter() {
		query = query.Where("is_websocket = ?", true)
	}
	if filter.IsWebSocket != nil {
		query = query.Where("is_websocket = ?", *filter.IsWebSocket)
	}
	if filter.SessionCommitted != nil {
		query = query.Where("session_committed = ?", *filter.SessionCommitted)
	}
	if filter.ClientVisible != nil {
		query = query.Where("client_visible = ?", *filter.ClientVisible)
	}
	if filter.CommitSource != "" {
		query = query.Where("commit_source = ?", filter.CommitSource)
	}
	if filter.UserID != "" {
		query = query.Where("user_id = ?", filter.UserID)
	}
	if filter.StartTime != nil {
		query = query.Where("created_at >= ?", *filter.StartTime)
	}
	if filter.EndTime != nil {
		query = query.Where("created_at < ?", *filter.EndTime)
	}
	if filter.MinLatency != nil {
		query = query.Where("latency_ms >= ?", *filter.MinLatency)
	}
	if filter.MinRetryCount != nil {
		query = query.Where("retry_count >= ?", *filter.MinRetryCount)
	}
	if filter.HasRetries != nil {
		if *filter.HasRetries {
			query = query.Where("retry_count > 0")
		} else {
			query = query.Where("retry_count = 0")
		}
	}
	return query
}

func (s *SQLiteStore) CountLogs(ctx context.Context, filter model.LogFilter) (int64, error) {
	var count int64
	query := s.db.WithContext(ctx).Model(&model.RequestLog{})

	// Apply the same filters as ListLogs (but ignore pagination)
	query = s.applyLogFilters(query, filter)

	if err := query.Count(&count).Error; err != nil {
		return 0, fmt.Errorf("count logs: %w", err)
	}
	return count, nil
}

// GetLogByID retrieves a single log entry by its ID.
func (s *SQLiteStore) GetLogByID(ctx context.Context, id uint) (*model.RequestLog, error) {
	var log model.RequestLog
	if err := s.db.WithContext(ctx).First(&log, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get log by id %d: %w", id, err)
	}
	return &log, nil
}

func (s *SQLiteStore) CleanOldLogs(ctx context.Context, beforeDays int) error {
	// Validate input to prevent accidental deletion of recent logs.
	// A negative value would result in a cutoff in the future.
	if beforeDays < 0 {
		return fmt.Errorf("beforeDays must be non-negative, got %d", beforeDays)
	}

	cutoff := s.clock.Now().AddDate(0, 0, -beforeDays)

	// Use a transaction to delete both logs and their associated attempts atomically.
	// This prevents orphaned request_attempts records from accumulating.
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Get request IDs to be deleted
		var requestIDs []string
		if err := tx.Model(&model.RequestLog{}).
			Where("created_at < ?", cutoff).
			Pluck("request_id", &requestIDs).Error; err != nil {
			return fmt.Errorf("get request IDs for cleanup: %w", err)
		}

		// Delete associated attempts first (foreign key integrity)
		if len(requestIDs) > 0 {
			if err := tx.Where("request_id IN ?", requestIDs).
				Delete(&model.RequestAttempt{}).Error; err != nil {
				return fmt.Errorf("clean old attempts: %w", err)
			}
		}

		// Delete logs
		if err := tx.Where("created_at < ?", cutoff).
			Delete(&model.RequestLog{}).Error; err != nil {
			return fmt.Errorf("clean old logs (before %d days): %w", beforeDays, err)
		}

		return nil
	})
}

// normalizedLogStatsBaseQuery keeps normalized reporting orthogonal to legacy
// rows because the old request-level semantics cannot be losslessly mapped into
// the new service_outcome taxonomy.
func (s *SQLiteStore) normalizedLogStatsBaseQuery(ctx context.Context, startTime, endTime time.Time) *gorm.DB {
	baseQuery := s.db.WithContext(ctx).
		Model(&model.RequestLog{}).
		Where("semantics_version = ?", model.RequestSemanticsVersionNormalizedV1)
	if !startTime.IsZero() {
		baseQuery = baseQuery.Where("created_at >= ?", startTime)
	}
	return baseQuery.Where("created_at < ?", endTime)
}

// GetLogStats retrieves aggregated statistics from request logs within the given time range.
// If startTime is zero, all logs are included (for "all" period).
func (s *SQLiteStore) GetLogStats(ctx context.Context, startTime, endTime time.Time) (*model.LogStats, error) {
	stats := &model.LogStats{
		OutcomeCounts: make(map[model.ServiceOutcome]int64),
		ByAPIType:     make(map[string]int64),
		ByProvider:    []model.ProviderLogStats{},
	}

	baseQuery := s.normalizedLogStatsBaseQuery(ctx, startTime, endTime)

	// Get overall statistics using a single query
	var overallStats struct {
		TotalRequests int64
		AvgLatencyMs  float64
	}
	err := baseQuery.Session(&gorm.Session{}).Select(
		"COUNT(*) as total_requests",
		"COALESCE(AVG(latency_ms), 0) as avg_latency_ms",
	).Scan(&overallStats).Error
	if err != nil {
		return nil, fmt.Errorf("get overall log stats: %w", err)
	}

	stats.TotalRequests = overallStats.TotalRequests
	stats.AvgLatencyMs = int64(overallStats.AvgLatencyMs)

	type outcomeStat struct {
		ServiceOutcome string
		Count          int64
	}
	var outcomeStats []outcomeStat
	err = baseQuery.Session(&gorm.Session{}).
		Select("service_outcome", "COUNT(*) as count").
		Group("service_outcome").
		Scan(&outcomeStats).Error
	if err != nil {
		return nil, fmt.Errorf("get outcome stats: %w", err)
	}
	for _, stat := range outcomeStats {
		if stat.ServiceOutcome == "" {
			continue
		}
		stats.OutcomeCounts[model.ServiceOutcome(stat.ServiceOutcome)] = stat.Count
	}

	// Get statistics by API type
	type apiTypeStat struct {
		APIType string
		Count   int64
	}
	var apiTypeStats []apiTypeStat
	err = baseQuery.Session(&gorm.Session{}).
		Select("api_type", "COUNT(*) as count").
		Group("api_type").
		Scan(&apiTypeStats).Error
	if err != nil {
		return nil, fmt.Errorf("get api type stats: %w", err)
	}
	for _, stat := range apiTypeStats {
		stats.ByAPIType[stat.APIType] = stat.Count
	}

	// Grouping provider and outcome in SQL keeps the database work set small while
	// still letting Go assemble the nested response shape the admin API needs.
	type providerOutcomeStat struct {
		ProviderID     string
		ServiceOutcome string
		Count          int64
	}
	var providerOutcomeStats []providerOutcomeStat
	err = baseQuery.Session(&gorm.Session{}).
		Select(
			"provider_id",
			"service_outcome",
			"COUNT(*) as count",
		).
		Group("provider_id, service_outcome").
		Scan(&providerOutcomeStats).Error
	if err != nil {
		return nil, fmt.Errorf("get provider stats: %w", err)
	}
	providerStats := make(map[string]*model.ProviderLogStats)
	for _, stat := range providerOutcomeStats {
		providerStat, ok := providerStats[stat.ProviderID]
		if !ok {
			providerStat = &model.ProviderLogStats{
				ProviderID:    stat.ProviderID,
				OutcomeCounts: make(map[model.ServiceOutcome]int64),
			}
			providerStats[stat.ProviderID] = providerStat
		}
		providerStat.Count += stat.Count
		if stat.ServiceOutcome != "" {
			providerStat.OutcomeCounts[model.ServiceOutcome(stat.ServiceOutcome)] = stat.Count
		}
	}
	for _, stat := range providerStats {
		stats.ByProvider = append(stats.ByProvider, *stat)
	}
	sort.Slice(stats.ByProvider, func(i, j int) bool {
		if stats.ByProvider[i].Count == stats.ByProvider[j].Count {
			return stats.ByProvider[i].ProviderID < stats.ByProvider[j].ProviderID
		}
		return stats.ByProvider[i].Count > stats.ByProvider[j].Count
	})

	// "all" period charts should start at the first normalized row, not the first
	// legacy row, so the time range matches the aggregation contract.
	if startTime.IsZero() {
		var earliestLog model.RequestLog
		err = s.db.WithContext(ctx).Model(&model.RequestLog{}).
			Where("semantics_version = ?", model.RequestSemanticsVersionNormalizedV1).
			Order("created_at ASC").
			Limit(1).
			Select("created_at").
			First(&earliestLog).Error
		if err == nil {
			stats.EarliestLog = earliestLog.CreatedAt
		}
		// Ignore error - it's OK if there are no logs
	}

	return stats, nil
}

// GetLogTimeSeries retrieves time series statistics from request logs.
// The data is bucketed by the specified granularity (e.g., 5m, 1h, 1d).
// Returns data points for each time bucket, with zero-filled gaps.
func (s *SQLiteStore) GetLogTimeSeries(ctx context.Context, startTime, endTime time.Time, granularity time.Duration) ([]model.TimeSeriesPoint, error) {
	// Calculate the strftime format based on granularity
	// SQLite's strftime works with UTC timestamps
	granularitySeconds := int64(granularity.Seconds())

	// Query with time bucketing using integer division
	// We use (strftime('%s', created_at) / granularity) * granularity to bucket
	type bucketOutcomeStat struct {
		BucketTime     int64
		ServiceOutcome string
		Requests       int64
		TotalLatency   int64
	}
	var bucketStats []bucketOutcomeStat

	// Build the query
	query := s.db.WithContext(ctx).Model(&model.RequestLog{}).
		Select(
			fmt.Sprintf("(CAST(strftime('%%s', created_at) AS INTEGER) / %d) * %d as bucket_time", granularitySeconds, granularitySeconds),
			"service_outcome",
			"COUNT(*) as requests",
			"COALESCE(SUM(latency_ms), 0) as total_latency",
		).
		Where("semantics_version = ?", model.RequestSemanticsVersionNormalizedV1).
		Where("created_at >= ? AND created_at < ?", startTime, endTime).
		Group("bucket_time, service_outcome").
		Order("bucket_time ASC")

	if err := query.Scan(&bucketStats).Error; err != nil {
		return nil, fmt.Errorf("get log time series: %w", err)
	}

	// Create a map of existing data points for quick lookup
	dataMap := make(map[int64]*model.TimeSeriesPoint, len(bucketStats))
	for _, stat := range bucketStats {
		point, ok := dataMap[stat.BucketTime]
		if !ok {
			point = &model.TimeSeriesPoint{
				Time:          time.Unix(stat.BucketTime, 0).UTC(),
				OutcomeCounts: make(map[model.ServiceOutcome]int64),
			}
			dataMap[stat.BucketTime] = point
		}
		point.Requests += stat.Requests
		point.AvgLatencyMs += stat.TotalLatency
		if stat.ServiceOutcome != "" {
			point.OutcomeCounts[model.ServiceOutcome(stat.ServiceOutcome)] += stat.Requests
		}
	}
	for _, point := range dataMap {
		if point.Requests > 0 {
			point.AvgLatencyMs /= point.Requests
		}
	}

	// Generate all time buckets and fill with data or zeros
	var result []model.TimeSeriesPoint
	startBucket := (startTime.Unix() / granularitySeconds) * granularitySeconds
	endBucket := (endTime.Unix() / granularitySeconds) * granularitySeconds

	for bucket := startBucket; bucket < endBucket; bucket += granularitySeconds {
		point := model.TimeSeriesPoint{
			Time:          time.Unix(bucket, 0).UTC(),
			OutcomeCounts: make(map[model.ServiceOutcome]int64),
		}

		if stat, ok := dataMap[bucket]; ok {
			point = *stat
		}

		result = append(result, point)
	}

	return result, nil
}
