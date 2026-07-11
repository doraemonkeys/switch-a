package proxy

import (
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"switch-a/internal"
	"switch-a/internal/model"
	"switch-a/internal/selector"
)

var _ model.VisibleContinuitySeedStore = (*MemoryVisibleContinuitySeedStore)(nil)

// ActiveRequest represents a request currently being processed by the proxy.
// It captures metadata needed for live monitoring without storing the full request/response.
type ActiveRequest struct {
	RequestID       string           `json:"request_id"`   // UUID identifying this request
	ProviderID      string           `json:"provider_id"`  // Selected provider handling the request
	Model           string           `json:"model"`        // Model being called (e.g., "claude-3-opus")
	APIType         string           `json:"api_type"`     // API type (claude, codex, gemini, grok, custom:*)
	UserID          string           `json:"user_id"`      // User identifier from header
	ClientIP        string           `json:"client_ip"`    // Client IP address
	StickyMode      model.StickyMode `json:"-"`            // Sticky dimensions captured when selection happened
	ContinuityKey   model.StickyKey  `json:"-"`            // Routing dimensions known at selection time
	IsSSE           bool             `json:"is_sse"`       // Whether this is an SSE streaming request
	IsWebSocket     bool             `json:"is_websocket"` // Whether this is a WebSocket connection
	StartedAt       time.Time        `json:"started_at"`   // When the request started
	HasReceivedData bool             `json:"has_data"`     // Whether data has been received from upstream
	model.RequestedReasoningObservation
	BytesSent      int64 `json:"bytes_sent,omitempty"`       // Cumulative request payload bytes forwarded upstream
	BytesReceived  int64 `json:"bytes_received,omitempty"`   // Cumulative response payload bytes forwarded to the client
	MsgsSent       int64 `json:"msgs_sent,omitempty"`        // WebSocket messages sent upstream; zero for HTTP/SSE
	MsgsReceived   int64 `json:"msgs_received,omitempty"`    // WebSocket messages received upstream; zero for HTTP/SSE
	LastActivityAt int64 `json:"last_activity_at,omitempty"` // Unix ms of most recent transport activity, 0 = no activity yet
}

// ActiveRequestRemovalReason explains why a request left the registry.
// Callers can distinguish definitive request termination from heuristic cleanup.
type ActiveRequestRemovalReason string

const (
	// ActiveRequestRemovalReasonExplicit means request teardown called Unregister.
	ActiveRequestRemovalReasonExplicit ActiveRequestRemovalReason = "explicit"
	// ActiveRequestRemovalReasonProviderHandoff means the same logical request moved
	// to a different provider and the prior provider slot can be retired.
	ActiveRequestRemovalReasonProviderHandoff ActiveRequestRemovalReason = "provider_handoff"
	// ActiveRequestRemovalReasonOrphaned means the request context has already ended,
	// so the registry is only reclaiming bookkeeping that should have been removed.
	ActiveRequestRemovalReasonOrphaned ActiveRequestRemovalReason = "orphaned"
	// ActiveRequestRemovalReasonStale is a best-effort fallback for entries that were
	// registered without any lifecycle signal. This is not strong enough evidence to
	// reclaim provider concurrency on its own.
	ActiveRequestRemovalReasonStale ActiveRequestRemovalReason = "stale"
)

// ActiveRequestRemovalHook runs after a request leaves the registry.
type ActiveRequestRemovalHook func(ActiveRequest, ActiveRequestRemovalReason)

// LiveBytesTracker provides lock-free counters shared by HTTP, SSE, and WebSocket
// transports. Writers update it on the hot path while List reads a coherent-enough
// monitoring snapshot without serializing transport activity through the registry.
type LiveBytesTracker struct {
	BytesSent      atomic.Int64 // client → upstream
	BytesReceived  atomic.Int64 // upstream → client
	MsgsSent       atomic.Int64
	MsgsReceived   atomic.Int64
	LastActivityAt atomic.Int64 // UnixMilli of most recent transport activity
}

// MemoryVisibleContinuitySeedStore keeps short-lived post-visible continuity
// seeds out of the live request registry. This preserves the one-shot,
// cross-request handoff semantics without conflating active requests with ended
// requests that merely left a continuity breadcrumb behind.
type MemoryVisibleContinuitySeedStore struct {
	mu    sync.RWMutex
	seeds map[model.StickyKey]model.VisibleContinuitySeed
	clock internal.Clock
}

func NewVisibleContinuitySeedStore() *MemoryVisibleContinuitySeedStore {
	return NewVisibleContinuitySeedStoreWithClock(internal.RealClock{})
}

func NewVisibleContinuitySeedStoreWithClock(clock internal.Clock) *MemoryVisibleContinuitySeedStore {
	return &MemoryVisibleContinuitySeedStore{
		seeds: make(map[model.StickyKey]model.VisibleContinuitySeed),
		clock: clock,
	}
}

func (s *MemoryVisibleContinuitySeedStore) Lookup(key model.StickyKey) (*model.VisibleContinuitySeedCandidate, bool) {
	now := s.clock.Now()

	s.mu.Lock()
	defer s.mu.Unlock()

	seed, ok := s.seeds[key]
	if !ok {
		return nil, false
	}
	if seedExpired(seed, now) {
		delete(s.seeds, key)
		return nil, false
	}
	return seed.Candidate(now), true
}

func (s *MemoryVisibleContinuitySeedStore) Store(seed model.VisibleContinuitySeed) {
	if seed.ObservedAt.IsZero() {
		// The store owns TTL enforcement, so it normalizes missing timestamps at the
		// boundary rather than letting zero values live forever or lose overwrite
		// ordering under tests.
		seed.ObservedAt = s.clock.Now()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	existing, ok := s.seeds[seed.ContinuityKey]
	if ok && !seedExpired(existing, s.clock.Now()) && existing.ObservedAt.After(seed.ObservedAt) {
		return
	}
	s.seeds[seed.ContinuityKey] = *seed.Clone()
}

func (s *MemoryVisibleContinuitySeedStore) CompareAndConsume(
	key model.StickyKey,
	seedID string,
) (*model.VisibleContinuitySeed, bool) {
	now := s.clock.Now()

	s.mu.Lock()
	defer s.mu.Unlock()

	seed, ok := s.seeds[key]
	if !ok {
		return nil, false
	}
	if seedExpired(seed, now) {
		delete(s.seeds, key)
		return nil, false
	}
	if seedID == "" || seed.SeedID != seedID {
		return nil, false
	}

	delete(s.seeds, key)
	return seed.Clone(), true
}

func (s *MemoryVisibleContinuitySeedStore) CleanupExpired() int {
	now := s.clock.Now()

	s.mu.Lock()
	defer s.mu.Unlock()

	removed := 0
	for key, seed := range s.seeds {
		if !seedExpired(seed, now) {
			continue
		}
		delete(s.seeds, key)
		removed++
	}
	return removed
}

// Background sweeping bounds memory growth for seeds that never see another
// lookup. Expiration semantics still come from seedExpired, so the loop only
// reclaims entries that are already invisible to callers.
func (s *MemoryVisibleContinuitySeedStore) StartCleanupLoop(interval time.Duration) (stop func()) {
	done := make(chan struct{})
	var wg sync.WaitGroup

	wg.Go(func() {
		ticker := s.clock.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.CleanupExpired()
			case <-done:
				return
			}
		}
	})

	return func() {
		close(done)
		wg.Wait()
	}
}

func (s *MemoryVisibleContinuitySeedStore) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.seeds)
}

func seedExpired(seed model.VisibleContinuitySeed, now time.Time) bool {
	if seed.ObservedAt.IsZero() {
		return true
	}
	return now.Sub(seed.ObservedAt) > model.VisibleContinuitySeedTTL
}

// ActiveRequestRegistry tracks requests currently being processed.
//
// Design note: This is a single-instance, in-memory implementation.
// It does not support distributed deployments with multiple proxy instances.
// For multi-instance scenarios, consider using Redis or a similar distributed store.
type activeRequestEntry struct {
	request ActiveRequest
	done    <-chan struct{}
}

type ActiveRequestRegistry struct {
	mu          sync.RWMutex
	requests    map[string]activeRequestEntry
	stickyIndex map[model.StickyKey]map[string]struct{} // stickyKey -> requestIDs for O(1) lookup
	keyIndex    map[string]model.StickyKey              // requestID -> sticky key used during registration
	liveTraffic map[string]*LiveBytesTracker            // requestID -> transport-neutral live counters
	stopCh      chan struct{}                           // Channel to signal cleanup goroutine to stop
	cleanupWg   sync.WaitGroup                          // WaitGroup to confirm cleanup goroutine has exited
	clock       internal.Clock                          // Injected clock for testability
	removalHook ActiveRequestRemovalHook                // Unified request teardown side effects
}

// NewActiveRequestRegistry creates a new registry for tracking active requests.
func NewActiveRequestRegistry() *ActiveRequestRegistry {
	return NewActiveRequestRegistryWithHook(nil)
}

// NewActiveRequestRegistryWithHook creates a new registry with a removal hook.
func NewActiveRequestRegistryWithHook(removalHook ActiveRequestRemovalHook) *ActiveRequestRegistry {
	return &ActiveRequestRegistry{
		requests:    make(map[string]activeRequestEntry),
		stickyIndex: make(map[model.StickyKey]map[string]struct{}),
		keyIndex:    make(map[string]model.StickyKey),
		liveTraffic: make(map[string]*LiveBytesTracker),
		clock:       internal.RealClock{},
		removalHook: removalHook,
	}
}

// NewActiveRequestRegistryWithClock creates a new registry with a custom clock for testing.
func NewActiveRequestRegistryWithClock(clock internal.Clock) *ActiveRequestRegistry {
	return NewActiveRequestRegistryWithClockAndHook(clock, nil)
}

// NewActiveRequestRegistryWithClockAndHook creates a new registry with a custom
// clock and removal hook for tests that need deterministic cleanup plus real
// lifecycle side effects.
func NewActiveRequestRegistryWithClockAndHook(clock internal.Clock, removalHook ActiveRequestRemovalHook) *ActiveRequestRegistry {
	return &ActiveRequestRegistry{
		requests:    make(map[string]activeRequestEntry),
		stickyIndex: make(map[model.StickyKey]map[string]struct{}),
		keyIndex:    make(map[string]model.StickyKey),
		liveTraffic: make(map[string]*LiveBytesTracker),
		clock:       clock,
		removalHook: removalHook,
	}
}

// SetStickyPerModel is retained as a compatibility no-op while continuity keys
// are derived per request instead of from a mutable registry-wide switch.
func (r *ActiveRequestRegistry) SetStickyPerModel(_ bool) {
}

func (r *ActiveRequestRegistry) buildKeyFromRequest(req *model.SelectRequest) model.StickyKey {
	return selector.BuildContinuityKey(req)
}

func (r *ActiveRequestRegistry) buildKey(req *ActiveRequest) model.StickyKey {
	if req == nil {
		return model.StickyKey{}
	}
	if req.ContinuityKey != (model.StickyKey{}) {
		return req.ContinuityKey
	}
	return r.buildKeyFromRequest(&model.SelectRequest{
		ClientIP:   req.ClientIP,
		User:       req.UserID,
		APIType:    req.APIType,
		Model:      req.Model,
		StickyMode: req.StickyMode,
	})
}

// removeFromStickyIndex removes a request ID from sticky/key indexes if present.
// Caller must hold r.mu.
func (r *ActiveRequestRegistry) removeFromStickyIndex(requestID string) {
	key, hasKey := r.keyIndex[requestID]
	if !hasKey {
		return
	}

	if ids := r.stickyIndex[key]; ids != nil {
		delete(ids, requestID)
		if len(ids) == 0 {
			delete(r.stickyIndex, key)
		}
	}
	delete(r.keyIndex, requestID)
}

// removeLocked detaches a request from every registry index while the caller holds r.mu.
func (r *ActiveRequestRegistry) removeLocked(requestID string) (ActiveRequest, bool) {
	entry, ok := r.requests[requestID]
	if !ok {
		return ActiveRequest{}, false
	}

	r.removeFromStickyIndex(requestID)
	delete(r.requests, requestID)
	delete(r.liveTraffic, requestID)
	return entry.request, true
}

func (r *ActiveRequestRegistry) runRemovalHook(req ActiveRequest, reason ActiveRequestRemovalReason) {
	if r.removalHook == nil || req.ProviderID == "" {
		return
	}
	r.removalHook(req, reason)
}

// Register overwrites any existing entry with the same ID to handle retry scenarios
// where the same request ID may be re-registered with updated provider information.
func (r *ActiveRequestRegistry) Register(req *ActiveRequest) {
	r.RegisterWithDone(req, nil)
}

// RegisterWithDone records a request plus the lifecycle signal that closes when
// the surrounding transport context ends. Cleanup only treats ended contexts as
// orphaned work, so quiet-but-live requests cannot be reclaimed heuristically.
func (r *ActiveRequestRegistry) RegisterWithDone(req *ActiveRequest, done <-chan struct{}) {
	if req == nil {
		return
	}

	var displaced ActiveRequest
	shouldRunRemovalHook := false

	r.mu.Lock()
	if previous, exists := r.removeLocked(req.RequestID); exists {
		// Re-registering the same logical request with a different provider is a
		// provider handoff. Releasing the old slot through the registry keeps
		// monitoring state and concurrency accounting on the same lifecycle edge.
		if previous.ProviderID != req.ProviderID {
			displaced = previous
			shouldRunRemovalHook = true
		}
	}

	r.requests[req.RequestID] = activeRequestEntry{
		request: *req,
		done:    done,
	}

	// Build sticky key using current mode and keep it for later cleanup.
	key := r.buildKey(req)
	r.keyIndex[req.RequestID] = key
	if r.stickyIndex[key] == nil {
		r.stickyIndex[key] = make(map[string]struct{})
	}
	r.stickyIndex[key][req.RequestID] = struct{}{}
	r.mu.Unlock()

	// Hooks run after unlocking so provider-side teardown cannot block unrelated
	// registry readers or deadlock if the hook needs its own synchronization.
	if shouldRunRemovalHook {
		r.runRemovalHook(displaced, ActiveRequestRemovalReasonProviderHandoff)
	}
}

// Unregister is idempotent; safe to call multiple times or with non-existent IDs.
func (r *ActiveRequestRegistry) Unregister(requestID string) {
	r.mu.Lock()
	req, ok := r.removeLocked(requestID)
	r.mu.Unlock()

	if !ok {
		return
	}
	r.runRemovalHook(req, ActiveRequestRemovalReasonExplicit)
}

// RegisterLiveBytes associates transport counters with an active request.
// The tracker is read during List() to populate snapshot fields.
func (r *ActiveRequestRegistry) RegisterLiveBytes(requestID string, tracker *LiveBytesTracker) {
	if tracker == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.liveTraffic[requestID] = tracker
}

// Touch records transport activity for callers without a LiveBytesTracker.
// Hot transport paths use the lock-free tracker and merge it into snapshots.
func (r *ActiveRequestRegistry) Touch(requestID string, at time.Time) {
	if at.IsZero() {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.touchLocked(requestID, at.UnixMilli())
}

func (r *ActiveRequestRegistry) touchLocked(requestID string, at int64) {
	entry, ok := r.requests[requestID]
	if !ok || at <= entry.request.LastActivityAt {
		return
	}

	entry.request.LastActivityAt = at
	r.requests[requestID] = entry
}

// UpdateSSE is called after response headers reveal whether the upstream is streaming.
// Idempotent; safe to call with non-existent IDs.
func (r *ActiveRequestRegistry) UpdateSSE(requestID string, isSSE bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if entry, ok := r.requests[requestID]; ok {
		entry.request.IsSSE = isSSE
		r.requests[requestID] = entry
	}
}

// UpdateModel refreshes the semantic model for an active request after the transport
// has already been registered. Continuity keeps the handshake-time key so later
// protocol observations can enrich logs without rewriting sticky lookup state.
func (r *ActiveRequestRegistry) UpdateModel(requestID, model string) {
	if model == "" {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	entry, ok := r.requests[requestID]
	if !ok || entry.request.Model == model {
		return
	}

	entry.request.Model = model
	r.requests[requestID] = entry
}

// List returns a snapshot copy safe to use without synchronization.
// Registered transport counters are merged into each snapshot for HTTP, SSE,
// and WebSocket requests.
func (r *ActiveRequestRegistry) List() []ActiveRequest {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]ActiveRequest, 0, len(r.requests))
	for _, entry := range r.requests {
		req := entry.request
		if tracker, ok := r.liveTraffic[req.RequestID]; ok {
			req.BytesSent = tracker.BytesSent.Load()
			req.BytesReceived = tracker.BytesReceived.Load()
			req.MsgsSent = tracker.MsgsSent.Load()
			req.MsgsReceived = tracker.MsgsReceived.Load()
			if trackerActivityAt := tracker.LastActivityAt.Load(); trackerActivityAt > req.LastActivityAt {
				req.LastActivityAt = trackerActivityAt
			}
		}
		result = append(result, req)
	}
	return result
}

type cleanupCandidate struct {
	request ActiveRequest
	reason  ActiveRequestRemovalReason
}

func requestDone(done <-chan struct{}) bool {
	if done == nil {
		return false
	}
	select {
	case <-done:
		return true
	default:
		return false
	}
}

func (r *ActiveRequestRegistry) cleanupReasonLocked(requestID string, entry activeRequestEntry, cutoff time.Time) (ActiveRequestRemovalReason, bool) {
	if requestDone(entry.done) {
		return ActiveRequestRemovalReasonOrphaned, true
	}
	if entry.done == nil && r.observedAtLocked(requestID, entry.request).Before(cutoff) {
		return ActiveRequestRemovalReasonStale, true
	}
	return "", false
}

// CleanupStale removes orphaned requests and returns the count of removed entries.
// When a request was registered without a lifecycle signal, maxAge remains as a
// defensive fallback for legacy callers and tests.
func (r *ActiveRequestRegistry) CleanupStale(maxAge time.Duration) int {
	r.mu.Lock()

	cutoff := r.clock.Now().Add(-maxAge)
	removed := make([]cleanupCandidate, 0)
	for id, entry := range r.requests {
		reason, shouldRemove := r.cleanupReasonLocked(id, entry, cutoff)
		if !shouldRemove {
			continue
		}

		if removedReq, ok := r.removeLocked(id); ok {
			removed = append(removed, cleanupCandidate{
				request: removedReq,
				reason:  reason,
			})
		}
	}
	r.mu.Unlock()

	for _, candidate := range removed {
		r.runRemovalHook(candidate.request, candidate.reason)
	}
	return len(removed)
}

// FindActiveProvider finds an active provider for the given sticky key.
// Only returns providers from requests that have received data (HasReceivedData=true).
// This prevents new requests from inheriting connections that are still waiting for upstream response.
// Returns (providerID, found).
func (r *ActiveRequestRegistry) FindActiveProvider(clientIP, userID, apiType, reqModel string) (providerID string, found bool) {
	req := &model.SelectRequest{
		ClientIP: clientIP,
		User:     userID,
		APIType:  apiType,
		Model:    reqModel,
	}
	if continuityModelKnown(reqModel) {
		req.StickyMode = model.StickyModeModel
	} else {
		req.StickyMode = model.StickyModeAPIType
	}
	return r.FindActiveProviderForRequest(req)
}

// FindActiveProviderForRequest finds an active provider using the same request
// dimensions that originally selected the provider.
func (r *ActiveRequestRegistry) FindActiveProviderForRequest(req *model.SelectRequest) (providerID string, found bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	key := r.buildKeyFromRequest(req)
	requestIDs, ok := r.stickyIndex[key]
	if !ok {
		return "", false
	}

	for reqID := range requestIDs {
		if entry, ok := r.requests[reqID]; ok && entry.request.HasReceivedData {
			return entry.request.ProviderID, true
		}
	}
	return "", false
}

func continuityModelKnown(modelName string) bool {
	trimmed := strings.TrimSpace(modelName)
	return trimmed != "" && !strings.EqualFold(trimmed, ModelUnknown)
}

// MarkDataReceived marks a request as having received data from upstream.
// This should be called when the first byte of response data is written to the client.
func (r *ActiveRequestRegistry) MarkDataReceived(requestID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if entry, ok := r.requests[requestID]; ok {
		entry.request.HasReceivedData = true
		if entry.request.LastActivityAt == 0 {
			entry.request.LastActivityAt = r.clock.Now().UnixMilli()
		}
		r.requests[requestID] = entry
	}
}

func (r *ActiveRequestRegistry) observedAtLocked(requestID string, req ActiveRequest) time.Time {
	observedAt := req.StartedAt
	if req.LastActivityAt > 0 {
		lastActivity := time.UnixMilli(req.LastActivityAt)
		if lastActivity.After(observedAt) {
			observedAt = lastActivity
		}
	}
	if tracker, ok := r.liveTraffic[requestID]; ok {
		lastActivityAt := tracker.LastActivityAt.Load()
		if lastActivityAt > 0 {
			lastActivity := time.UnixMilli(lastActivityAt)
			if lastActivity.After(observedAt) {
				observedAt = lastActivity
			}
		}
	}
	return observedAt
}

// Default cleanup configuration constants.
const (
	// defaultCleanupInterval is how often to check for stale requests.
	defaultCleanupInterval = 1 * time.Minute
	// defaultStaleMaxAge is only used for registrations that lack a lifecycle
	// signal, keeping a compatibility fallback without reclaiming quiet live work.
	defaultStaleMaxAge = 10 * time.Minute
)

// StartCleanup spawns a background goroutine that periodically removes orphaned
// requests. Call StopCleanup to stop the goroutine.
func (r *ActiveRequestRegistry) StartCleanup() {
	r.mu.Lock()
	if r.stopCh != nil {
		r.mu.Unlock()
		return // Already running
	}
	r.stopCh = make(chan struct{})
	stopCh := r.stopCh // Capture channel before unlocking to avoid race with StopCleanup
	r.cleanupWg.Add(1)
	r.mu.Unlock()

	go func() {
		defer r.cleanupWg.Done()
		ticker := r.clock.NewTicker(defaultCleanupInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				r.CleanupStale(defaultStaleMaxAge)
			case <-stopCh:
				return
			}
		}
	}()
}

// StopCleanup stops the background cleanup goroutine and waits for it to exit.
// Safe to call multiple times or if cleanup was never started.
func (r *ActiveRequestRegistry) StopCleanup() {
	r.mu.Lock()
	ch := r.stopCh
	if ch != nil {
		close(ch)
		r.stopCh = nil
	}
	r.mu.Unlock()

	// Wait outside the lock to avoid blocking other operations
	if ch != nil {
		r.cleanupWg.Wait()
	}
}
