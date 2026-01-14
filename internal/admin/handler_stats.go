package admin

import (
	"context"
	"net/http"
	"time"

	"switch-a/internal/model"

	"go.uber.org/zap"
)

// validPeriods contains the allowed period values for stats API.
// Unexported to prevent external mutation.
var validPeriods = map[string]bool{
	"24h": true,
	"7d":  true,
	"30d": true,
	"all": true,
}

// validGranularities contains the allowed granularity values for stats API.
// Unexported to prevent external mutation.
var validGranularities = map[string]time.Duration{
	"5m":  5 * time.Minute,
	"15m": 15 * time.Minute,
	"1h":  time.Hour,
	"6h":  6 * time.Hour,
	"1d":  24 * time.Hour,
}

// minGranularityByPeriod defines the minimum allowed granularity for each period.
// This prevents excessive data points from large time ranges with small granularities.
// Unexported to prevent external mutation.
var minGranularityByPeriod = map[string]time.Duration{
	"24h": 5 * time.Minute, // 24h allows 5m minimum (288 points max)
	"7d":  time.Hour,       // 7d allows 1h minimum (168 points max)
	"30d": 6 * time.Hour,   // 30d allows 6h minimum (120 points max)
	"all": 24 * time.Hour,  // all allows 1d minimum
}

// periodToDuration converts a period string to time.Duration.
// Returns 0 for "all" to indicate no time limit.
func periodToDuration(period string) time.Duration {
	switch period {
	case "24h":
		return 24 * time.Hour
	case "7d":
		return 7 * 24 * time.Hour
	case "30d":
		return 30 * 24 * time.Hour
	default: // "all"
		return 0
	}
}

// formatGranularity converts a duration to a human-readable granularity string.
func formatGranularity(d time.Duration) string {
	switch d {
	case 5 * time.Minute:
		return "5m"
	case 15 * time.Minute:
		return "15m"
	case time.Hour:
		return "1h"
	case 6 * time.Hour:
		return "6h"
	case 24 * time.Hour:
		return "1d"
	default:
		return d.String()
	}
}

// statsParams holds validated parameters for the stats API.
type statsParams struct {
	period      string
	granularity time.Duration
}

// validateStatsParams validates and returns stats query parameters.
// Returns nil params and writes error response if validation fails.
func (h *Handler) validateStatsParams(w http.ResponseWriter, r *http.Request) *statsParams {
	period := r.URL.Query().Get("period")
	if period == "" {
		period = "24h"
	}
	if !validPeriods[period] {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "Invalid period: must be '24h', '7d', '30d', or 'all'")
		return nil
	}

	granularityStr := r.URL.Query().Get("granularity")
	var granularity time.Duration
	if granularityStr != "" {
		var ok bool
		granularity, ok = validGranularities[granularityStr]
		if !ok {
			writeError(w, http.StatusBadRequest, ErrCodeValidation, "Invalid granularity: must be '5m', '15m', '1h', '6h', or '1d'")
			return nil
		}

		minGranularity := minGranularityByPeriod[period]
		if granularity < minGranularity {
			writeError(w, http.StatusBadRequest, ErrCodeValidation,
				"Granularity too fine for period: "+period+" requires minimum granularity of "+formatGranularity(minGranularity))
			return nil
		}
	}

	return &statsParams{period: period, granularity: granularity}
}

// calculateProviderStats computes provider health statistics.
// Providers without explicit health state are counted as healthy.
func calculateProviderStats(providers []model.Provider, healthStates map[string]*model.HealthState) ProviderStats {
	stats := ProviderStats{Total: len(providers)}
	for i := range providers {
		p := &providers[i]
		if !p.Enabled {
			stats.Disabled++
			continue
		}
		if state, ok := healthStates[p.ID]; ok && state != nil && !state.Available {
			stats.Unhealthy++
		} else {
			stats.Healthy++
		}
	}
	return stats
}

// buildRequestsByProvider converts log stats to provider request stats with names.
func buildRequestsByProvider(logStats *model.LogStats, providerNameMap map[string]string) []ProviderRequestStats {
	result := make([]ProviderRequestStats, 0, len(logStats.ByProvider))
	for _, ps := range logStats.ByProvider {
		name := ps.ProviderID
		if n, ok := providerNameMap[ps.ProviderID]; ok {
			name = n
		}
		result = append(result, ProviderRequestStats{
			ID:          ps.ProviderID,
			Name:        name,
			Count:       ps.Count,
			SuccessRate: ps.SuccessRate,
		})
	}
	return result
}

// getTimeSeriesStartTime determines the start time for time series based on period and log stats.
func getTimeSeriesStartTime(period string, startTime time.Time, earliestLog time.Time, now time.Time) time.Time {
	if period != "all" {
		return startTime
	}
	if !earliestLog.IsZero() {
		return earliestLog
	}
	return now.Add(-DefaultTimeSeriesRangeDays * 24 * time.Hour)
}

// StatsResponse represents the response for the stats API.
type StatsResponse struct {
	TotalRequests      int64                   `json:"total_requests"`
	SuccessCount       int64                   `json:"success_count"`
	FailCount          int64                   `json:"fail_count"`
	SuccessRate        float64                 `json:"success_rate"`
	AvgLatencyMs       int64                   `json:"avg_latency_ms"`
	Providers          ProviderStats           `json:"providers"`
	RequestsByAPIType  map[string]int64        `json:"requests_by_api_type"`
	RequestsByProvider []ProviderRequestStats  `json:"requests_by_provider"`
	TimeRange          TimeRange               `json:"time_range"`
	TimeSeries         []model.TimeSeriesPoint `json:"timeseries,omitempty"`
}

// ProviderStats represents provider health statistics.
type ProviderStats struct {
	Total     int `json:"total"`
	Healthy   int `json:"healthy"`
	Unhealthy int `json:"unhealthy"`
	Disabled  int `json:"disabled"`
}

// ProviderRequestStats represents request statistics for a single provider.
type ProviderRequestStats struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Count       int64   `json:"count"`
	SuccessRate float64 `json:"success_rate"`
}

// TimeRange represents the time range for statistics.
type TimeRange struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// GetStats handles GET /admin/api/stats.
// Query parameters:
//   - period: statistics time range (24h/7d/30d/all, default: 24h)
//   - granularity: time bucket size for time series (5m/15m/1h/6h/1d, optional)
func (h *Handler) GetStats(w http.ResponseWriter, r *http.Request) {
	params := h.validateStatsParams(w, r)
	if params == nil {
		return
	}

	ctx := r.Context()
	now := time.Now().UTC()
	startTime, endTime := h.calculateTimeRange(params.period, now)

	logStats, err := h.store.GetLogStats(ctx, startTime, endTime)
	if err != nil {
		h.logger.Error("failed to get log stats", zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to get statistics")
		return
	}

	providerStats, providerNameMap, err := h.fetchProviderStats(ctx)
	if err != nil {
		h.logger.Error("failed to get provider stats", zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to get statistics")
		return
	}

	resp := h.buildStatsResponse(logStats, providerStats, providerNameMap, startTime, endTime, params.period)

	if params.granularity > 0 {
		tsStartTime := getTimeSeriesStartTime(params.period, startTime, logStats.EarliestLog, now)
		timeSeries, err := h.store.GetLogTimeSeries(ctx, tsStartTime, endTime, params.granularity)
		if err != nil {
			h.logger.Error("failed to get log time series", zap.Error(err))
			writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to get time series statistics")
			return
		}
		resp.TimeSeries = timeSeries
	}

	writeJSON(w, http.StatusOK, resp)
}

// calculateTimeRange returns start and end times based on period.
func (h *Handler) calculateTimeRange(period string, now time.Time) (time.Time, time.Time) {
	duration := periodToDuration(period)
	var startTime time.Time
	if duration > 0 {
		startTime = now.Add(-duration)
	}
	return startTime, now
}

// fetchProviderStats retrieves providers and calculates health statistics.
func (h *Handler) fetchProviderStats(ctx context.Context) (ProviderStats, map[string]string, error) {
	providers, err := h.store.ListProviders(ctx)
	if err != nil {
		return ProviderStats{}, nil, err
	}

	providerIDs := make([]string, len(providers))
	providerNameMap := make(map[string]string, len(providers))
	for i := range providers {
		providerIDs[i] = providers[i].ID
		providerNameMap[providers[i].ID] = providers[i].Name
	}

	healthStates, err := h.store.GetHealthStatesByProviderIDs(ctx, providerIDs)
	if err != nil {
		h.logger.Warn("failed to batch fetch health states", zap.Error(err))
	}

	return calculateProviderStats(providers, healthStates), providerNameMap, nil
}

// buildStatsResponse constructs the stats response from collected data.
func (h *Handler) buildStatsResponse(
	logStats *model.LogStats,
	providerStats ProviderStats,
	providerNameMap map[string]string,
	startTime, endTime time.Time,
	period string,
) StatsResponse {
	resp := StatsResponse{
		TotalRequests:      logStats.TotalRequests,
		SuccessCount:       logStats.SuccessCount,
		FailCount:          logStats.FailCount,
		SuccessRate:        logStats.SuccessRate,
		AvgLatencyMs:       logStats.AvgLatencyMs,
		Providers:          providerStats,
		RequestsByAPIType:  logStats.ByAPIType,
		RequestsByProvider: buildRequestsByProvider(logStats, providerNameMap),
		TimeRange:          TimeRange{Start: startTime, End: endTime},
	}

	if period == "all" && !logStats.EarliestLog.IsZero() {
		resp.TimeRange.Start = logStats.EarliestLog
	}

	return resp
}
