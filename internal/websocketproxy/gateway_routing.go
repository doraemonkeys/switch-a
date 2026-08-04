package websocketproxy

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/apicontract"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/selector"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

type webSocketProviderConfigError struct {
	missingField string
	err          error
}

func (e *webSocketProviderConfigError) Error() string {
	if e == nil || e.err == nil {
		return ""
	}
	return e.err.Error()
}

func (e *webSocketProviderConfigError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func (h *Gateway) validateWebSocketProviderReady(provider *model.Provider, apiType string) (string, error) {
	baseURL := provider.BaseURLForAPIType(apiType)
	if baseURL == "" {
		return "", &webSocketProviderConfigError{
			missingField: "base_url",
			err:          fmt.Errorf("no base_url for api_type %q", apiType),
		}
	}
	switch model.NormalizeProviderCredentialType(provider.CredentialType) {
	case model.ProviderCredentialTypeChatGPT:
		if h.auth == nil {
			return "", &webSocketProviderConfigError{
				missingField: "credentials",
				err:          fmt.Errorf("provider %q requires managed credentials for websocket", provider.ID),
			}
		}
	default:
		if provider.APIKeyForAPIType(apiType) == "" {
			return "", &webSocketProviderConfigError{
				missingField: "api_key",
				err:          fmt.Errorf("no api_key for api_type %q", apiType),
			}
		}
	}
	return baseURL, nil
}

// extractWebSocketModel extracts the model identifier from a WebSocket request.
// WebSocket requests carry the model in query parameters because the request body
// is empty during the handshake.
func extractWebSocketModel(r *http.Request) string {
	if modelName := r.URL.Query().Get("model"); modelName != "" {
		return modelName
	}
	return ModelUnknown
}

func hasUsableWebSocketSelectionModel(modelName string) bool {
	trimmed := strings.TrimSpace(modelName)
	return trimmed != "" && !strings.EqualFold(trimmed, ModelUnknown)
}

// webSocketSelectionConsumesHiddenModel reuses the selector seam so websocket
// probe gating stays aligned with the pre-selection consumers that govern model
// sticky continuity and active model-scoped routing rules.
func (h *Gateway) webSocketSelectionConsumesHiddenModel(ctx context.Context, req *model.SelectRequest) (bool, error) {
	if h == nil || req == nil || hasUsableWebSocketSelectionModel(req.Model) {
		return false, nil
	}
	return selector.ResolveSelectionHiddenModelDemand(ctx, h.store, req)
}

// buildWebSocketPassthroughHeaders copies client-controlled handshake headers
// that belong to the wire protocol rather than provider authentication.
func buildWebSocketPassthroughHeaders(r *http.Request) http.Header {
	headers := make(http.Header)
	for key, values := range r.Header {
		if hopByHopHeaders[key] || isAuthHeader(key) || isWebSocketHandshakeHeader(key) {
			continue
		}
		for _, value := range values {
			headers.Add(key, value)
		}
	}
	return headers
}

// buildWebSocketDialHeaders preserves static-auth behavior for tests and
// fallback paths that do not use the managed provider auth service.
func buildWebSocketDialHeaders(r *http.Request, provider *model.Provider, apiType, globalAuthMode string) http.Header {
	headers := buildWebSocketPassthroughHeaders(r)
	SetAuthHeader(headers, provider.APIKeyForAPIType(apiType), provider.AuthMode, globalAuthMode, r)
	return headers
}

func (h *Gateway) prepareWebSocketDialHeaders(ctx context.Context, r *http.Request, provider *model.Provider, apiType, globalAuthMode string) (http.Header, error) {
	headers, err := h.prepareWebSocketAttemptHeaders(ctx, r, provider, apiType, globalAuthMode)
	if err != nil {
		return nil, err
	}
	return headers, nil
}

func (h *Gateway) prepareWebSocketAttemptHeaders(
	ctx context.Context,
	r *http.Request,
	provider *model.Provider,
	apiType,
	globalAuthMode string,
) (http.Header, error) {
	headers := buildWebSocketPassthroughHeaders(r)
	if h.auth != nil {
		if err := h.auth.ApplyProviderCredentials(ctx, headers, provider, apiType, globalAuthMode, r); err != nil {
			// The orchestrator retains this partial request only as sanitizer input;
			// the compatibility wrapper above still returns nil and no dial can occur.
			return headers, err
		}
		return headers, nil
	}

	SetAuthHeader(headers, provider.APIKeyForAPIType(apiType), provider.AuthMode, globalAuthMode, r)
	return headers, nil
}

func websocketGatewayFailure(result *WebSocketResult) (int, string, string) {
	statusCode := http.StatusBadGateway
	if result != nil && result.HandshakeStatusCode > 0 {
		statusCode = result.HandshakeStatusCode
	}

	message := "Upstream WebSocket handshake failed"
	switch statusCode {
	case http.StatusUnauthorized:
		message = "Upstream WebSocket authentication failed"
	case http.StatusUpgradeRequired:
		message = "Upstream provider requires HTTP fallback for this request"
	}
	if result != nil && result.HandshakeBodySnippet != "" {
		message = result.HandshakeBodySnippet
	}

	return statusCode, ErrCodeWebSocketUpgrade, message
}

func (h *Gateway) buildFullURL(baseURL, path, query string) string {
	joined, err := url.JoinPath(baseURL, path)
	if err != nil {
		h.logger.Warn("invalid websocket base URL; using slash-preserving fallback", zap.String("base_url", baseURL), zap.Error(err))
		if !strings.HasSuffix(baseURL, "/") && !strings.HasPrefix(path, "/") {
			joined = baseURL + "/" + path
		} else {
			joined = baseURL + path
		}
	}
	if query != "" {
		return joined + "?" + query
	}
	return joined
}

func BuildUpstreamPath(originalPath, apiType string) string {
	return apicontract.RewriteUpstreamPath(originalPath, apiType)
}

func (h *Gateway) maybeLookupVisibleContinuityCandidate(ctx context.Context, tracker *providerSwitchTracker) {
	if h == nil || tracker == nil || tracker.selectReq == nil {
		return
	}
	if hasUsableSelectionModel(tracker.selectReq.Model) {
		tracker.lookupVisibleContinuityCandidate()
		return
	}
	consumesHiddenModel, err := selector.ResolveSelectionHiddenModelDemand(ctx, h.store, tracker.selectReq)
	if err != nil {
		h.logger.Warn("failed to resolve hidden-model demand for websocket continuity", zap.String("api_type", tracker.selectReq.APIType), zap.Error(err))
		return
	}
	if !consumesHiddenModel {
		tracker.lookupVisibleContinuityCandidate()
	}
}

func (h *Gateway) storeVisibleContinuitySeedFromContext(
	selectReq *model.SelectRequest,
	continuity *model.ProviderContinuityContext,
	observedAt time.Time,
	seed *model.VisibleContinuitySeed,
) {
	if h == nil || h.visibleContinuitySeedStore == nil {
		return
	}
	if seed == nil {
		if selectReq == nil || continuity == nil {
			return
		}
		key := selector.BuildContinuityKey(selectReq)
		if key.APIType == "" || continuity.VisibleOriginProviderID == "" {
			return
		}
		vendors := append([]string(nil), continuity.ContaminatedVendors...)
		if len(vendors) == 0 && continuity.VisibleOriginVendor != "" {
			vendors = append(vendors, continuity.VisibleOriginVendor)
		}
		scope := continuity.StrictestScope
		if scope == "" {
			scope = model.ScopeAny
		}
		seed = &model.VisibleContinuitySeed{
			SeedID: uuid.NewString(), ContinuityKey: key,
			OriginProviderID: continuity.VisibleOriginProviderID, OriginVendor: continuity.VisibleOriginVendor,
			ContaminatedVendors: vendors, StrictestScope: scope, ObservedAt: observedAt,
		}
	}
	h.visibleContinuitySeedStore.Store(*seed)
}

func shouldStoreWebSocketVisibleContinuitySeed(session *WebSocketSessionResult) bool {
	if session == nil || session.FinalResult == nil || !session.FinalResult.ClientVisible {
		return false
	}
	switch session.FinalResult.TerminalCause {
	case model.TerminalCleanClose, model.TerminalClientDisconnect:
		return false
	default:
		return true
	}
}

func (h *Gateway) selectProviderWithTracking(
	ctx context.Context,
	req *model.SelectRequest,
	attempt int,
	excluded map[string]bool,
) (ProviderSelection, error) {
	if h.selector == nil {
		provider, err := normalizeSelectedProvider(h.selectProviderFallback(ctx, req, attempt, excluded))
		if err != nil {
			return ProviderSelection{}, err
		}
		return ProviderSelection{
			Lease:    h.newFallbackProviderLease(provider),
			Metadata: selector.BuildSelectionMetadataAt(req, selector.SelectionSourceStrategy, time.Now()),
		}, nil
	}
	if attempt == 0 {
		result, err := normalizeProviderSelection(h.selector.SelectInitial(ctx, req))
		if err != nil {
			return ProviderSelection{}, err
		}
		if result.Metadata.UsesContinuity() {
			return result, nil
		}
		if active, found := h.tryActiveProviderFallback(ctx, req); found {
			released := result.Lease.Release()
			h.logger.Debug("websocket.initial_selection_replaced_by_active_continuity",
				zap.String("superseded_provider_id", result.Lease.ProviderID()),
				zap.Uint64("superseded_provider_generation", result.Lease.Generation()),
				zap.Bool("superseded_lease_released", released),
				zap.String("active_provider_id", active.Lease.ProviderID()),
				zap.Uint64("active_provider_generation", active.Lease.Generation()),
			)
			return active, nil
		}
		return result, nil
	}
	result, err := normalizeProviderSelection(h.selector.SelectAlternate(ctx, req, excluded))
	if err != nil {
		return ProviderSelection{}, err
	}
	if excluded != nil && excluded[result.Provider().ID] {
		result.Lease.Release()
		return ProviderSelection{}, internal.ErrNoProvider
	}
	return result, nil
}

func normalizeProviderSelection(result ProviderSelection, err error) (ProviderSelection, error) {
	if err != nil {
		if result.Lease != nil {
			result.Lease.Release()
		}
		return ProviderSelection{}, err
	}
	provider := result.Provider()
	if provider == nil || result.Lease == nil || !result.Lease.Held() ||
		result.Lease.ProviderID() != provider.ID || result.Lease.CapabilityIdentity() == 0 {
		if result.Lease != nil {
			result.Lease.Release()
		}
		return ProviderSelection{}, internal.ErrNoProvider
	}
	return result, nil
}

func normalizeSelectedProvider(provider *model.Provider, err error) (*model.Provider, error) {
	if err != nil {
		return nil, err
	}
	if provider == nil {
		return nil, internal.ErrNoProvider
	}
	return provider, nil
}

func (h *Gateway) tryActiveProviderFallback(ctx context.Context, req *model.SelectRequest) (ProviderSelection, bool) {
	if req == nil || req.StickyMode == model.StickyModeOff || h.activeSessions == nil {
		return ProviderSelection{}, false
	}
	activeLease, found := h.activeSessions.FindActiveLeaseForRequest(req)
	if !found || activeLease == nil || !activeLease.Held() || activeLease.Provider() == nil ||
		activeLease.ProviderID() != activeLease.Provider().ID || activeLease.CapabilityIdentity() == 0 {
		return ProviderSelection{}, false
	}
	rawResult, selectErr := h.selector.SelectActive(ctx, selectRequestForSameProviderRetry(req), activeLease)
	if rawResult.Lease != nil && rawResult.Lease.CapabilityIdentity() == activeLease.CapabilityIdentity() {
		// Even an adapter error cannot transfer cleanup of the existing session's
		// capability to this request.
		return ProviderSelection{}, false
	}
	result, err := normalizeProviderSelection(rawResult, selectErr)
	if err != nil {
		return ProviderSelection{}, false
	}
	if result.Lease.ProviderID() != activeLease.ProviderID() ||
		result.Lease.Generation() != activeLease.Generation() ||
		result.Lease.CapabilityIdentity() == activeLease.CapabilityIdentity() {
		// Returning the source capability would let either session release the
		// other's slot. A different invalid capability is ours to roll back; the
		// source lease remains owned by the existing active session.
		if result.Lease.CapabilityIdentity() != activeLease.CapabilityIdentity() {
			result.Lease.Release()
		}
		return ProviderSelection{}, false
	}
	result.Metadata = selector.BuildSelectionMetadataAt(req, selector.SelectionSourceActiveContinuity, time.Now())
	return result, true
}

func selectRequestForSameProviderRetry(req *model.SelectRequest) *model.SelectRequest {
	if req == nil {
		return nil
	}
	cloned := *req
	cloned.SwitchMode = model.SwitchModeInitial
	cloned.ProviderSwitchHistory = nil
	cloned.ProviderContinuityContext = nil
	cloned.VisibleContinuitySeedCandidate = nil
	cloned.FailoverContext = nil
	cloned.MaxProviderSwitches = 0
	return &cloned
}

func (h *Gateway) selectProviderFallback(ctx context.Context, req *model.SelectRequest, attempt int, excluded map[string]bool) (*model.Provider, error) {
	scope, err := selector.NewProviderSelectionEligibility(ctx, h.store, h.health, req)
	if err != nil {
		return nil, err
	}
	providers, err := h.store.ListProvidersByAPIType(ctx, req.APIType)
	if err != nil {
		return nil, err
	}
	available := providers[:0]
	for index := range providers {
		allowed, eligibilityErr := scope.AllowsProvider(ctx, &providers[index])
		if eligibilityErr != nil {
			return nil, eligibilityErr
		}
		if !excluded[providers[index].ID] && allowed {
			available = append(available, providers[index])
		}
	}
	if len(available) == 0 {
		return nil, internal.ErrNoProvider
	}
	index := h.fallbackCounter.Add(1)
	provider := available[int(uint64(index-1+int64(attempt))%uint64(len(available)))]
	return &provider, nil
}

const (
	authModeAuto    = "auto"
	authModeXAPI    = "x-api-key"
	headerUserAgent = "User-Agent"
)

var hopByHopHeaders = map[string]bool{
	"Connection": true, "Keep-Alive": true, "Proxy-Authenticate": true,
	"Proxy-Authorization": true, "Te": true, "Trailer": true,
	"Transfer-Encoding": true, "Upgrade": true,
}

func isAuthHeader(key string) bool {
	lower := strings.ToLower(key)
	return lower == "authorization" || lower == "x-api-key"
}

func isWebSocketHandshakeHeader(key string) bool {
	return len(key) > 14 && strings.EqualFold(key[:14], "Sec-Websocket-")
}

func detectAuthMode(r *http.Request) string {
	if r.Header.Get("Authorization") != "" {
		return "bearer"
	}
	if r.Header.Get("X-Api-Key") != "" {
		return authModeXAPI
	}
	return "bearer"
}

func SetAuthHeader(dst http.Header, apiKey, providerMode, globalMode string, original *http.Request) {
	mode := providerMode
	if mode == "" {
		mode = globalMode
	}
	if mode == authModeAuto {
		mode = detectAuthMode(original)
	}
	if mode == authModeXAPI {
		dst.Set("x-api-key", apiKey)
		return
	}
	dst.Set("Authorization", "Bearer "+apiKey)
}

func EnsureExplicitUserAgentHeader(headers http.Header) {
	if len(headers.Values(headerUserAgent)) == 0 {
		headers.Set(headerUserAgent, "")
	}
}
