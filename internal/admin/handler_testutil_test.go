package admin

import (
	"context"
	"net/http"
	"sort"
	"time"

	"switch-a/internal/model"
	"switch-a/internal/store"

	"go.uber.org/zap"
)

// mockStore implements Store interface for testing.
type mockStore struct {
	providers           map[string]*model.Provider
	routingPolicies     map[uint]*model.RoutingPolicy
	nextRoutingPolicyID uint
	groups              map[string]*model.Group
	healthStates        map[string]*model.HealthState
	config              map[string]string
	logs                []model.RequestLog
	attempts            map[string][]model.RequestAttempt // Keyed by request_id for GetAttemptsByRequestID
	listErr             error
	getErr              error
	createErr           error
	updateErr           error
	deleteErr           error
	configErr           error
	logsErr             error
	healthErr           error
	attemptsErr         error // Separate error field for attempts operations
}

func newMockStore() *mockStore {
	return &mockStore{
		providers:       make(map[string]*model.Provider),
		routingPolicies: make(map[uint]*model.RoutingPolicy),
		groups:          make(map[string]*model.Group),
		healthStates:    make(map[string]*model.HealthState),
		config:          make(map[string]string),
		logs:            []model.RequestLog{},
		attempts:        make(map[string][]model.RequestAttempt),
	}
}

func (m *mockStore) ListProviders(_ context.Context) ([]model.Provider, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	result := make([]model.Provider, 0, len(m.providers))
	for _, p := range m.providers {
		result = append(result, *p)
	}
	return result, nil
}

func (m *mockStore) GetProvider(_ context.Context, id string) (*model.Provider, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	if p, ok := m.providers[id]; ok {
		return p, nil
	}
	return nil, store.ErrNotFound
}

func (m *mockStore) CreateProvider(_ context.Context, p *model.Provider) error {
	if m.createErr != nil {
		return m.createErr
	}
	m.providers[p.ID] = p
	return nil
}

func (m *mockStore) UpdateProvider(_ context.Context, p *model.Provider) error {
	if m.updateErr != nil {
		return m.updateErr
	}
	m.providers[p.ID] = p
	return nil
}

func (m *mockStore) DeleteProvider(_ context.Context, id string) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	delete(m.providers, id)
	return nil
}

func (m *mockStore) ListRoutingPolicies(_ context.Context) ([]model.RoutingPolicy, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	ids := make([]int, 0, len(m.routingPolicies))
	for id := range m.routingPolicies {
		ids = append(ids, int(id))
	}
	sort.Ints(ids)
	result := make([]model.RoutingPolicy, 0, len(ids))
	for _, id := range ids {
		result = append(result, *cloneRoutingPolicy(m.routingPolicies[uint(id)]))
	}
	return result, nil
}

func (m *mockStore) GetRoutingPolicy(_ context.Context, id uint) (*model.RoutingPolicy, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	if policy, ok := m.routingPolicies[id]; ok {
		return cloneRoutingPolicy(policy), nil
	}
	return nil, store.ErrNotFound
}

func (m *mockStore) CreateRoutingPolicy(_ context.Context, policy *model.RoutingPolicy) error {
	if m.createErr != nil {
		return m.createErr
	}
	created := cloneRoutingPolicy(policy)
	if created.ID == 0 {
		m.nextRoutingPolicyID++
		created.ID = m.nextRoutingPolicyID
	}
	if created.ID > m.nextRoutingPolicyID {
		m.nextRoutingPolicyID = created.ID
	}
	m.routingPolicies[created.ID] = created
	policy.ID = created.ID
	return nil
}

func (m *mockStore) UpdateRoutingPolicy(_ context.Context, policy *model.RoutingPolicy) error {
	if m.updateErr != nil {
		return m.updateErr
	}
	if _, ok := m.routingPolicies[policy.ID]; !ok {
		return store.ErrNotFound
	}
	m.routingPolicies[policy.ID] = cloneRoutingPolicy(policy)
	return nil
}

func (m *mockStore) DeleteRoutingPolicy(_ context.Context, id uint) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	if _, ok := m.routingPolicies[id]; !ok {
		return store.ErrNotFound
	}
	delete(m.routingPolicies, id)
	return nil
}

func (m *mockStore) ListGroups(_ context.Context) ([]model.Group, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	result := make([]model.Group, 0, len(m.groups))
	for _, g := range m.groups {
		result = append(result, *g)
	}
	return result, nil
}

func (m *mockStore) GetGroup(_ context.Context, id string) (*model.Group, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	if g, ok := m.groups[id]; ok {
		return g, nil
	}
	return nil, store.ErrNotFound
}

func (m *mockStore) CreateGroup(_ context.Context, g *model.Group) error {
	if m.createErr != nil {
		return m.createErr
	}
	m.groups[g.ID] = g
	return nil
}

func (m *mockStore) UpdateGroup(_ context.Context, g *model.Group) error {
	if m.updateErr != nil {
		return m.updateErr
	}
	m.groups[g.ID] = g
	return nil
}

func (m *mockStore) DeleteGroup(_ context.Context, id string) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	delete(m.groups, id)
	return nil
}

func (m *mockStore) GetHealthState(_ context.Context, providerID string) (*model.HealthState, error) {
	if m.healthErr != nil {
		return nil, m.healthErr
	}
	if hs, ok := m.healthStates[providerID]; ok {
		return hs, nil
	}
	return &model.HealthState{
		ProviderID: providerID,
		Available:  true,
	}, nil
}

func (m *mockStore) GetHealthStatesByProviderIDs(_ context.Context, providerIDs []string) (map[string]*model.HealthState, error) {
	if m.healthErr != nil {
		return nil, m.healthErr
	}
	result := make(map[string]*model.HealthState, len(providerIDs))
	for _, id := range providerIDs {
		if hs, ok := m.healthStates[id]; ok {
			result[id] = hs
		} else {
			result[id] = &model.HealthState{
				ProviderID: id,
				Available:  true,
			}
		}
	}
	return result, nil
}

func (m *mockStore) ListHealthStates(_ context.Context) ([]model.HealthState, error) {
	if m.healthErr != nil {
		return nil, m.healthErr
	}
	result := make([]model.HealthState, 0, len(m.healthStates))
	for _, hs := range m.healthStates {
		result = append(result, *hs)
	}
	return result, nil
}

func (m *mockStore) GetAllConfig(_ context.Context) (map[string]string, error) {
	if m.configErr != nil {
		return nil, m.configErr
	}
	result := make(map[string]string, len(m.config))
	for k, v := range m.config {
		result[k] = v
	}
	return result, nil
}

func (m *mockStore) SetConfig(_ context.Context, key, value string) error {
	if m.configErr != nil {
		return m.configErr
	}
	m.config[key] = value
	return nil
}

func (m *mockStore) SetConfigs(_ context.Context, configs map[string]string) error {
	if m.configErr != nil {
		return m.configErr
	}
	for key, value := range configs {
		m.config[key] = value
	}
	return nil
}

// matchesFilter checks if a log entry matches the given filter criteria.
func matchesFilter(log model.RequestLog, filter model.LogFilter) bool {
	if filter.ProviderID != "" && log.ProviderID != filter.ProviderID {
		return false
	}
	if filter.APIType != "" && log.APIType != filter.APIType {
		return false
	}
	if filter.Success != nil && log.Success != *filter.Success {
		return false
	}
	if filter.IsSSE != nil && log.IsSSE != *filter.IsSSE {
		return false
	}
	if filter.IsWebSocket != nil && log.IsWebSocket != *filter.IsWebSocket {
		return false
	}
	if filter.HasWebSocketLifecycleFilter() && !log.IsWebSocket {
		return false
	}
	if filter.StickyWritten != nil {
		if log.StickyWritten == nil || *log.StickyWritten != *filter.StickyWritten {
			return false
		}
	}
	if filter.SessionCommitted != nil {
		if log.SessionCommitted == nil || *log.SessionCommitted != *filter.SessionCommitted {
			return false
		}
	}
	if filter.ProbeOutcome != "" {
		if log.ProbeOutcome == nil || *log.ProbeOutcome != filter.ProbeOutcome {
			return false
		}
	}
	if filter.TerminalCause != "" {
		if log.TerminalCause == nil || *log.TerminalCause != filter.TerminalCause {
			return false
		}
	}
	if filter.UserID != "" && log.UserID != filter.UserID {
		return false
	}
	if filter.StartTime != nil && log.CreatedAt.Before(*filter.StartTime) {
		return false
	}
	if filter.EndTime != nil && !log.CreatedAt.Before(*filter.EndTime) {
		return false
	}
	if filter.MinLatency != nil && log.LatencyMs < *filter.MinLatency {
		return false
	}
	if filter.MinRetryCount != nil && log.RetryCount < *filter.MinRetryCount {
		return false
	}
	if filter.HasRetries != nil {
		if *filter.HasRetries && log.RetryCount == 0 {
			return false
		}
		if !*filter.HasRetries && log.RetryCount > 0 {
			return false
		}
	}
	return true
}

func (m *mockStore) ListLogs(_ context.Context, filter model.LogFilter) ([]model.RequestLog, error) {
	if m.logsErr != nil {
		return nil, m.logsErr
	}

	// Apply filters to logs
	var filtered []model.RequestLog
	for _, log := range m.logs {
		if matchesFilter(log, filter) {
			filtered = append(filtered, log)
		}
	}

	// Apply pagination
	offset := filter.Offset
	limit := filter.Limit
	if offset >= len(filtered) {
		return []model.RequestLog{}, nil
	}
	end := offset + limit
	if end > len(filtered) {
		end = len(filtered)
	}
	return filtered[offset:end], nil
}

func (m *mockStore) CountLogs(_ context.Context, filter model.LogFilter) (int64, error) {
	if m.logsErr != nil {
		return 0, m.logsErr
	}

	// Apply filters to logs
	count := int64(0)
	for _, log := range m.logs {
		if matchesFilter(log, filter) {
			count++
		}
	}
	return count, nil
}

func (m *mockStore) GetLogStats(_ context.Context, startTime, endTime time.Time) (*model.LogStats, error) {
	if m.logsErr != nil {
		return nil, m.logsErr
	}

	stats := &model.LogStats{
		ByAPIType:  make(map[string]int64),
		ByProvider: []model.ProviderLogStats{},
	}

	// Filter and aggregate logs
	providerStats := make(map[string]*model.ProviderLogStats)
	var totalLatency int64
	for _, log := range m.logs {
		if !startTime.IsZero() && log.CreatedAt.Before(startTime) {
			continue
		}
		if log.CreatedAt.After(endTime) || log.CreatedAt.Equal(endTime) {
			continue
		}

		stats.TotalRequests++
		totalLatency += log.LatencyMs
		if log.Success {
			stats.SuccessCount++
		} else {
			stats.FailCount++
		}
		stats.ByAPIType[log.APIType]++

		// Aggregate by provider
		ps, ok := providerStats[log.ProviderID]
		if !ok {
			ps = &model.ProviderLogStats{ProviderID: log.ProviderID}
			providerStats[log.ProviderID] = ps
		}
		ps.Count++
		if log.Success {
			ps.SuccessCount++
		}
	}

	// Calculate success rates and average latency
	if stats.TotalRequests > 0 {
		stats.SuccessRate = float64(stats.SuccessCount) / float64(stats.TotalRequests)
		stats.AvgLatencyMs = totalLatency / stats.TotalRequests
	}
	for _, ps := range providerStats {
		if ps.Count > 0 {
			ps.SuccessRate = float64(ps.SuccessCount) / float64(ps.Count)
		}
		stats.ByProvider = append(stats.ByProvider, *ps)
	}

	return stats, nil
}

func (m *mockStore) GetLogTimeSeries(_ context.Context, startTime, endTime time.Time, granularity time.Duration) ([]model.TimeSeriesPoint, error) {
	if m.logsErr != nil {
		return nil, m.logsErr
	}

	granularitySeconds := int64(granularity.Seconds())

	// Create buckets map
	buckets := make(map[int64]*model.TimeSeriesPoint)

	// Filter logs and aggregate into buckets
	for _, log := range m.logs {
		if !startTime.IsZero() && log.CreatedAt.Before(startTime) {
			continue
		}
		if log.CreatedAt.After(endTime) || log.CreatedAt.Equal(endTime) {
			continue
		}

		bucketTime := (log.CreatedAt.Unix() / granularitySeconds) * granularitySeconds
		bucket, ok := buckets[bucketTime]
		if !ok {
			bucket = &model.TimeSeriesPoint{
				Time: time.Unix(bucketTime, 0).UTC(),
			}
			buckets[bucketTime] = bucket
		}

		bucket.Requests++
		bucket.AvgLatencyMs += log.LatencyMs // Will divide later
		if log.Success {
			bucket.SuccessCount++
		} else {
			bucket.FailCount++
		}
	}

	// Calculate averages for existing buckets
	for _, bucket := range buckets {
		if bucket.Requests > 0 {
			bucket.SuccessRate = float64(bucket.SuccessCount) / float64(bucket.Requests)
			bucket.AvgLatencyMs = bucket.AvgLatencyMs / bucket.Requests
		}
	}

	// Generate all time buckets and fill with data or zeros
	var result []model.TimeSeriesPoint
	startBucket := (startTime.Unix() / granularitySeconds) * granularitySeconds
	endBucket := (endTime.Unix() / granularitySeconds) * granularitySeconds

	for bucket := startBucket; bucket < endBucket; bucket += granularitySeconds {
		if point, ok := buckets[bucket]; ok {
			result = append(result, *point)
		} else {
			result = append(result, model.TimeSeriesPoint{
				Time: time.Unix(bucket, 0).UTC(),
			})
		}
	}

	return result, nil
}

func (m *mockStore) GetLogByID(_ context.Context, id uint) (*model.RequestLog, error) {
	if m.logsErr != nil {
		return nil, m.logsErr
	}
	for _, log := range m.logs {
		if log.ID == id {
			return &log, nil
		}
	}
	// Return ErrNotFound to match the actual SQLiteStore implementation behavior.
	// This ensures tests properly validate handler error handling paths.
	return nil, store.ErrNotFound
}

func (m *mockStore) GetAttemptsByRequestID(_ context.Context, requestID string) ([]model.RequestAttempt, error) {
	if m.attemptsErr != nil {
		return nil, m.attemptsErr
	}
	if attempts, ok := m.attempts[requestID]; ok {
		return attempts, nil
	}
	return nil, nil
}

// mockHealthManager implements HealthManager interface for testing.
type mockHealthManager struct {
	disableErr error
	enableErr  error
}

func (m *mockHealthManager) MarkSuccess(_ context.Context, _ string) {}

func (m *mockHealthManager) MarkFailure(_ context.Context, _ string, _ error) bool {
	return false
}

func (m *mockHealthManager) RecoverIfExpired(_ context.Context, _ string) bool {
	return false
}

func (m *mockHealthManager) IsAvailable(_ context.Context, _ string) bool {
	return true
}

func (m *mockHealthManager) ManualDisable(_ context.Context, _ string, _ string) error {
	return m.disableErr
}

func (m *mockHealthManager) SuspendUntil(_ context.Context, _ string, _ time.Time, _ string) error {
	return nil
}

func (m *mockHealthManager) ManualEnable(_ context.Context, _ string) error {
	return m.enableErr
}

func (m *mockHealthManager) ResetCircuitBreaker(_ string) {
	// No-op for testing
}

// mockConcurrencyTracker implements ConcurrencyTracker interface for testing.
type mockConcurrencyTracker struct {
	counts map[string]int64
}

func (m *mockConcurrencyTracker) Current(providerID string) int64 {
	if m.counts == nil {
		return 0
	}
	return m.counts[providerID]
}

// mockConcurrencyCleaner implements ConcurrencyCleaner interface for testing.
type mockConcurrencyCleaner struct {
	cleared []string
}

func (m *mockConcurrencyCleaner) ClearConcurrency(providerID string) {
	m.cleared = append(m.cleared, providerID)
}

func testHandler() (*Handler, *mockStore, *mockHealthManager) {
	logger, _ := zap.NewDevelopment()
	st := newMockStore()
	health := &mockHealthManager{}
	h := NewHandler(Config{
		Store:       st,
		Health:      health,
		Concurrency: &mockConcurrencyTracker{},
		Cleaner:     &mockConcurrencyCleaner{},
		Logger:      logger,
	})
	return h, st, health
}

// setPathValue sets a path value on a request (Go 1.22+ feature).
func setPathValue(r *http.Request, key, value string) {
	r.SetPathValue(key, value)
}

// configErrorStore is a specialized mock for testing config error paths.
// Note: configData is intentionally named differently from mockStore.config
// to avoid field shadowing and make it clear this is specialized behavior.
type configErrorStore struct {
	mockStore
	configData map[string]string
	setErr     error
	getErr     error
	afterSet   bool
	setCalls   int
}

// newConfigErrorStore creates a new configErrorStore with initialized maps.
func newConfigErrorStore() *configErrorStore {
	return &configErrorStore{
		mockStore:  *newMockStore(),
		configData: make(map[string]string),
	}
}

func cloneRoutingPolicy(policy *model.RoutingPolicy) *model.RoutingPolicy {
	if policy == nil {
		return nil
	}
	clone := *policy
	clone.Groups = append([]model.RoutingPolicyGroup(nil), policy.Groups...)
	clone.Vendors = append([]model.RoutingPolicyVendor(nil), policy.Vendors...)
	return &clone
}

func (s *configErrorStore) SetConfig(_ context.Context, key, value string) error {
	s.setCalls++
	if s.setErr != nil {
		return s.setErr
	}
	s.configData[key] = value
	return nil
}

func (s *configErrorStore) SetConfigs(_ context.Context, configs map[string]string) error {
	s.setCalls++
	if s.setErr != nil {
		return s.setErr
	}
	for key, value := range configs {
		s.configData[key] = value
	}
	return nil
}

func (s *configErrorStore) GetAllConfig(_ context.Context) (map[string]string, error) {
	if s.afterSet && s.setCalls > 0 && s.getErr != nil {
		return nil, s.getErr
	}
	result := make(map[string]string, len(s.configData))
	for k, v := range s.configData {
		result[k] = v
	}
	return result, nil
}
