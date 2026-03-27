package proxy

import (
	"context"

	"switch-a/internal"
	"switch-a/internal/model"
	"switch-a/internal/selector"

	"go.uber.org/zap"
)

// selectProviderWithTracking selects a provider for the given attempt.
// Returns (provider, useStickyBehavior, error). useStickyBehavior indicates the provider
// was selected via sticky cache or active request fallback, meaning retries should be skipped
// on failure since the client explicitly requested this provider continuity.
func (h *Handler) selectProviderWithTracking(ctx context.Context, selectReq *model.SelectRequest, attempt int, excluded map[string]bool) (*model.Provider, bool, error) {
	if h.selector == nil {
		// Fallback: direct provider list (no selector configured)
		provider, err := normalizeSelectedProvider(h.selectProviderFallback(ctx, selectReq, attempt, excluded))
		return provider, false, err
	}

	if attempt == 0 {
		result, err := normalizeSelectorSelectResult(h.selector.SelectWithMetadata(ctx, selectReq))
		if err != nil {
			return nil, false, err
		}

		// Sticky cache hit, directly return
		if result.FromStickyCache {
			return result.Provider, true, nil
		}

		// Check active requests when sticky cache misses (see tryActiveProviderFallback doc).
		if activeProvider := h.tryActiveProviderFallback(ctx, selectReq); activeProvider != nil {
			// Release the concurrency slot acquired by SelectWithMetadata above,
			// since we're returning a different provider from the active registry.
			h.releaseConcurrency(result.Provider.ID)
			return activeProvider, true, nil
		}

		return result.Provider, false, nil
	}

	provider, err := normalizeSelectedProvider(h.selector.SelectExcluding(ctx, selectReq, excluded))
	return provider, false, err
}

func normalizeSelectorSelectResult(result *selector.SelectResult, err error) (*selector.SelectResult, error) {
	if err != nil {
		return nil, err
	}
	if result == nil || result.Provider == nil {
		// Provider exhaustion must collapse to ErrNoProvider so retry orchestration can
		// terminate cleanly instead of treating a nil provider as a partially valid selection.
		return nil, internal.ErrNoProvider
	}
	return result, nil
}

func normalizeSelectedProvider(provider *model.Provider, err error) (*model.Provider, error) {
	if err != nil {
		return nil, err
	}
	if provider == nil {
		// Keeping "no candidate" on the error channel makes selection semantics consistent
		// across first picks, retries, and fallback mode.
		return nil, internal.ErrNoProvider
	}
	return provider, nil
}

// tryActiveProviderFallback checks active requests for a valid provider.
// This handles the case where sticky TTL expires during a long-running SSE stream:
// the client may have an ongoing request to a provider, and we want to continue
// routing to that same provider for consistency even though the cache entry expired.
// Returns nil if no active provider is found or available.
func (h *Handler) tryActiveProviderFallback(ctx context.Context, selectReq *model.SelectRequest) *model.Provider {
	if selectReq.StickyMode == model.StickyModeOff || h.activeRegistry == nil {
		return nil
	}

	activeProviderID, found := h.activeRegistry.FindActiveProviderForRequest(selectReq)
	if !found {
		return nil
	}

	scope, err := h.selectionScope(ctx, selectReq)
	if err != nil {
		h.logger.Warn("failed to resolve provider eligibility for active fallback", zap.Error(err))
		return nil
	}

	return h.getProviderIfValid(ctx, scope, activeProviderID)
}

// getProviderIfValid validates that a provider is still valid and available.
// Used when checking active requests for sticky session fallback.
// Returns nil if provider is not found, disabled, or unhealthy.
//
// Note: This iterates through all providers by API type to find the target provider.
// A direct GetProvider(id) lookup would be O(1) but requires additional store interface
// changes. The current O(n) approach is acceptable given typical provider counts (<100).
func (h *Handler) getProviderIfValid(ctx context.Context, scope *selector.ProviderSelectionEligibility, providerID string) *model.Provider {
	if scope == nil || scope.Request() == nil {
		return nil
	}

	providers, err := h.store.ListProvidersByAPIType(ctx, scope.Request().APIType)
	if err != nil {
		h.logger.Warn("failed to list providers for active fallback", zap.Error(err))
		return nil
	}

	for _, p := range providers {
		allowed, eligibilityErr := scope.AllowsProvider(ctx, &p)
		if eligibilityErr != nil {
			h.logger.Warn("failed to validate active fallback provider", zap.String("provider_id", p.ID), zap.Error(eligibilityErr))
			return nil
		}
		if p.ID == providerID && allowed {
			provider := p
			return &provider
		}
	}
	return nil
}

// selectProviderFallback selects a provider when no Selector is configured.
//
// This fallback exists for minimal deployments that skip selector-owned strategy
// and concurrency features. It still reuses the shared eligibility closure so
// auth-state and hard-routing rules cannot be bypassed just because no selector
// was wired into the handler.
//
// Limitations compared to the full Selector:
//   - No concurrency limits: may overload providers
//   - No sticky sessions: same client may hit different providers
//   - No group-based strategies: ignores priority/weight/random settings
//
// For production deployments with multiple providers, configure a Selector for robust behavior.
func (h *Handler) selectProviderFallback(ctx context.Context, selectReq *model.SelectRequest, attempt int, excluded map[string]bool) (*model.Provider, error) {
	scope, err := h.selectionScope(ctx, selectReq)
	if err != nil {
		return nil, err
	}

	providers, err := h.store.ListProvidersByAPIType(ctx, selectReq.APIType)
	if err != nil { // coverage-ignore -- database errors are rare after successful startup
		h.logger.Error("failed to list providers", zap.Error(err), zap.String("api_type", selectReq.APIType))
		return nil, err
	}

	// Filter out excluded providers (those that have already failed in this request)
	available := providers[:0]
	for _, p := range providers {
		allowed, eligibilityErr := scope.AllowsProvider(ctx, &p)
		if eligibilityErr != nil {
			return nil, eligibilityErr
		}
		if !excluded[p.ID] && allowed {
			available = append(available, p)
		}
	}

	if len(available) == 0 {
		return nil, internal.ErrNoProvider
	}
	// True round-robin: use atomic counter for cross-request distribution,
	// plus attempt offset to ensure retries hit different providers.
	// Use uint64 conversion to handle wrap-around safely: when int64 wraps from
	// MaxInt64 to MinInt64, converting to uint64 ensures non-negative indices.
	idx := h.fallbackCounter.Add(1)
	provider := available[int(uint64(idx-1+int64(attempt))%uint64(len(available)))]
	return &provider, nil
}

func (h *Handler) selectionScope(ctx context.Context, req *model.SelectRequest) (*selector.ProviderSelectionEligibility, error) {
	return selector.NewProviderSelectionEligibility(ctx, h.store, h.health, req)
}
