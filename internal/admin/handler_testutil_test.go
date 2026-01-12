package admin

import (
	"context"
	"net/http"

	"switch-a/internal/model"
	"switch-a/internal/store"

	"go.uber.org/zap"
)

// mockStore implements Store interface for testing.
type mockStore struct {
	providers    map[string]*model.Provider
	groups       map[string]*model.Group
	healthStates map[string]*model.HealthState
	config       map[string]string
	logs         []model.RequestLog
	listErr      error
	getErr       error
	createErr    error
	updateErr    error
	deleteErr    error
	configErr    error
	logsErr      error
	healthErr    error
}

func newMockStore() *mockStore {
	return &mockStore{
		providers:    make(map[string]*model.Provider),
		groups:       make(map[string]*model.Group),
		healthStates: make(map[string]*model.HealthState),
		config:       make(map[string]string),
		logs:         []model.RequestLog{},
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

func (m *mockStore) ListLogs(_ context.Context, limit, offset int) ([]model.RequestLog, error) {
	if m.logsErr != nil {
		return nil, m.logsErr
	}
	if offset >= len(m.logs) {
		return []model.RequestLog{}, nil
	}
	end := offset + limit
	if end > len(m.logs) {
		end = len(m.logs)
	}
	return m.logs[offset:end], nil
}

func (m *mockStore) CountLogs(_ context.Context) (int64, error) {
	if m.logsErr != nil {
		return 0, m.logsErr
	}
	return int64(len(m.logs)), nil
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

func testHandler() (*Handler, *mockStore, *mockHealthManager) {
	logger, _ := zap.NewDevelopment()
	st := newMockStore()
	health := &mockHealthManager{}
	h := NewHandler(Config{
		Store:       st,
		Health:      health,
		Concurrency: &mockConcurrencyTracker{},
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
