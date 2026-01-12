package admin

import (
	"net/http"
	"time"

	"switch-a/internal/model"

	"go.uber.org/zap"
)

// ValidPeriods contains the allowed period values for stats API.
var ValidPeriods = map[string]bool{
	"24h": true,
	"7d":  true,
	"30d": true,
	"all": true,
}

// ValidGranularities contains the allowed granularity values for stats API.
var ValidGranularities = map[string]time.Duration{
	"5m":  5 * time.Minute,
	"15m": 15 * time.Minute,
	"1h":  time.Hour,
	"6h":  6 * time.Hour,
	"1d":  24 * time.Hour,
}

// MinGranularityByPeriod defines the minimum allowed granularity for each period.
// This prevents excessive data points from large time ranges with small granularities.
var MinGranularityByPeriod = map[string]time.Duration{
	"24h": 5 * time.Minute,  // 24h allows 5m minimum (288 points max)
	"7d":  time.Hour,        // 7d allows 1h minimum (168 points max)
	"30d": 6 * time.Hour,    // 30d allows 6h minimum (120 points max)
	"all": 24 * time.Hour,   // all allows 1d minimum
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

// StatsResponse represents the response for the stats API.
type StatsResponse struct {
	TotalRequests      int64                    `json:"total_requests"`
	SuccessCount       int64                    `json:"success_count"`
	FailCount          int64                    `json:"fail_count"`
	SuccessRate        float64                  `json:"success_rate"`
	AvgLatencyMs       int64                    `json:"avg_latency_ms"`
	Providers          ProviderStats            `json:"providers"`
	RequestsByAPIType  map[string]int64         `json:"requests_by_api_type"`
	RequestsByProvider []ProviderRequestStats   `json:"requests_by_provider"`
	TimeRange          TimeRange                `json:"time_range"`
	TimeSeries         []model.TimeSeriesPoint  `json:"timeseries,omitempty"`
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
	// Parse and validate period parameter
	period := r.URL.Query().Get("period")
	if period == "" {
		period = "24h"
	}
	if !ValidPeriods[period] {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "Invalid period: must be '24h', '7d', '30d', or 'all'")
		return
	}

	// Parse and validate granularity parameter
	granularityStr := r.URL.Query().Get("granularity")
	var granularity time.Duration
	if granularityStr != "" {
		var ok bool
		granularity, ok = ValidGranularities[granularityStr]
		if !ok {
			writeError(w, http.StatusBadRequest, ErrCodeValidation, "Invalid granularity: must be '5m', '15m', '1h', '6h', or '1d'")
			return
		}

		// Check granularity limit for the period
		minGranularity := MinGranularityByPeriod[period]
		if granularity < minGranularity {
			writeError(w, http.StatusBadRequest, ErrCodeValidation,
				"Granularity too fine for period: "+period+" requires minimum granularity of "+formatGranularity(minGranularity))
			return
		}
	}

	ctx := r.Context()
	now := time.Now().UTC()

	// Calculate time range
	var startTime time.Time
	duration := periodToDuration(period)
	if duration > 0 {
		startTime = now.Add(-duration)
	} else {
		// For "all", use a very old date
		startTime = time.Time{}
	}
	endTime := now

	// Get log statistics from store
	logStats, err := h.store.GetLogStats(ctx, startTime, endTime)
	if err != nil {
		h.logger.Error("failed to get log stats", zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to get statistics")
		return
	}

	// Get provider statistics
	providers, err := h.store.ListProviders(ctx)
	if err != nil {
		h.logger.Error("failed to list providers", zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to get statistics")
		return
	}

	// Collect provider IDs for batch health state fetch
	providerIDs := make([]string, len(providers))
	providerNameMap := make(map[string]string, len(providers))
	for i := range providers {
		providerIDs[i] = providers[i].ID
		providerNameMap[providers[i].ID] = providers[i].Name
	}

	// Get health states for all providers
	healthStates, err := h.store.GetHealthStatesByProviderIDs(ctx, providerIDs)
	if err != nil {
		h.logger.Warn("failed to batch fetch health states", zap.Error(err))
	}

	// Calculate provider health statistics.
	// Design decision: Providers without explicit health state are counted as healthy.
	// This is because health states are only created when a provider experiences failures,
	// so a missing health state indicates the provider has never failed and should be
	// considered healthy. This avoids false negatives for newly added providers.
	providerStats := ProviderStats{
		Total: len(providers),
	}
	for _, p := range providers {
		if !p.Enabled {
			providerStats.Disabled++
			continue
		}
		if state, ok := healthStates[p.ID]; ok && state != nil {
			if state.Available {
				providerStats.Healthy++
			} else {
				providerStats.Unhealthy++
			}
		} else {
			// No health state found - provider is assumed healthy (see design note above)
			providerStats.Healthy++
		}
	}

	// Build requests by provider with names
	requestsByProvider := make([]ProviderRequestStats, 0, len(logStats.ByProvider))
	for _, ps := range logStats.ByProvider {
		name := ps.ProviderID
		if n, ok := providerNameMap[ps.ProviderID]; ok {
			name = n
		}
		requestsByProvider = append(requestsByProvider, ProviderRequestStats{
			ID:          ps.ProviderID,
			Name:        name,
			Count:       ps.Count,
			SuccessRate: ps.SuccessRate,
		})
	}

	// Build response
	resp := StatsResponse{
		TotalRequests:      logStats.TotalRequests,
		SuccessCount:       logStats.SuccessCount,
		FailCount:          logStats.FailCount,
		SuccessRate:        logStats.SuccessRate,
		AvgLatencyMs:       logStats.AvgLatencyMs,
		Providers:          providerStats,
		RequestsByAPIType:  logStats.ByAPIType,
		RequestsByProvider: requestsByProvider,
		TimeRange: TimeRange{
			Start: startTime,
			End:   endTime,
		},
	}

	// For "all" period, adjust start time to earliest log if available
	if period == "all" && !logStats.EarliestLog.IsZero() {
		resp.TimeRange.Start = logStats.EarliestLog
	}

	// Fetch time series data if granularity is specified
	if granularity > 0 {
		tsStartTime := startTime
		// For "all" period with time series, use earliest log time or default fallback
		if period == "all" {
			if !logStats.EarliestLog.IsZero() {
				tsStartTime = logStats.EarliestLog
			} else {
				tsStartTime = now.Add(-DefaultTimeSeriesRangeDays * 24 * time.Hour)
			}
		}

		timeSeries, err := h.store.GetLogTimeSeries(ctx, tsStartTime, endTime, granularity)
		if err != nil {
			h.logger.Error("failed to get log time series", zap.Error(err))
			writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to get time series statistics")
			return
		}
		resp.TimeSeries = timeSeries
	}

	writeJSON(w, http.StatusOK, resp)
}
