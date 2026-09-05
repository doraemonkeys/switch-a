package proxy

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/codex/clientdisguise/wire"
	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/codex/disguiseruntime"
	"github.com/doraemonkeys/switch-a/internal/codex/http"
	"github.com/doraemonkeys/switch-a/internal/codex/identity"
	"github.com/doraemonkeys/switch-a/internal/errorrule"
	"github.com/doraemonkeys/switch-a/internal/errorrule/statistics"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/requestcapture"
	"github.com/doraemonkeys/switch-a/internal/requestcapture/capturefailure"
	"github.com/doraemonkeys/switch-a/internal/requestingress"
	"github.com/doraemonkeys/switch-a/internal/requestingress/clientconnection"
	"github.com/doraemonkeys/switch-a/internal/responseanalysis"
	"github.com/doraemonkeys/switch-a/internal/responseanalysis/tokenusage"
	"github.com/doraemonkeys/switch-a/internal/selector"
	"github.com/doraemonkeys/switch-a/internal/upstreamtarget"
	"github.com/doraemonkeys/switch-a/internal/upstreamtransport"
	"github.com/doraemonkeys/switch-a/internal/websocketproxy"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

type TokenUsage = tokenusage.TokenUsage

// Handler handles proxy requests.
type Handler struct {
	transportOverride          HTTPTransport
	startIngress               func(context.Context, *http.Request, requestingress.Options) (*requestingress.Handle, error)
	store                      Store
	selector                   Selector
	httpSelector               httpProviderSelector
	health                     internal.HealthManager
	activeRegistry             *ActiveRequestRegistry
	visibleContinuitySeedStore model.VisibleContinuitySeedStore
	logger                     *zap.Logger
	mu                         sync.RWMutex
	transport                  *Transport
	lastCfg                    *transportCacheKey
	fallbackCounter            atomic.Int64 // Counter for true round-robin in fallback mode
	webSocketGateway           *websocketproxy.Gateway
	auth                       ProviderAuthenticator
	usageObserver              ProviderUsageObserver
	capture                    RequestCapture
	ruleSets                   errorrule.RuleSetProvider
	analyzer                   ResponseAnalyzer
	ruleStats                  RuleStatistics
	backoff                    BackoffWaiter
	requestLogInsertTimeout    time.Duration
	codexHTTP                  *codexhttp.Runtime
	clientDisguise             ClientDisguiseRepository
	disguisePool               *upstreamtransport.Pool
}

type ClientDisguiseRepository interface {
	disguiseruntime.Repository
	wire.Mapper
}

// Store defines the minimal storage interface needed by the proxy handler.
type Store interface {
	ListProvidersByAPIType(ctx context.Context, apiType string) ([]model.Provider, error)
	GetConfig(ctx context.Context, key string) (string, error)
	InsertLog(ctx context.Context, log *model.RequestLog) error
	InsertAttempts(ctx context.Context, attempts []model.RequestAttempt) error
}

// Selector defines the provider selection interface.
type Selector interface {
	UpdateStickyWithTTL(req *model.SelectRequest, providerID string, ttl time.Duration)
	EvictProviderContinuity(providerID string)
}

// HTTPTransport is the forwarding surface consumed by one gateway request.
type HTTPTransport interface {
	FetchUpstream(
		context.Context,
		*http.Request,
		upstreamtransport.ExecutionOptions,
	) (*upstreamtransport.Response, upstreamtransport.RequestDisclosure, error)
}

type ResponseAnalyzer interface {
	Start(context.Context, responseanalysis.StartInput) *responseanalysis.PendingResponse
}

type RuleStatistics interface {
	Hit(statistics.Handle, time.Time) error
}

type BackoffWaiter interface {
	Wait(context.Context, time.Duration) error
}

type timerBackoffWaiter struct{}

func (timerBackoffWaiter) Wait(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type staticRuleSetProvider struct {
	snapshot *errorrule.CompiledRuleSet
}

func (p staticRuleSetProvider) CurrentRuleSet() *errorrule.CompiledRuleSet {
	return p.snapshot
}

// ProviderAuthenticator is defined at the proxy boundary so credential refresh
// can be fault-injected without coupling request orchestration to its producer.
type ProviderAuthenticator interface {
	ApplyProviderCredentials(context.Context, http.Header, codexidentity.CandidateSnapshot, string, string, *http.Request, *url.URL) (codexidentity.AppliedIdentity, error)
	RefreshCredentialSession(context.Context, credentialsession.Snapshot) (bool, error)
}

// ProviderUsageObserver is separate from credential injection because quota
// observation is an optional post-response write, not an authentication step.
type ProviderUsageObserver interface {
	ObserveCredentialSessionUsage(context.Context, string, *model.ProviderUsageSnapshot) error
}

// RequestCapture is the proxy-owned view of the process capture manager.
type RequestCapture interface {
	Enabled() bool
	BeginGateway(requestcapture.GatewayStart) requestcapture.GatewayRecorder
}

type webSocketActiveSessions struct {
	registry *ActiveRequestRegistry
}

func newWebSocketActiveSessions(registry *ActiveRequestRegistry) websocketproxy.ActiveSessions {
	if registry == nil {
		return nil
	}
	return webSocketActiveSessions{registry: registry}
}

type webSocketLiveTraffic struct {
	tracker *LiveBytesTracker
}

func (webSocketActiveSessions) NewLiveTraffic() websocketproxy.LiveTraffic {
	return webSocketLiveTraffic{tracker: &LiveBytesTracker{}}
}

func (sessions webSocketActiveSessions) Register(
	session websocketproxy.ActiveSession,
	done <-chan struct{},
	traffic websocketproxy.LiveTraffic,
) bool {
	if session.Lease == nil || session.Lease.Provider() == nil {
		return false
	}
	registered := sessions.registry.RegisterWithDone(&ActiveRequest{
		RequestID: session.RequestID, ProviderID: session.Lease.ProviderID(),
		Model: session.Model, APIType: session.APIType, UserID: session.UserID,
		ClientIP: session.ClientIP, StickyMode: session.StickyMode,
		ContinuityKey: session.ContinuityKey, IsWebSocket: true,
		StartedAt:                     session.StartedAt,
		RequestedReasoningObservation: session.Reasoning,
		lease:                         session.Lease,
	}, done)
	if !registered {
		return false
	}
	if live, ok := traffic.(webSocketLiveTraffic); ok {
		sessions.registry.RegisterLiveBytes(session.RequestID, live.tracker)
	}
	return true
}

func (sessions webSocketActiveSessions) Unregister(requestID string) bool {
	return sessions.registry.Unregister(requestID)
}

func (sessions webSocketActiveSessions) UpdateModel(requestID, modelName string) {
	sessions.registry.UpdateModel(requestID, modelName)
}

func (sessions webSocketActiveSessions) MarkDataReceived(requestID string) {
	sessions.registry.MarkDataReceived(requestID)
}

func (sessions webSocketActiveSessions) FindActiveLeaseForRequest(
	req *model.SelectRequest,
) (websocketproxy.ProviderLease, bool) {
	return sessions.registry.FindActiveLeaseForRequest(req)
}

type webSocketSelectorAdapter struct {
	routing    Selector
	capability httpProviderSelector
}

func newWebSocketSelectorAdapter(routing Selector, capability httpProviderSelector) websocketproxy.Selector {
	if routing == nil || capability == nil {
		return nil
	}
	return webSocketSelectorAdapter{routing: routing, capability: capability}
}

func (a webSocketSelectorAdapter) SelectInitial(
	ctx context.Context,
	request *model.SelectRequest,
) (websocketproxy.ProviderSelection, error) {
	selection, err := a.capability.SelectInitial(ctx, request)
	return websocketSelection(selection, err)
}

func (a webSocketSelectorAdapter) SelectActive(
	ctx context.Context,
	request *model.SelectRequest,
	active websocketproxy.ProviderLease,
) (websocketproxy.ProviderSelection, error) {
	lease, ok := active.(providerLease)
	if !ok {
		return websocketproxy.ProviderSelection{}, internal.ErrNoProvider
	}
	selection, err := a.capability.SelectActive(ctx, request, lease)
	return websocketSelection(selection, err)
}

func (a webSocketSelectorAdapter) SelectAlternate(
	ctx context.Context,
	request *model.SelectRequest,
	excluded map[string]bool,
) (websocketproxy.ProviderSelection, error) {
	reservation, err := a.capability.ReserveAlternate(ctx, request, excluded)
	if err != nil {
		return websocketproxy.ProviderSelection{}, err
	}
	activated := false
	defer func() {
		if !activated {
			reservation.Release()
		}
	}()
	if err := reservation.PrepareActivation(ctx); err != nil {
		return websocketproxy.ProviderSelection{}, err
	}
	lease := reservation.Activate()
	if lease == nil || !lease.Held() {
		return websocketproxy.ProviderSelection{}, internal.ErrNoProvider
	}
	activated = true
	return websocketproxy.ProviderSelection{
		Lease:    lease,
		Metadata: reservation.Metadata(),
	}, nil
}

func (a webSocketSelectorAdapter) UpdateStickyWithTTL(
	request *model.SelectRequest,
	providerID string,
	ttl time.Duration,
) {
	a.routing.UpdateStickyWithTTL(request, providerID, ttl)
}

func (a webSocketSelectorAdapter) EvictProviderContinuity(providerID string) {
	a.routing.EvictProviderContinuity(providerID)
}

func websocketSelection(
	selection *providerSelection,
	err error,
) (websocketproxy.ProviderSelection, error) {
	if selection != nil && selection.lease != nil {
		adapted := websocketproxy.ProviderSelection{
			Lease:    selection.lease,
			Metadata: selection.metadata,
		}
		if err != nil {
			// The WebSocket gateway owns cleanup once a lease crosses this
			// boundary, including the uncommon result-plus-error selector case.
			return adapted, err
		}
		if selection.provider != nil && selection.lease.Held() {
			return adapted, nil
		}
	}
	if err != nil {
		return websocketproxy.ProviderSelection{}, err
	}
	return websocketproxy.ProviderSelection{}, internal.ErrNoProvider
}

func (traffic webSocketLiveTraffic) ObserveClientToUpstream(bytes int64) {
	traffic.tracker.BytesSent.Add(bytes)
	traffic.tracker.MsgsSent.Add(1)
	traffic.tracker.LastActivityAt.Store(time.Now().UnixMilli())
}

func (traffic webSocketLiveTraffic) ObserveUpstreamToClient(bytes int64) {
	traffic.tracker.BytesReceived.Add(bytes)
	traffic.tracker.MsgsReceived.Add(1)
	traffic.tracker.LastActivityAt.Store(time.Now().UnixMilli())
}

// proxyContext aggregates per-request state to reduce method signature complexity.
// Immutable fields (r, cfg, apiType, body, info, startTime, requestID) are set once during construction.
// Mutable fields (selectReq, isSticky, attempts) are modified during retry orchestration.
type proxyContext struct {
	handler             *Handler
	r                   *http.Request
	w                   http.ResponseWriter
	cfg                 *runtimeConfig
	transport           HTTPTransport
	apiType             string
	ingress             *requestingress.Handle
	upload              *ingressUpload
	operation           *clientconnection.Operation
	captureIngress      requestcapture.IngressRecorder
	responseCommitted   atomic.Bool
	facts               *requestFacts
	info                RequestInfo
	selectReq           *model.SelectRequest
	startTime           time.Time
	requestID           string // UUID for this request
	capture             requestcapture.GatewayRecorder
	captureParticipates bool
	liveBytes           *LiveBytesTracker      // Logical-request traffic shared across provider attempts
	isSticky            bool                   // Whether provider came from sticky cache
	attempts            []model.RequestAttempt // Attempts made during this request
	usageObservations   map[string]*model.ProviderUsageSnapshot
	codex               *codexhttp.Operation
	disguise            *httpDisguiseOperation
}

// ServeHTTP handles proxy requests.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	startTime := time.Now()
	requestID := uuid.New().String()

	// Load runtime configuration (immutable per-request)
	cfg, err := h.loadConfig(ctx)
	if err != nil {
		h.logger.Error("failed to load config", zap.Error(err))
		h.writeGatewayError(w, http.StatusInternalServerError, ErrCodeInternalError, "Failed to load configuration")
		return
	}

	// Resolve from the escaped wire path so encoded slashes remain segment data
	// throughout routing, endpoint policy, and upstream target construction.
	requestRoute, ok := ResolveRequestURL(r.Method, r.URL)
	if !ok {
		h.logger.Warn("unknown API type",
			zap.String("path", r.URL.Path),
			zap.String("escaped_path", r.URL.EscapedPath()),
		)
		h.writeGatewayError(w, http.StatusBadRequest, ErrCodeUnknownAPIType, fmt.Sprintf("Unknown API path: %s", r.URL.Path))
		return
	}
	apiType := requestRoute.APIType

	// WebSocket upgrade is only valid for Codex (OpenAI Realtime API).
	// Reject upgrades on other API types to prevent health metric pollution
	// from failed WS dials to non-WebSocket backends.
	if websocketproxy.IsUpgrade(r) {
		if apiType != APITypeCodex {
			h.logger.Warn("websocket upgrade rejected for unsupported API type",
				zap.String("api_type", apiType),
				zap.String("remote_addr", r.RemoteAddr),
			)
			h.writeGatewayError(w, http.StatusBadRequest, ErrCodeWebSocketUpgrade,
				fmt.Sprintf("WebSocket upgrade is not supported for API type %q", apiType))
			return
		}
		h.webSocketGateway.Handle(ctx, w, r, websocketproxy.RequestConfig{
			GlobalAuthMode:             cfg.globalAuthMode,
			GlobalMaxAttempts:          cfg.globalMaxAttempts,
			StickyMode:                 cfg.stickyMode,
			ConversationRecoveryPolicy: cfg.ConversationRecoveryPolicy,
			StickyTTL:                  cfg.stickyTTL,
			TrustProxy:                 cfg.trustProxy,
			UserHeader:                 cfg.userHeader,
			ProbeClientModel:           cfg.websocketProbeClientModel,
		}, apiType, requestID, startTime)
		return
	}

	// GET /responses exists solely for WebSocket (OpenAI Realtime API).
	// A plain GET without Upgrade is meaningless there — reject early.
	// The resolved route owns this endpoint capability so encoded path data
	// cannot be reinterpreted as separators by a later decoded-path check.
	if requestRoute.RequiresWebSocketUpgrade {
		h.writeGatewayError(w, http.StatusUpgradeRequired, ErrCodeWebSocketUpgrade, "This endpoint requires a WebSocket upgrade")
		return
	}

	h.serveHTTPIngress(w, r, cfg, apiType, requestID, startTime)

}

func (h *Handler) beginGatewayCapture(requestID string, startedAt time.Time) requestcapture.GatewayRecorder {
	if h.capture == nil || !h.capture.Enabled() {
		return requestcapture.GatewayRecorder{}
	}
	return h.capture.BeginGateway(requestcapture.GatewayStart{
		GatewayRequestID: requestID,
		StartedAt:        startedAt,
	})
}

func gatewayCaptureOutcome(ctx context.Context) requestcapture.GatewayOutcome {
	err := ctx.Err()
	if err == nil {
		return requestcapture.GatewayOutcome{}
	}

	var sourceFailure *requestIngressFailure
	if errors.As(context.Cause(ctx), &sourceFailure) {
		peer := requestcapture.FailurePeerGateway
		if sourceFailure.kind != requestingress.FailureStorage {
			peer = requestcapture.FailurePeerClient
		}
		return requestcapture.GatewayOutcome{
			TerminationReason: requestcapture.TerminationReasonReadError,
			Failure:           capturefailure.Observation(capturefailure.FromError(requestcapture.FailureSiteGateway, peer, requestcapture.FailureClassRead, requestcapture.FailureCodeGatewayIngress, sourceFailure), requestcapture.FailureFact{}),
		}
	}
	peer := requestcapture.FailurePeerGateway
	class := requestcapture.FailureClassCanceled
	reason := requestcapture.TerminationReasonCanceled
	if contextClass, known := capturefailure.ContextClass(err); known {
		class = contextClass
		switch contextClass {
		case requestcapture.FailureClassTimeout:
			reason = requestcapture.TerminationReasonTimeout
		case requestcapture.FailureClassCanceled:
			peer = requestcapture.FailurePeerClient
			reason = requestcapture.TerminationReasonClientDisconnect
		}
	}
	return requestcapture.GatewayOutcome{
		TerminationReason: reason,
		Failure: capturefailure.Observation(
			capturefailure.FromError(
				requestcapture.FailureSiteGateway,
				peer,
				class,
				requestcapture.FailureCodeGatewayContext,
				err,
			),
			requestcapture.FailureFact{},
		),
	}
}

// handleBodyError handles body read errors.
func (h *Handler) handleBodyError(w http.ResponseWriter, err error, maxSize int64) {
	if errors.Is(err, ErrBodyTooLarge) || errors.Is(err, requestingress.ErrBodyTooLarge) {
		h.logger.Warn("request body too large", zap.Int64("max_size_mb", maxSize))
		h.writeGatewayError(w, http.StatusRequestEntityTooLarge, ErrCodeBodyTooLarge, fmt.Sprintf("Request body exceeds %d MB limit", maxSize))
		return
	}
	h.logger.Error("failed to read request body", zap.Error(err))
	h.writeGatewayError(w, http.StatusInternalServerError, ErrCodeInternalError, "Failed to read request body")
}

// retryState tracks mutable state across retry attempts in executeProxy.
type retryState struct {
	ledger            errorrule.RetryLedger
	lastErr           error
	providerUsed      *model.Provider
	statusCode        int
	success           bool
	isSSE             bool
	headersWritten    bool
	responseCommitted bool
	clientTermination clientTermination
	semanticError     bool
	// Transport observation plumbing — mirrored from forwardResult so the
	// final logRequest/evidence path can reconstruct the observation without
	// re-running forwarding logic. Only the last attempt's values survive
	// here (exhausted retries flow through finalizeProxy), matching how the
	// existing err/statusCode/... fields behave.
	firstByteVisible   bool
	isStatusFailover   bool
	isClientWriteError bool
	excludedProviders  map[string]bool
	currentProvider    *model.Provider
	currentLease       providerLease
	// providerAttempt is 0-based within a single provider. A provider with MaxRetries=N
	// can be attempted (N+1) times: providerAttempt 0..N.
	providerAttempt  int
	activeRegistered bool
	// switchTracker keeps pre-visible replacement separate from visible failover
	// so selector isolation only activates after the upstream becomes observable.
	switchTracker     providerSwitchTracker
	selectionMode     model.SwitchMode
	selectionMetadata selector.SelectionMetadata
	// firstTokenMs tracks Time To First Token for SSE requests (in milliseconds).
	// Only set for successful SSE responses when data starts flowing.
	firstTokenMs *int64
	// responseBytes tracks total bytes written to client for transfer statistics.
	responseBytes int64
	// tokenUsage tracks token usage extracted from response (Phase 4a).
	// Only set for successful 2xx responses that contain usage data.
	tokenUsage *TokenUsage
	// failureDisposition carries provider-scoped retry semantics inferred from the
	// last upstream failure, such as "switch now" or "suspend until reset".
	failureDisposition providerFailureDisposition
	// injectedCredential is attempt-scoped evidence derived from the final
	// sanitized and applied request, including any refreshed ChatGPT token.
	injectedCredential string
}

// registerActiveRequest registers or updates the active request in the registry.
func (h *Handler) registerActiveRequest(pctx *proxyContext, state *retryState) {
	if h.activeRegistry == nil {
		return
	}
	registered := h.activeRegistry.RegisterWithDone(&ActiveRequest{
		RequestID:                     pctx.requestID,
		ProviderID:                    state.currentProvider.ID,
		Model:                         pctx.info.Model,
		APIType:                       pctx.apiType,
		UserID:                        pctx.info.UserID,
		ClientIP:                      pctx.info.ClientIP,
		StickyMode:                    pctx.selectReq.StickyMode,
		ContinuityKey:                 selector.BuildContinuityKey(pctx.selectReq),
		IsSSE:                         false, // Updated after response type is known
		StartedAt:                     pctx.startTime,
		RequestedReasoningObservation: pctx.info.Reasoning,
		lease:                         state.currentLease,
	}, pctx.r.Context().Done())
	if !registered {
		state.activeRegistered = false
		return
	}
	h.activeRegistry.RegisterLiveBytes(pctx.requestID, pctx.liveBytes)
	state.activeRegistered = true
}

// recordAttempt records a request attempt in the proxy context.
func (h *Handler) recordAttempt(
	pctx *proxyContext,
	attemptContext httpAttemptContext,
	result forwardResult,
	attempt int,
	attemptStart time.Time,
) {
	createdAt := time.Now()
	attemptRecord := newNormalizedRequestAttempt(pctx.requestID, attemptContext.provider.ID, createdAt)
	attemptRecord.Attempt = attempt
	attemptRecord.SwitchMode = requestAttemptSwitchMode(attemptContext.switchMode)
	attemptRecord.ProviderAttempt = attemptContext.providerAttemptIndex + 1
	attemptRecord.ProviderSwitchCount = attemptContext.providerSwitchCount
	attemptRecord.StatusCode = result.statusCode
	attemptRecord.BodySnippet = result.bodySnippet
	attemptRecord.LatencyMs = time.Since(attemptStart).Milliseconds()
	attemptRecord.SwitchReason = result.switchReason
	attemptRecord.ContinuitySeeded = attemptContext.selectionMetadata.ContinuitySeeded
	attemptRecord.ContinuityOriginProviderID = attemptContext.selectionMetadata.ContinuityOriginProviderID
	attemptRecord.ContinuitySeedAgeMs = selectionMetadataContinuitySeedAgeMs(attemptContext.selectionMetadata)
	if result.failureMessage != "" {
		attemptRecord.Error = result.failureMessage
		// Include request body snippet for error attempts to help diagnose issues.
		// SECURITY NOTE: This may expose sensitive data (API keys, tokens, PII) in the
		// request_attempts table. Administrators should be aware that error diagnostics
		// include partial request content. The snippet is truncated to maxSnippetBytes to limit
		// exposure. Consider the security implications when granting access to logs/attempts data.
		attemptRecord.ReqBodySnippet = pctx.requestBodySnippet()
	}
	pctx.attempts = append(pctx.attempts, attemptRecord)
}

// buildProviderRequest validates the provider's endpoint/auth config and
// constructs the upstream HTTP request.
func (h *Handler) buildProviderRequest(
	ctx context.Context,
	pctx *proxyContext,
	provider *model.Provider,
) (*http.Request, requestcapture.FailureCode, error) {
	baseURL := provider.BaseURLForAPIType(pctx.apiType)

	// Fail fast if provider has no BaseURL configured for this API type.
	// This prevents forwarding requests to invalid URLs (just the path with no host),
	// which would produce cryptic errors instead of a clear diagnostic message.
	if baseURL == "" {
		h.logger.Error("missing base_url for api_type",
			zap.String("provider_id", provider.ID),
			zap.String("api_type", pctx.apiType),
		)
		return nil, requestcapture.FailureCodeMissingBaseURL,
			fmt.Errorf("provider %q has no base_url configured for api_type %q", provider.ID, pctx.apiType)
	}

	upstreamURL, err := upstreamtarget.Build(baseURL, pctx.r.URL, pctx.apiType)
	if err != nil {
		h.logger.Error("failed to build upstream target",
			zap.String("request_id", pctx.requestID),
			zap.String("provider_id", provider.ID),
			zap.String("api_type", pctx.apiType),
			zap.Error(err),
		)
		return nil, requestcapture.FailureCodeRequestBuild, fmt.Errorf("build upstream target: %w", err)
	}

	req, err := BuildUpstreamRequestWithPolicy(
		ctx, pctx.r.Method, upstreamURL.String(), pctx.upload, pctx.r, pctx.codex.RequestPolicy(),
	)
	if err != nil {
		h.logger.Error("failed to build upstream request", zap.Error(err))
		return nil, requestcapture.FailureCodeRequestBuild, err
	}

	return req, "", nil
}
