package selector

import (
	"context"
	"fmt"
	"math"
	"time"

	"switch-a/internal"
	"switch-a/internal/defaults"
	"switch-a/internal/model"

	"go.uber.org/zap"
)

// Compile-time interface check.
var _ internal.Selector = (*Selector)(nil)

// DefaultStickyTTLSeconds is the canonical default TTL for sticky sessions in seconds.
// Derived from the centralized defaults package.
const DefaultStickyTTLSeconds = defaults.StickyTTLSeconds

// DefaultStickyTTL is the default TTL for sticky sessions as a time.Duration.
// Derived from the centralized defaults package.
const DefaultStickyTTL = defaults.StickyTTL

// ConfigKeyInterGroupStrategy is the config key for inter-group selection strategy.
const ConfigKeyInterGroupStrategy = "inter_group_strategy"

// ConfigKeyStickyEnabled is the config key for enabling/disabling sticky sessions.
const ConfigKeyStickyEnabled = "sticky_enabled"

// UngroupedProviderPriority is the priority assigned to ungrouped providers.
// They are given the lowest priority (highest value) so grouped providers take precedence.
//
// Limitation: If a user sets a group priority to math.MaxInt32 (2147483647), it will
// have the same priority as ungrouped providers. In this edge case, the tiebreaker is
// alphabetical GroupID comparison, which may produce unexpected ordering. In practice,
// group priorities should be kept in reasonable ranges (e.g., 0-1000).
const UngroupedProviderPriority = math.MaxInt32

// UngroupedGroupIDPrefix is the prefix for virtual group IDs assigned to ungrouped providers.
const UngroupedGroupIDPrefix = "__ungrouped_"

// UngroupedProviderWeight is the weight assigned to ungrouped provider virtual groups.
const UngroupedProviderWeight = 1

// Store defines the minimal storage interface needed by the selector.
type Store interface {
	ListProvidersByAPIType(ctx context.Context, apiType string) ([]model.Provider, error)
	GetProvider(ctx context.Context, id string) (*model.Provider, error)
	GetGroup(ctx context.Context, id string) (*model.Group, error)
	GetConfig(ctx context.Context, key string) (string, error)
}

// HealthChecker defines the interface to check provider availability.
type HealthChecker interface {
	// RecoverIfExpired triggers auto-recovery if the provider's disable period has expired.
	// Returns true if recovery was performed.
	RecoverIfExpired(ctx context.Context, providerID string) bool
	// IsAvailable checks if the provider is currently available (pure query).
	IsAvailable(ctx context.Context, providerID string) bool
}

// Config holds selector configuration.
type Config struct {
	Store         Store
	HealthChecker HealthChecker
	StickyCache   internal.StickyCache
	Limiter       *ConcurrencyLimiter
	Clock         internal.Clock
	Logger        *zap.Logger
}

// SelectResult contains the selected provider along with metadata about the selection.
type SelectResult struct {
	Provider        *model.Provider
	FromStickyCache bool
}

// Selector selects providers based on strategies and health status.
type Selector struct {
	store   Store
	health  HealthChecker
	sticky  internal.StickyCache
	limiter *ConcurrencyLimiter
	clock   internal.Clock
	logger  *zap.Logger
}

// NewSelector creates a new provider selector.
func NewSelector(cfg Config) *Selector {
	return &Selector{
		store:   cfg.Store,
		health:  cfg.HealthChecker,
		sticky:  cfg.StickyCache,
		limiter: cfg.Limiter,
		clock:   cfg.Clock,
		logger:  cfg.Logger,
	}
}

// Select chooses an available provider based on the request.
// It uses sticky sessions, health checks, concurrency limits, and selection strategies.
func (s *Selector) Select(ctx context.Context, req *model.SelectRequest) (*model.Provider, error) {
	return s.SelectExcluding(ctx, req, nil)
}

// SelectWithMetadata chooses an available provider and returns metadata about the selection.
// It works like Select but also indicates whether the provider came from the sticky cache.
func (s *Selector) SelectWithMetadata(ctx context.Context, req *model.SelectRequest) (*SelectResult, error) {
	// Check sticky cache first
	if provider := s.checkStickyCache(ctx, req); provider != nil {
		return &SelectResult{
			Provider:        provider,
			FromStickyCache: true,
		}, nil
	}

	// Fall through to normal provider selection (without sticky cache check)
	provider, err := s.selectExcludingInternal(ctx, req, nil)
	if err != nil {
		return nil, err
	}

	return &SelectResult{
		Provider:        provider,
		FromStickyCache: false,
	}, nil
}

// SelectExcluding selects a provider excluding the given provider IDs (for retry).
func (s *Selector) SelectExcluding(ctx context.Context, req *model.SelectRequest, excludeIDs map[string]bool) (*model.Provider, error) {
	// Check sticky cache first (if no exclusions)
	if len(excludeIDs) == 0 {
		if provider := s.checkStickyCache(ctx, req); provider != nil {
			return provider, nil
		}
	}

	return s.selectExcludingInternal(ctx, req, excludeIDs)
}

// selectExcludingInternal performs the actual provider selection without sticky cache check.
// This is extracted to allow SelectWithMetadata to track whether the result came from sticky cache.
func (s *Selector) selectExcludingInternal(ctx context.Context, req *model.SelectRequest, excludeIDs map[string]bool) (*model.Provider, error) {
	// Get all enabled providers for this API type
	providers, err := s.store.ListProvidersByAPIType(ctx, req.APIType)
	if err != nil {
		return nil, err
	}

	if len(providers) == 0 {
		return nil, internal.ErrNoProvider
	}

	// Get inter-group strategy from config
	interGroupStrategy, err := s.store.GetConfig(ctx, ConfigKeyInterGroupStrategy)
	if err != nil {
		s.logger.Warn("failed to get inter_group_strategy, using default",
			zap.Error(err),
			zap.String("default", StrategyPriority))
	}
	if interGroupStrategy == "" {
		interGroupStrategy = StrategyPriority
	}

	// Build group candidates with failover filtering
	groupCandidates, ungroupedProviders := s.buildGroupCandidates(ctx, providers, excludeIDs, req.FailoverContext, req.MaxProviderSwitches)

	// Add ungrouped providers as individual virtual groups (lowest priority)
	// Each gets a unique virtual group ID to allow independent removal during retry
	for i, p := range ungroupedProviders {
		provider := p // Create a copy to avoid pointer issues
		groupCandidates = append(groupCandidates, &groupCandidate{
			GroupID:   fmt.Sprintf("%s%d_%s", UngroupedGroupIDPrefix, i, provider.ID), // Unique virtual group ID
			Priority:  UngroupedProviderPriority,
			Weight:    UngroupedProviderWeight,
			Strategy:  StrategyPriority,
			Providers: []*model.Provider{&provider},
		})
	}

	if len(groupCandidates) == 0 {
		return nil, internal.ErrNoProvider
	}

	// Try groups in order until we find an available provider
	for len(groupCandidates) > 0 {
		// Select a group
		group := SelectGroup(groupCandidates, interGroupStrategy)
		if group == nil {
			break
		}

		// Remove from candidates to avoid re-selecting
		groupCandidates = removeGroupCandidate(groupCandidates, group.GroupID)

		// Try to select a provider from this group
		provider := s.selectFromGroup(ctx, group, excludeIDs)
		if provider != nil {
			return provider, nil
		}
	}

	return nil, internal.ErrNoProvider
}

// checkStickyCache checks for a cached sticky provider and returns it if available.
// Returns nil if sticky sessions are disabled (StickyEnabled=false) or if no valid cached provider exists.
func (s *Selector) checkStickyCache(ctx context.Context, req *model.SelectRequest) *model.Provider {
	if s.sticky == nil {
		return nil
	}

	if !req.StickyEnabled {
		return nil
	}

	stickyKey := model.StickyKey{
		IP:      req.ClientIP,
		User:    req.User,
		APIType: req.APIType,
	}

	providerID, found := s.sticky.Get(stickyKey)
	if !found {
		return nil
	}

	// Verify provider health (trigger recovery first, then check availability)
	if s.health != nil {
		s.health.RecoverIfExpired(ctx, providerID)
		if !s.health.IsAvailable(ctx, providerID) {
			s.sticky.Delete(stickyKey)
			return nil
		}
	}

	// Get and verify provider
	provider, err := s.getProviderByIDIfValid(ctx, providerID, req.APIType)
	if err != nil || provider == nil {
		s.sticky.Delete(stickyKey)
		return nil
	}

	// Check concurrency limit
	if s.limiter != nil && !s.limiter.TryAcquire(provider.ID, provider.Concurrency) {
		// Log sticky cache deletion for observability.
		// This helps identify when high load causes session affinity loss.
		s.logger.Debug("sticky cache deleted due to concurrency limit",
			zap.String("provider_id", provider.ID),
			zap.String("client_ip", stickyKey.IP),
			zap.String("user", stickyKey.User),
			zap.String("api_type", stickyKey.APIType),
		)
		s.sticky.Delete(stickyKey)
		return nil
	}

	return provider
}

// buildGroupCandidates organizes providers into groups.
func (s *Selector) buildGroupCandidates(ctx context.Context, providers []model.Provider, excludeIDs map[string]bool, failoverCtx *model.FailoverContext, maxProviderSwitches int) ([]*groupCandidate, []model.Provider) {
	groupMap := make(map[string]*groupCandidate)
	var ungrouped []model.Provider

	for _, p := range providers {
		// Skip excluded providers
		if excludeIDs != nil && excludeIDs[p.ID] {
			continue
		}

		// Check health (trigger recovery first, then check availability)
		if s.health != nil {
			s.health.RecoverIfExpired(ctx, p.ID)
			if !s.health.IsAvailable(ctx, p.ID) {
				continue
			}
		}

		// Check failover isolation rules (if this is a failover attempt)
		if failoverCtx != nil {
			if !model.IsFailoverAllowed(&p, failoverCtx, maxProviderSwitches) {
				s.logger.Debug("provider excluded by failover rules",
					zap.String("provider_id", p.ID),
					zap.String("vendor", p.Vendor),
					zap.String("candidate_accept_failover", string(p.AcceptFailover)),
					zap.Strings("contaminated_vendors", failoverCtx.ContaminatedVendors),
					zap.String("strictest_scope", string(failoverCtx.StrictestScope)),
					zap.Int("retry_count", failoverCtx.RetryCount))
				continue
			}
		}

		if p.GroupID == nil || *p.GroupID == "" {
			// Ungrouped provider
			ungrouped = append(ungrouped, p)
			continue
		}

		groupID := *p.GroupID
		if _, exists := groupMap[groupID]; !exists {
			// Load group info
			group, err := s.store.GetGroup(ctx, groupID)
			if err != nil {
				s.logger.Warn("failed to load group, skipping provider",
					zap.String("group_id", groupID),
					zap.String("provider_id", p.ID),
					zap.Error(err))
				// Fail-closed: skip this provider instead of treating as ungrouped
				continue
			}
			if !group.Enabled {
				continue // Skip disabled groups
			}
			groupMap[groupID] = &groupCandidate{
				GroupID:   groupID,
				Priority:  group.Priority,
				Weight:    group.Weight,
				Strategy:  group.Strategy,
				Providers: []*model.Provider{},
			}
		}

		provider := p // Copy to avoid pointer issues
		groupMap[groupID].Providers = append(groupMap[groupID].Providers, &provider)
	}

	// Convert map to slice, filtering out empty groups
	var candidates []*groupCandidate
	for _, g := range groupMap {
		if len(g.Providers) > 0 {
			candidates = append(candidates, g)
		}
	}

	return candidates, ungrouped
}

// selectFromGroup selects a provider from a group using the group's strategy.
// It first filters and selects a provider using the strategy, then acquires
// the concurrency slot only for the selected provider.
func (s *Selector) selectFromGroup(ctx context.Context, group *groupCandidate, excludeIDs map[string]bool) *model.Provider {
	// Filter providers by exclusion list only (no slot acquisition yet)
	candidates := make([]*model.Provider, 0, len(group.Providers))
	for _, p := range group.Providers {
		if excludeIDs != nil && excludeIDs[p.ID] {
			continue
		}
		candidates = append(candidates, p)
	}

	if len(candidates) == 0 {
		return nil
	}

	// Try to select and acquire a slot, retrying with remaining candidates if needed
	for len(candidates) > 0 {
		// Apply group strategy to select a provider
		selected := SelectProvider(candidates, group.Strategy)
		if selected == nil {
			return nil
		}

		// Double-check: capture concurrent circuit breaker trips
		// This handles the race condition where a provider becomes unhealthy
		// between buildGroupCandidates and this selection point
		if s.health != nil && !s.health.IsAvailable(ctx, selected.ID) {
			candidates = removeProvider(candidates, selected.ID)
			continue
		}

		// Try to acquire concurrency slot for the selected provider only
		if s.limiter == nil || s.limiter.TryAcquire(selected.ID, selected.Concurrency) {
			return selected
		}

		// Acquisition failed - remove from candidates and retry with remaining
		candidates = removeProvider(candidates, selected.ID)
	}

	return nil
}

// removeProvider removes a provider from the candidates list by ID.
func removeProvider(candidates []*model.Provider, providerID string) []*model.Provider {
	result := make([]*model.Provider, 0, len(candidates)-1)
	for _, p := range candidates {
		if p.ID != providerID {
			result = append(result, p)
		}
	}
	return result
}

// getProviderByIDIfValid checks if a provider exists and matches the API type.
// Uses direct O(1) lookup instead of iterating through all providers.
func (s *Selector) getProviderByIDIfValid(ctx context.Context, providerID, apiType string) (*model.Provider, error) {
	provider, err := s.store.GetProvider(ctx, providerID)
	if err != nil || provider == nil || !provider.Enabled {
		return nil, err
	}

	// Verify provider supports this API type
	for _, at := range provider.APITypes {
		if at.APIType == apiType {
			return provider, nil
		}
	}

	return nil, nil
}

// UpdateSticky updates the sticky cache after a successful request using the default TTL.
// This is a convenience method; use UpdateStickyWithTTL if you need a custom TTL.
func (s *Selector) UpdateSticky(req *model.SelectRequest, providerID string) {
	if s.sticky == nil {
		return
	}

	stickyKey := model.StickyKey{
		IP:      req.ClientIP,
		User:    req.User,
		APIType: req.APIType,
	}
	s.sticky.Set(stickyKey, providerID, DefaultStickyTTL)
}

// UpdateStickyWithTTL updates the sticky cache with a specific TTL.
func (s *Selector) UpdateStickyWithTTL(req *model.SelectRequest, providerID string, ttl time.Duration) {
	if s.sticky == nil {
		return
	}

	stickyKey := model.StickyKey{
		IP:      req.ClientIP,
		User:    req.User,
		APIType: req.APIType,
	}
	s.sticky.Set(stickyKey, providerID, ttl)
}

// ReleaseConcurrency releases the concurrency slot for a provider.
func (s *Selector) ReleaseConcurrency(providerID string) {
	if s.limiter != nil {
		s.limiter.Release(providerID)
	}
}

// ClearConcurrency removes the concurrency counter for a provider.
// Call this when a provider is deleted to prevent unbounded memory growth.
// This delegates to ConcurrencyLimiter.Clear.
func (s *Selector) ClearConcurrency(providerID string) {
	if s.limiter != nil {
		s.limiter.Clear(providerID)
	}
}

// removeGroupCandidate removes a group from the candidates list.
func removeGroupCandidate(candidates []*groupCandidate, groupID string) []*groupCandidate {
	result := make([]*groupCandidate, 0, len(candidates))
	for _, c := range candidates {
		if c.GroupID != groupID {
			result = append(result, c)
		}
	}
	return result
}
