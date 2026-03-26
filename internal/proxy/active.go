package proxy

import (
	"sync"
	"sync/atomic"
	"time"

	"switch-a/internal"
)

// stickyKey is used to index active requests for O(1) lookup by sticky session key.
type stickyKey struct {
	ClientIP string
	UserID   string
	APIType  string
	Model    string
}

// ActiveRequest represents a request currently being processed by the proxy.
// It captures metadata needed for live monitoring without storing the full request/response.
type ActiveRequest struct {
	RequestID       string    `json:"request_id"`                 // UUID identifying this request
	ProviderID      string    `json:"provider_id"`                // Selected provider handling the request
	Model           string    `json:"model"`                      // Model being called (e.g., "claude-3-opus")
	APIType         string    `json:"api_type"`                   // API type (claude, codex, gemini, custom:*)
	UserID          string    `json:"user_id"`                    // User identifier from header
	ClientIP        string    `json:"client_ip"`                  // Client IP address
	IsSSE           bool      `json:"is_sse"`                     // Whether this is an SSE streaming request
	IsWebSocket     bool      `json:"is_websocket"`               // Whether this is a WebSocket connection
	StartedAt       time.Time `json:"started_at"`                 // When the request started
	HasReceivedData bool      `json:"has_data"`                   // Whether data has been received from upstream
	BytesSent       int64     `json:"bytes_sent,omitempty"`       // WS: cumulative bytes client → upstream
	BytesReceived   int64     `json:"bytes_received,omitempty"`   // WS: cumulative bytes upstream → client
	MsgsSent        int64     `json:"msgs_sent,omitempty"`        // WS: WebSocket frames client → upstream
	MsgsReceived    int64     `json:"msgs_received,omitempty"`    // WS: WebSocket frames upstream → client
	LastActivityAt  int64     `json:"last_activity_at,omitempty"` // Unix ms of most recent transport activity, 0 = no activity yet
}

// LiveBytesTracker provides lock-free counters for tracking WebSocket data flow
// during a connection's lifetime. The observer writes atomically; the registry
// reads atomically during List() to populate ActiveRequest snapshot fields.
type LiveBytesTracker struct {
	BytesSent      atomic.Int64 // client → upstream
	BytesReceived  atomic.Int64 // upstream → client
	MsgsSent       atomic.Int64
	MsgsReceived   atomic.Int64
	LastActivityAt atomic.Int64 // UnixMilli of most recent message
}

// ActiveRequestRegistry tracks requests currently being processed.
//
// Design note: This is a single-instance, in-memory implementation.
// It does not support distributed deployments with multiple proxy instances.
// For multi-instance scenarios, consider using Redis or a similar distributed store.
type ActiveRequestRegistry struct {
	mu          sync.RWMutex
	requests    map[string]ActiveRequest
	stickyIndex map[stickyKey]map[string]struct{} // stickyKey -> requestIDs for O(1) lookup
	keyIndex    map[string]stickyKey              // requestID -> sticky key used during registration
	wsBytes     map[string]*LiveBytesTracker      // requestID -> live WS counters (populated only for WS)
	perModel    atomic.Bool
	stopCh      chan struct{}  // Channel to signal cleanup goroutine to stop
	cleanupWg   sync.WaitGroup // WaitGroup to confirm cleanup goroutine has exited
	clock       internal.Clock // Injected clock for testability
}

// NewActiveRequestRegistry creates a new registry for tracking active requests.
func NewActiveRequestRegistry() *ActiveRequestRegistry {
	r := &ActiveRequestRegistry{
		requests:    make(map[string]ActiveRequest),
		stickyIndex: make(map[stickyKey]map[string]struct{}),
		keyIndex:    make(map[string]stickyKey),
		wsBytes:     make(map[string]*LiveBytesTracker),
		clock:       internal.RealClock{},
	}
	r.perModel.Store(true)
	return r
}

// NewActiveRequestRegistryWithClock creates a new registry with a custom clock for testing.
func NewActiveRequestRegistryWithClock(clock internal.Clock) *ActiveRequestRegistry {
	r := &ActiveRequestRegistry{
		requests:    make(map[string]ActiveRequest),
		stickyIndex: make(map[stickyKey]map[string]struct{}),
		keyIndex:    make(map[string]stickyKey),
		wsBytes:     make(map[string]*LiveBytesTracker),
		clock:       clock,
	}
	r.perModel.Store(true)
	return r
}

// SetStickyPerModel updates whether sticky matching includes the model dimension.
func (r *ActiveRequestRegistry) SetStickyPerModel(enabled bool) {
	r.perModel.Store(enabled)
}

func (r *ActiveRequestRegistry) buildKeyFromParams(clientIP, userID, apiType, reqModel string) stickyKey {
	key := stickyKey{ClientIP: clientIP, UserID: userID, APIType: apiType}
	if r.perModel.Load() {
		key.Model = reqModel
	}
	return key
}

func (r *ActiveRequestRegistry) buildKey(req *ActiveRequest) stickyKey {
	return r.buildKeyFromParams(req.ClientIP, req.UserID, req.APIType, req.Model)
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

// Register overwrites any existing entry with the same ID to handle retry scenarios
// where the same request ID may be re-registered with updated provider information.
func (r *ActiveRequestRegistry) Register(req *ActiveRequest) {
	if req == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	// If request already exists, remove old sticky index and stale WS tracker
	if _, exists := r.requests[req.RequestID]; exists {
		r.removeFromStickyIndex(req.RequestID)
		delete(r.wsBytes, req.RequestID)
	}

	r.requests[req.RequestID] = *req

	// Build sticky key using current mode and keep it for later cleanup.
	key := r.buildKey(req)
	r.keyIndex[req.RequestID] = key
	if r.stickyIndex[key] == nil {
		r.stickyIndex[key] = make(map[string]struct{})
	}
	r.stickyIndex[key][req.RequestID] = struct{}{}
}

// Unregister is idempotent; safe to call multiple times or with non-existent IDs.
func (r *ActiveRequestRegistry) Unregister(requestID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.requests[requestID]; !ok {
		return
	}

	r.removeFromStickyIndex(requestID)
	delete(r.requests, requestID)
	delete(r.wsBytes, requestID)
}

// RegisterLiveBytes associates a LiveBytesTracker with an active request.
// The tracker is read during List() to populate snapshot fields.
func (r *ActiveRequestRegistry) RegisterLiveBytes(requestID string, tracker *LiveBytesTracker) {
	if tracker == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.wsBytes[requestID] = tracker
}

// Touch records transport activity for non-WebSocket requests.
// WebSocket activity stays lock-free via LiveBytesTracker and is merged when
// producing snapshots or evaluating staleness.
func (r *ActiveRequestRegistry) Touch(requestID string, at time.Time) {
	if at.IsZero() {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.touchLocked(requestID, at.UnixMilli())
}

func (r *ActiveRequestRegistry) touchLocked(requestID string, at int64) {
	req, ok := r.requests[requestID]
	if !ok || at <= req.LastActivityAt {
		return
	}

	req.LastActivityAt = at
	r.requests[requestID] = req
}

// UpdateSSE is called after response headers reveal whether the upstream is streaming.
// Idempotent; safe to call with non-existent IDs.
func (r *ActiveRequestRegistry) UpdateSSE(requestID string, isSSE bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if req, ok := r.requests[requestID]; ok {
		req.IsSSE = isSSE
		r.requests[requestID] = req
	}
}

// UpdateModel refreshes the semantic model for an active request after the transport
// has already been registered. WebSocket protocols often reveal the billed model only
// after the upgrade succeeds, so live monitoring and sticky lookup must be re-indexed.
func (r *ActiveRequestRegistry) UpdateModel(requestID, model string) {
	if model == "" {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	req, ok := r.requests[requestID]
	if !ok || req.Model == model {
		return
	}

	r.removeFromStickyIndex(requestID)
	req.Model = model
	r.requests[requestID] = req

	key := r.buildKey(&req)
	r.keyIndex[requestID] = key
	if r.stickyIndex[key] == nil {
		r.stickyIndex[key] = make(map[string]struct{})
	}
	r.stickyIndex[key][requestID] = struct{}{}
}

// List returns a snapshot copy safe to use without synchronization.
// For WebSocket requests with a registered LiveBytesTracker, the snapshot
// includes current byte/message counts and last-activity timestamp.
func (r *ActiveRequestRegistry) List() []ActiveRequest {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]ActiveRequest, 0, len(r.requests))
	for _, req := range r.requests {
		if tracker, ok := r.wsBytes[req.RequestID]; ok {
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

// CleanupStale removes requests older than maxAge and returns the count of removed requests.
// This is a safety mechanism to prevent memory leaks from requests that were never unregistered
// (e.g., due to panics or bugs). Under normal operation, requests should be unregistered
// explicitly when they complete.
func (r *ActiveRequestRegistry) CleanupStale(maxAge time.Duration) int {
	r.mu.Lock()
	defer r.mu.Unlock()

	cutoff := r.clock.Now().Add(-maxAge)
	removed := 0
	for id, req := range r.requests {
		if !r.observedAtLocked(id, req).Before(cutoff) {
			continue
		}

		r.removeFromStickyIndex(id)
		delete(r.requests, id)
		delete(r.wsBytes, id)
		removed++
	}
	return removed
}

// FindActiveProvider finds an active provider for the given sticky key.
// Only returns providers from requests that have received data (HasReceivedData=true).
// This prevents new requests from inheriting connections that are still waiting for upstream response.
// Returns (providerID, found).
func (r *ActiveRequestRegistry) FindActiveProvider(clientIP, userID, apiType, reqModel string) (providerID string, found bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	key := r.buildKeyFromParams(clientIP, userID, apiType, reqModel)
	requestIDs, ok := r.stickyIndex[key]
	if !ok {
		return "", false
	}

	for reqID := range requestIDs {
		if req, ok := r.requests[reqID]; ok && req.HasReceivedData {
			return req.ProviderID, true
		}
	}
	return "", false
}

// MarkDataReceived marks a request as having received data from upstream.
// This should be called when the first byte of response data is written to the client.
func (r *ActiveRequestRegistry) MarkDataReceived(requestID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if req, ok := r.requests[requestID]; ok {
		req.HasReceivedData = true
		if req.LastActivityAt == 0 {
			req.LastActivityAt = r.clock.Now().UnixMilli()
		}
		r.requests[requestID] = req
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
	if tracker, ok := r.wsBytes[requestID]; ok {
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
	// defaultStaleMaxAge is how old a request must be to be considered stale.
	// This is set conservatively high to avoid removing long-running SSE streams.
	defaultStaleMaxAge = 10 * time.Minute
)

// StartCleanup spawns a background goroutine that periodically removes stale requests.
// This provides defense-in-depth against memory leaks from requests that were not properly
// unregistered (e.g., due to panics). Call StopCleanup to stop the goroutine.
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
