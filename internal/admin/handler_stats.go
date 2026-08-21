package admin

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/doraemonkeys/switch-a/internal/analyticswindow"
	"github.com/doraemonkeys/switch-a/internal/model"

	"go.uber.org/zap"
)

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

// buildRequestsByProviderOutcome converts log stats to provider outcome stats
// with provider names attached for the admin response.
func buildRequestsByProviderOutcome(logStats *model.LogStats, providerNameMap map[string]string) []ProviderRequestStats {
	result := make([]ProviderRequestStats, 0, len(logStats.ByProvider))
	for _, ps := range logStats.ByProvider {
		name := ps.ProviderID
		if n, ok := providerNameMap[ps.ProviderID]; ok {
			name = n
		}
		result = append(result, ProviderRequestStats{
			ID:            ps.ProviderID,
			Name:          name,
			TotalRequests: ps.Count,
			OutcomeCounts: ps.OutcomeCounts,
		})
	}
	return result
}

// StatsResponse represents the response for the stats API.
type StatsResponse struct {
	TotalRequests             int64                          `json:"total_requests"`
	AvgLatencyMs              int64                          `json:"avg_latency_ms"`
	OutcomeCounts             map[model.ServiceOutcome]int64 `json:"outcome_counts"`
	Providers                 ProviderStats                  `json:"providers"`
	RequestsByAPIType         map[string]int64               `json:"requests_by_api_type"`
	RequestsByProviderOutcome []ProviderRequestStats         `json:"requests_by_provider_outcome"`
	TimeRange                 TimeRange                      `json:"time_range"`
	OutcomeTimeSeries         []model.TimeSeriesPoint        `json:"outcome_timeseries"`
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
	ID            string                         `json:"id"`
	Name          string                         `json:"name"`
	TotalRequests int64                          `json:"total_requests"`
	OutcomeCounts map[model.ServiceOutcome]int64 `json:"outcome_counts"`
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
//   - as_of: RFC3339 exclusive end shared with token analytics for boundary alignment
func (h *Handler) GetStats(w http.ResponseWriter, r *http.Request) {
	window, err := h.statsWindowResolver.ResolveStats(r.URL.Query())
	if err != nil {
		h.writeStatsWindowError(w, err)
		return
	}

	ctx := r.Context()
	logStats, err := h.store.GetLogStats(ctx, window.Start, window.End)
	if err != nil {
		h.logger.Error("failed to get log stats", zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to get statistics")
		return
	}
	earliest := timePointer(logStats.EarliestLog)
	window, err = analyticswindow.ResolveAll(window, earliest)
	if err != nil {
		h.writeStatsWindowError(w, err)
		return
	}

	providerStats, providerNameMap, err := h.fetchProviderStats(ctx)
	if err != nil {
		h.logger.Error("failed to get provider stats", zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to get statistics")
		return
	}

	resp := h.buildStatsResponse(logStats, providerStats, providerNameMap, window.Start, window.End)

	if window.Granularity > 0 {
		timeSeries, err := h.store.GetLogTimeSeries(ctx, window.Start, window.End, window.Granularity)
		if err != nil {
			h.logger.Error("failed to get log time series", zap.Error(err))
			writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to get time series statistics")
			return
		}
		if timeSeries == nil {
			timeSeries = []model.TimeSeriesPoint{}
		}
		resp.OutcomeTimeSeries = timeSeries
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) writeStatsWindowError(w http.ResponseWriter, err error) {
	var validationErr *analyticswindow.ValidationError
	if !errors.As(err, &validationErr) {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "Invalid analytics window")
		return
	}
	writeErrorWithDetails(w, http.StatusBadRequest, ErrCodeValidation, "Invalid analytics window", map[string]string{
		validationErr.Field: validationErr.Reason,
	})
}

func timePointer(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	return &value
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
) StatsResponse {
	resp := StatsResponse{
		TotalRequests:             logStats.TotalRequests,
		AvgLatencyMs:              logStats.AvgLatencyMs,
		OutcomeCounts:             logStats.OutcomeCounts,
		Providers:                 providerStats,
		RequestsByAPIType:         logStats.ByAPIType,
		RequestsByProviderOutcome: buildRequestsByProviderOutcome(logStats, providerNameMap),
		TimeRange:                 TimeRange{Start: startTime, End: endTime},
		OutcomeTimeSeries:         []model.TimeSeriesPoint{},
	}

	return resp
}
