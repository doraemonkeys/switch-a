package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"switch-a/internal/model"

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
	if filter.Success != nil {
		query = query.Where("success = ?", *filter.Success)
	}
	if filter.IsSSE != nil {
		query = query.Where("is_sse = ?", *filter.IsSSE)
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

// GetLogStats retrieves aggregated statistics from request logs within the given time range.
// If startTime is zero, all logs are included (for "all" period).
func (s *SQLiteStore) GetLogStats(ctx context.Context, startTime, endTime time.Time) (*model.LogStats, error) {
	stats := &model.LogStats{
		ByAPIType:  make(map[string]int64),
		ByProvider: []model.ProviderLogStats{},
	}

	// Build base query with time filter
	baseQuery := s.db.WithContext(ctx).Model(&model.RequestLog{})
	if !startTime.IsZero() {
		baseQuery = baseQuery.Where("created_at >= ?", startTime)
	}
	baseQuery = baseQuery.Where("created_at < ?", endTime)

	// Get overall statistics using a single query
	var overallStats struct {
		TotalRequests int64
		SuccessCount  int64
		AvgLatencyMs  float64
	}
	err := baseQuery.Session(&gorm.Session{}).Select(
		"COUNT(*) as total_requests",
		"SUM(CASE WHEN success = 1 THEN 1 ELSE 0 END) as success_count",
		"COALESCE(AVG(latency_ms), 0) as avg_latency_ms",
	).Scan(&overallStats).Error
	if err != nil {
		return nil, fmt.Errorf("get overall log stats: %w", err)
	}

	stats.TotalRequests = overallStats.TotalRequests
	stats.SuccessCount = overallStats.SuccessCount
	stats.FailCount = overallStats.TotalRequests - overallStats.SuccessCount
	stats.AvgLatencyMs = int64(overallStats.AvgLatencyMs)

	// Calculate success rate
	if stats.TotalRequests > 0 {
		stats.SuccessRate = float64(stats.SuccessCount) / float64(stats.TotalRequests)
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

	// Get statistics by provider
	type providerStat struct {
		ProviderID   string
		Count        int64
		SuccessCount int64
	}
	var providerStats []providerStat
	err = baseQuery.Session(&gorm.Session{}).
		Select(
			"provider_id",
			"COUNT(*) as count",
			"SUM(CASE WHEN success = 1 THEN 1 ELSE 0 END) as success_count",
		).
		Group("provider_id").
		Order("count DESC").
		Scan(&providerStats).Error
	if err != nil {
		return nil, fmt.Errorf("get provider stats: %w", err)
	}
	for _, stat := range providerStats {
		successRate := float64(0)
		if stat.Count > 0 {
			successRate = float64(stat.SuccessCount) / float64(stat.Count)
		}
		stats.ByProvider = append(stats.ByProvider, model.ProviderLogStats{
			ProviderID:   stat.ProviderID,
			Count:        stat.Count,
			SuccessCount: stat.SuccessCount,
			SuccessRate:  successRate,
		})
	}

	// Get earliest log timestamp (for "all" period display)
	if startTime.IsZero() {
		var earliestLog model.RequestLog
		err = s.db.WithContext(ctx).Model(&model.RequestLog{}).
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
	type bucketStat struct {
		BucketTime   int64
		Requests     int64
		SuccessCount int64
		TotalLatency int64
	}
	var bucketStats []bucketStat

	// Build the query
	query := s.db.WithContext(ctx).Model(&model.RequestLog{}).
		Select(
			fmt.Sprintf("(CAST(strftime('%%s', created_at) AS INTEGER) / %d) * %d as bucket_time", granularitySeconds, granularitySeconds),
			"COUNT(*) as requests",
			"SUM(CASE WHEN success = 1 THEN 1 ELSE 0 END) as success_count",
			"COALESCE(SUM(latency_ms), 0) as total_latency",
		).
		Where("created_at >= ? AND created_at < ?", startTime, endTime).
		Group("bucket_time").
		Order("bucket_time ASC")

	if err := query.Scan(&bucketStats).Error; err != nil {
		return nil, fmt.Errorf("get log time series: %w", err)
	}

	// Create a map of existing data points for quick lookup
	dataMap := make(map[int64]bucketStat, len(bucketStats))
	for _, stat := range bucketStats {
		dataMap[stat.BucketTime] = stat
	}

	// Generate all time buckets and fill with data or zeros
	var result []model.TimeSeriesPoint
	startBucket := (startTime.Unix() / granularitySeconds) * granularitySeconds
	endBucket := (endTime.Unix() / granularitySeconds) * granularitySeconds

	for bucket := startBucket; bucket < endBucket; bucket += granularitySeconds {
		point := model.TimeSeriesPoint{
			Time: time.Unix(bucket, 0).UTC(),
		}

		if stat, ok := dataMap[bucket]; ok {
			point.Requests = stat.Requests
			point.SuccessCount = stat.SuccessCount
			point.FailCount = stat.Requests - stat.SuccessCount
			if stat.Requests > 0 {
				point.SuccessRate = float64(stat.SuccessCount) / float64(stat.Requests)
				point.AvgLatencyMs = stat.TotalLatency / stat.Requests
			}
		}

		result = append(result, point)
	}

	return result, nil
}
