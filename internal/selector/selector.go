package selector

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/defaults"
	"github.com/doraemonkeys/switch-a/internal/model"

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
	Provider *model.Provider
	Metadata SelectionMetadata
}

// SelectionSource explains how the selector reached the chosen provider.
type SelectionSource string

const (
	SelectionSourceStrategy         SelectionSource = "strategy"
	SelectionSourceStickyContinuity SelectionSource = "sticky_continuity"
	SelectionSourceActiveContinuity SelectionSource = "active_continuity"
)

// SelectionMetadata keeps continuity provenance explicit so retry policy can be
// derived from lifecycle state instead of overloading "sticky" as a control flag.
type SelectionMetadata struct {
	Source SelectionSource
	// SwitchMode is echoed back so downstream logging and orchestration do not
	// need to reconstruct replacement vs failover from incidental state.
	SwitchMode model.SwitchMode
	// ContinuitySeeded reports whether this request entered selection with a
	// visible continuity seed candidate. The selector keeps this explicit so
	// callers never need to infer seeded continuity from sticky hits alone.
	ContinuitySeeded bool
	// ContinuityOriginProviderID identifies the visible continuity source when the
	// request is seeded or already continuity-attached.
	ContinuityOriginProviderID string
	// ContinuitySeedObservedAt lets callers derive age in milliseconds without
	// asking the seed store to materialize mutable request state.
	ContinuitySeedObservedAt time.Time
	// ContinuitySeedAgeAtSelectionMs freezes the admin-visible seed age when the
	// provider was chosen so long-running attempts cannot rewrite provenance.
	ContinuitySeedAgeAtSelectionMs *int64
}

func (m SelectionMetadata) UsesContinuity() bool {
	switch m.Source {
	case SelectionSourceStickyContinuity, SelectionSourceActiveContinuity:
		return true
	default:
		return false
	}
}

func (m SelectionMetadata) ContinuitySeedAge(at time.Time) time.Duration {
	if !m.ContinuitySeeded || m.ContinuitySeedObservedAt.IsZero() {
		return 0
	}
	age := at.Sub(m.ContinuitySeedObservedAt)
	if age < 0 {
		return 0
	}
	return age
}

// BuildSelectionMetadata keeps continuity provenance explicit at the selection
// boundary so downstream code can log the request's continuity path without
// reverse-engineering it from sticky booleans or adjacent attempts.
func BuildSelectionMetadata(req *model.SelectRequest, source SelectionSource) SelectionMetadata {
	return BuildSelectionMetadataAt(req, source, time.Now())
}

// BuildSelectionMetadataAt freezes selection-time continuity provenance so
// downstream persistence can report the age seen when the provider was chosen,
// not after an arbitrarily long request lifecycle finishes.
func BuildSelectionMetadataAt(req *model.SelectRequest, source SelectionSource, selectedAt time.Time) SelectionMetadata {
	metadata := SelectionMetadata{
		Source:     source,
		SwitchMode: reqSwitchMode(req),
	}

	if continuity := reqProviderContinuityContext(req); continuity != nil {
		metadata.ContinuityOriginProviderID = continuity.VisibleOriginProviderID
	}
	if candidate := reqVisibleContinuitySeedCandidate(req); candidate != nil {
		metadata.ContinuitySeeded = true
		metadata.ContinuityOriginProviderID = candidate.OriginProviderID
		metadata.ContinuitySeedObservedAt = candidate.ObservedAt
		ageMs := metadata.ContinuitySeedAge(selectedAt).Milliseconds()
		metadata.ContinuitySeedAgeAtSelectionMs = &ageMs
	}

	return metadata
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
	scope, err := s.selectionScope(ctx, req)
	if err != nil {
		return nil, err
	}

	// Check sticky cache first.
	provider, err := s.checkStickyCache(ctx, scope)
	if err != nil {
		return nil, err
	}
	if provider != nil {
		return &SelectResult{
			Provider: provider,
			Metadata: BuildSelectionMetadataAt(req, SelectionSourceStickyContinuity, s.selectionTimestamp()),
		}, nil
	}

	// Fall through to normal provider selection (without sticky cache check)
	provider, err = s.selectExcludingInternal(ctx, scope, nil)
	if err != nil {
		return nil, err
	}

	return &SelectResult{
		Provider: provider,
		Metadata: BuildSelectionMetadataAt(req, SelectionSourceStrategy, s.selectionTimestamp()),
	}, nil
}

func (s *Selector) selectionTimestamp() time.Time {
	if s == nil || s.clock == nil {
		return time.Now()
	}
	return s.clock.Now()
}

// SelectExcluding selects a provider excluding the given provider IDs (for retry).
func (s *Selector) SelectExcluding(ctx context.Context, req *model.SelectRequest, excludeIDs map[string]bool) (*model.Provider, error) {
	scope, err := s.selectionScope(ctx, req)
	if err != nil {
		return nil, err
	}

	// Check sticky cache first (if no exclusions)
	if len(excludeIDs) == 0 {
		provider, err := s.checkStickyCache(ctx, scope)
		if err != nil {
			return nil, err
		}
		if provider != nil {
			return provider, nil
		}
	}

	return s.selectExcludingInternal(ctx, scope, excludeIDs)
}

// selectExcludingInternal performs the actual provider selection without sticky cache check.
// This is extracted to allow SelectWithMetadata to track whether the result came from sticky cache.
func (s *Selector) selectExcludingInternal(ctx context.Context, scope *ProviderSelectionEligibility, excludeIDs map[string]bool) (*model.Provider, error) {
	req := scope.req

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
	groupCandidates, ungroupedProviders, err := s.buildGroupCandidates(ctx, scope, providers, excludeIDs)
	if err != nil {
		return nil, err
	}

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
		provider, err := s.selectFromGroup(ctx, scope, group, excludeIDs)
		if err != nil {
			return nil, err
		}
		if provider != nil {
			return provider, nil
		}
	}

	return nil, internal.ErrNoProvider
}

// isStickyEnabled returns true only for recognized non-off sticky modes.
// Unknown or zero-value StickyMode is treated as disabled for defense-in-depth,
// even though the current sole caller (proxy.Handler) validates before constructing SelectRequest.
func isStickyEnabled(mode model.StickyMode) bool {
	return mode == model.StickyModeAPIType || mode == model.StickyModeModel
}

func buildStickyKey(req *model.SelectRequest) model.StickyKey {
	return BuildContinuityKey(req)
}

// checkStickyCache checks for a cached sticky provider and returns it if available.
// It returns an error when eligibility cannot be evaluated safely, preserving the
// non-sticky path's contract for transient auth-state read failures.
func (s *Selector) checkStickyCache(ctx context.Context, scope *ProviderSelectionEligibility) (*model.Provider, error) {
	if scope == nil || scope.req == nil || s.sticky == nil || !isStickyEnabled(scope.req.StickyMode) {
		return nil, nil
	}

	stickyKey := buildStickyKey(scope.req)

	providerID, found := s.sticky.Get(stickyKey)
	if !found {
		return nil, nil
	}

	// Get and verify provider
	provider, err := s.store.GetProvider(ctx, providerID)
	if err != nil || provider == nil {
		s.sticky.Delete(stickyKey)
		return nil, nil
	}

	allowed, err := scope.AllowsProvider(ctx, provider)
	if err != nil {
		return nil, err
	}
	if !allowed {
		s.sticky.Delete(stickyKey)
		return nil, nil
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
		return nil, nil
	}

	return provider, nil
}

// buildGroupCandidates organizes providers into groups.
func (s *Selector) buildGroupCandidates(ctx context.Context, scope *ProviderSelectionEligibility, providers []model.Provider, excludeIDs map[string]bool) ([]*groupCandidate, []model.Provider, error) {
	groupMap := make(map[string]*groupCandidate)
	var ungrouped []model.Provider

	for _, p := range providers {
		// Skip excluded providers
		if excludeIDs != nil && excludeIDs[p.ID] {
			continue
		}

		allowed, err := scope.AllowsProvider(ctx, &p)
		if err != nil {
			return nil, nil, err
		}
		if !allowed {
			continue
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

	return candidates, ungrouped, nil
}

// selectFromGroup selects a provider from a group using the group's strategy.
// It first filters and selects a provider using the strategy, then acquires
// the concurrency slot only for the selected provider.
func (s *Selector) selectFromGroup(ctx context.Context, scope *ProviderSelectionEligibility, group *groupCandidate, excludeIDs map[string]bool) (*model.Provider, error) {
	// Filter providers by exclusion list only (no slot acquisition yet)
	candidates := make([]*model.Provider, 0, len(group.Providers))
	for _, p := range group.Providers {
		if excludeIDs != nil && excludeIDs[p.ID] {
			continue
		}
		candidates = append(candidates, p)
	}

	if len(candidates) == 0 {
		return nil, nil
	}

	// Try to select and acquire a slot, retrying with remaining candidates if needed
	for len(candidates) > 0 {
		// Apply group strategy to select a provider
		selected := SelectProvider(candidates, group.Strategy)
		if selected == nil {
			return nil, nil
		}

		// Re-run eligibility at selection time so routing/auth/health changes that
		// happen after the initial candidate build cannot leak through retries.
		allowed, err := scope.AllowsProvider(ctx, selected)
		if err != nil {
			return nil, err
		}
		if !allowed {
			candidates = removeProvider(candidates, selected.ID)
			continue
		}

		// Try to acquire concurrency slot for the selected provider only
		if s.limiter == nil || s.limiter.TryAcquire(selected.ID, selected.Concurrency) {
			return selected, nil
		}

		// Acquisition failed - remove from candidates and retry with remaining
		candidates = removeProvider(candidates, selected.ID)
	}

	return nil, nil
}

func (s *Selector) selectionScope(ctx context.Context, req *model.SelectRequest) (*ProviderSelectionEligibility, error) {
	return NewProviderSelectionEligibility(ctx, s.store, s.health, req)
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

// UpdateSticky updates the sticky cache after a successful request using the default TTL.
// This is a convenience method; use UpdateStickyWithTTL if you need a custom TTL.
func (s *Selector) UpdateSticky(req *model.SelectRequest, providerID string) {
	if s.sticky == nil || !isStickyEnabled(req.StickyMode) {
		return
	}

	stickyKey := buildStickyKey(req)
	s.sticky.Set(stickyKey, providerID, DefaultStickyTTL)
}

// UpdateStickyWithTTL updates the sticky cache with a specific TTL.
func (s *Selector) UpdateStickyWithTTL(req *model.SelectRequest, providerID string, ttl time.Duration) {
	if s.sticky == nil || !isStickyEnabled(req.StickyMode) {
		return
	}

	stickyKey := buildStickyKey(req)
	s.sticky.Set(stickyKey, providerID, ttl)
}

// EvictProviderContinuity removes every sticky continuity entry for the provider.
// Suspension paths use this instead of reaching into the cache implementation so
// eager invalidation stays aligned with the same abstraction selectors read from.
func (s *Selector) EvictProviderContinuity(providerID string) {
	if s.sticky == nil || providerID == "" {
		return
	}
	s.sticky.EvictProvider(providerID)
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
