package selector

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/codex/identity"
	"github.com/doraemonkeys/switch-a/internal/defaults"
	"github.com/doraemonkeys/switch-a/internal/model"

	"go.uber.org/zap"
)

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
	Store             Store
	HealthChecker     HealthChecker
	AuthorityResolver CandidateAuthorityResolver
	StickyCache       internal.StickyCache
	Limiter           *ConcurrencyLimiter
	Clock             internal.Clock
	Logger            *zap.Logger
}

// SelectResult keeps the selected provider behind its exact lease so dispatch
// facts and cleanup ownership cannot diverge.
type SelectResult struct {
	Lease    *ProviderLease
	Metadata SelectionMetadata
}

// Provider derives the dispatch snapshot from the capability that owns cleanup,
// preventing callers from pairing one provider value with another slot lease.
func (r *SelectResult) Provider() *model.Provider {
	if r == nil || r.Lease == nil {
		return nil
	}
	return r.Lease.Provider()
}

// CandidateSnapshot exposes the exact selection-time identity snapshot carried
// by the lease so authentication does not independently resolve mutable state.
func (r *SelectResult) CandidateSnapshot() (codexidentity.CandidateSnapshot, bool) {
	if r == nil || r.Lease == nil {
		return codexidentity.CandidateSnapshot{}, false
	}
	return r.Lease.CandidateSnapshot()
}

// SelectionSource explains how the selector reached the chosen provider.
type SelectionSource string

const (
	SelectionSourceStrategy         SelectionSource = "strategy"
	SelectionSourcePreferredRoute   SelectionSource = "preferred_route_target"
	SelectionSourceStickyContinuity SelectionSource = "sticky_continuity"
	SelectionSourceActiveContinuity SelectionSource = "active_continuity"
	SelectionSourceAlternate        SelectionSource = "alternate"
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
	case SelectionSourcePreferredRoute, SelectionSourceStickyContinuity, SelectionSourceActiveContinuity:
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
	store    Store
	health   HealthChecker
	resolver CandidateAuthorityResolver
	sticky   internal.StickyCache
	limiter  *ConcurrencyLimiter
	clock    internal.Clock
	logger   *zap.Logger
}

// NewSelector creates a new provider selector.
func NewSelector(cfg Config) *Selector {
	if cfg.Limiter == nil {
		cfg.Limiter = NewConcurrencyLimiter()
	}
	if cfg.Logger == nil {
		cfg.Logger = zap.NewNop()
	}
	return &Selector{
		store:    cfg.Store,
		health:   cfg.HealthChecker,
		resolver: cfg.AuthorityResolver,
		sticky:   cfg.StickyCache,
		limiter:  cfg.Limiter,
		clock:    cfg.Clock,
		logger:   cfg.Logger,
	}
}

// SelectWithMetadata chooses an available provider and returns its exact lease
// plus explicit selection provenance.
func (s *Selector) SelectWithMetadata(ctx context.Context, req *model.SelectRequest) (*SelectResult, error) {
	lifecycle := s.limiter.beginLifecycleRead()
	defer lifecycle.Release()
	scope, err := s.selectionScope(ctx, req)
	if err != nil {
		return nil, err
	}

	lease, err := s.selectPreferredRoute(ctx, scope, nil)
	if err != nil {
		return nil, err
	}
	if lease != nil {
		return &SelectResult{
			Lease:    lease,
			Metadata: BuildSelectionMetadataAt(req, SelectionSourcePreferredRoute, s.selectionTimestamp()),
		}, nil
	}

	// Check sticky cache first.
	lease, err = s.checkStickyCache(ctx, scope)
	if err != nil {
		return nil, err
	}
	if lease != nil {
		return &SelectResult{
			Lease:    lease,
			Metadata: BuildSelectionMetadataAt(req, SelectionSourceStickyContinuity, s.selectionTimestamp()),
		}, nil
	}

	// Fall through to normal provider selection (without sticky cache check)
	lease, err = s.selectExcludingInternal(ctx, scope, nil)
	if err != nil {
		return nil, err
	}

	return &SelectResult{
		Lease:    lease,
		Metadata: BuildSelectionMetadataAt(req, SelectionSourceStrategy, s.selectionTimestamp()),
	}, nil
}

func (s *Selector) selectionTimestamp() time.Time {
	if s == nil || s.clock == nil {
		return time.Now()
	}
	return s.clock.Now()
}

// SelectExcludingWithMetadata keeps provider ownership explicit for retry and
// active-request call sites while preserving the same sticky semantics.
func (s *Selector) SelectExcludingWithMetadata(ctx context.Context, req *model.SelectRequest, excludeIDs map[string]bool) (*SelectResult, error) {
	lifecycle := s.limiter.beginLifecycleRead()
	defer lifecycle.Release()
	scope, err := s.selectionScope(ctx, req)
	if err != nil {
		return nil, err
	}

	lease, err := s.selectPreferredRoute(ctx, scope, excludeIDs)
	if err != nil {
		return nil, err
	}
	if lease != nil {
		return &SelectResult{
			Lease:    lease,
			Metadata: BuildSelectionMetadataAt(req, SelectionSourcePreferredRoute, s.selectionTimestamp()),
		}, nil
	}

	// Check sticky cache first (if no exclusions)
	if len(excludeIDs) == 0 {
		lease, err := s.checkStickyCache(ctx, scope)
		if err != nil {
			return nil, err
		}
		if lease != nil {
			return &SelectResult{
				Lease:    lease,
				Metadata: BuildSelectionMetadataAt(req, SelectionSourceStickyContinuity, s.selectionTimestamp()),
			}, nil
		}
	}

	lease, err = s.selectExcludingInternal(ctx, scope, excludeIDs)
	if err != nil {
		return nil, err
	}
	return &SelectResult{
		Lease:    lease,
		Metadata: BuildSelectionMetadataAt(req, SelectionSourceStrategy, s.selectionTimestamp()),
	}, nil
}

// selectExcludingInternal performs the actual provider selection without sticky cache check.
// This is extracted to allow SelectWithMetadata to track whether the result came from sticky cache.
func (s *Selector) selectExcludingInternal(ctx context.Context, scope *ProviderSelectionEligibility, excludeIDs map[string]bool) (*ProviderLease, error) {
	providers := scope.Providers()

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
		lease, err := s.selectFromGroup(ctx, scope, group, excludeIDs)
		if err != nil {
			return nil, err
		}
		if lease != nil {
			return lease, nil
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

// selectPreferredRoute applies the route hint only after a verified Authority
// has narrowed the candidate set. Without that boundary, a provider ID could
// silently steer a state-bearing request to a different security owner.
func (s *Selector) selectPreferredRoute(
	ctx context.Context,
	scope *ProviderSelectionEligibility,
	excludeIDs map[string]bool,
) (*ProviderLease, error) {
	if scope == nil {
		return nil, nil
	}
	providerID := reqPreferredRouteTargetID(scope.req)
	if providerID == "" || (excludeIDs != nil && excludeIDs[providerID]) {
		return nil, nil
	}
	provider := scope.Provider(providerID)
	if provider == nil {
		return nil, nil
	}
	allowed, err := scope.AllowsProvider(ctx, provider)
	if err != nil || !allowed {
		return nil, err
	}
	lease, acquired := s.acquireProvider(scope, provider)
	if !acquired {
		return nil, nil
	}
	return lease, nil
}

// checkStickyCache checks for a cached sticky provider and returns it if available.
// It returns an error when eligibility cannot be evaluated safely, preserving the
// non-sticky path's contract for transient auth-state read failures.
func (s *Selector) checkStickyCache(ctx context.Context, scope *ProviderSelectionEligibility) (*ProviderLease, error) {
	if scope == nil || scope.req == nil || s.sticky == nil || !isStickyEnabled(scope.req.StickyMode) {
		return nil, nil
	}

	stickyKey := buildStickyKey(scope.req)

	providerID, found := s.sticky.Get(stickyKey)
	if !found {
		return nil, nil
	}

	// Get and verify provider
	provider := scope.Provider(providerID)
	if provider == nil {
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

	lease, acquired := s.acquireProvider(scope, provider)
	if !acquired {
		s.observeStickyBindingDecision(
			scope.req,
			provider.ID,
			stickyBindingDecisionEvicted,
			stickyBindingDecisionReasonProviderConcurrencyExhausted,
		)
		s.sticky.Delete(stickyKey)
		return nil, nil
	}

	return lease, nil
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
			group, ok := scope.Group(p.ID)
			if !ok || !group.Enabled {
				continue
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
func (s *Selector) selectFromGroup(ctx context.Context, scope *ProviderSelectionEligibility, group *groupCandidate, excludeIDs map[string]bool) (*ProviderLease, error) {
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

		// The lease is the ownership result of selection, not an incidental
		// counter mutation that downstream code must reconstruct from an ID.
		if lease, acquired := s.acquireProvider(scope, selected); acquired {
			return lease, nil
		}

		// Acquisition failed - remove from candidates and retry with remaining
		candidates = removeProvider(candidates, selected.ID)
	}

	return nil, nil
}

func (s *Selector) acquireProvider(scope *ProviderSelectionEligibility, provider *model.Provider) (*ProviderLease, bool) {
	if s == nil || s.limiter == nil || scope == nil || provider == nil {
		return nil, false
	}
	slot, acquired := s.limiter.acquireUnderLifecycle(provider.ID, provider.Concurrency)
	if !acquired {
		return nil, false
	}
	candidate, resolved := scope.CandidateSnapshot(provider.ID)
	return newProviderLeaseWithCandidate(provider, slot, candidate, resolved), true
}

func (s *Selector) selectionScope(ctx context.Context, req *model.SelectRequest) (*ProviderSelectionEligibility, error) {
	if s == nil || s.store == nil {
		return nil, internal.ErrNoProvider
	}
	providers, err := s.store.ListProvidersByAPIType(ctx, reqAPIType(req))
	if err != nil {
		return nil, err
	}
	return newProviderSelectionEligibility(ctx, s.store, s.health, s.resolver, req, providers)
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

// RetireProviderGeneration runs one provider mutation at the same lifecycle
// boundary used by lease acquisition and dispatch activation. A failed mutation
// still retires the old generation because its validated snapshot may no longer
// be trusted after a write was attempted.
func (s *Selector) RetireProviderGeneration(providerID string, mutation func() error) error {
	if mutation == nil {
		return fmt.Errorf("provider lifecycle mutation is required")
	}
	if providerID == "" {
		return fmt.Errorf("provider ID is required for lifecycle retirement")
	}
	if s == nil || s.limiter == nil {
		return mutation()
	}
	return s.limiter.mutateWithRetiredGenerations([]string{providerID}, false, mutation)
}

// RetireAllProviderGenerations is the mutation boundary for group, routing, and
// bulk configuration changes whose eligibility effects span provider IDs.
func (s *Selector) RetireAllProviderGenerations(mutation func() error) error {
	if mutation == nil {
		return fmt.Errorf("provider lifecycle mutation is required")
	}
	if s == nil || s.limiter == nil {
		return mutation()
	}
	return s.limiter.mutateWithRetiredGenerations(nil, true, mutation)
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
