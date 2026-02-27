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
	RequestID       string    `json:"request_id"`   // UUID identifying this request
	ProviderID      string    `json:"provider_id"`  // Selected provider handling the request
	Model           string    `json:"model"`        // Model being called (e.g., "claude-3-opus")
	APIType         string    `json:"api_type"`     // API type (claude, codex, gemini, custom:*)
	UserID          string    `json:"user_id"`      // User identifier from header
	ClientIP        string    `json:"client_ip"`    // Client IP address
	IsSSE           bool      `json:"is_sse"`       // Whether this is an SSE streaming request
	IsWebSocket     bool      `json:"is_websocket"` // Whether this is a WebSocket connection
	StartedAt       time.Time `json:"started_at"`   // When the request started
	HasReceivedData bool      `json:"has_data"`     // Whether data has been received from upstream
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

	// If request already exists, remove old sticky index entry first
	if _, exists := r.requests[req.RequestID]; exists {
		r.removeFromStickyIndex(req.RequestID)
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

// List returns a snapshot copy safe to use without synchronization.
func (r *ActiveRequestRegistry) List() []ActiveRequest {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]ActiveRequest, 0, len(r.requests))
	for _, req := range r.requests {
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
		if !req.StartedAt.Before(cutoff) {
			continue
		}

		r.removeFromStickyIndex(id)
		delete(r.requests, id)
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
		r.requests[requestID] = req
	}
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
