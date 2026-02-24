package proxy

import (
	"sync"
	"time"

	"switch-a/internal"
)

// stickyKey is used to index active requests for O(1) lookup by sticky session key.
type stickyKey struct {
	ClientIP string
	UserID   string
	APIType  string
}

// ActiveRequest represents a request currently being processed by the proxy.
// It captures metadata needed for live monitoring without storing the full request/response.
type ActiveRequest struct {
	RequestID       string    `json:"request_id"`  // UUID identifying this request
	ProviderID      string    `json:"provider_id"` // Selected provider handling the request
	Model           string    `json:"model"`       // Model being called (e.g., "claude-3-opus")
	APIType         string    `json:"api_type"`    // API type (claude, codex, gemini, custom:*)
	UserID          string    `json:"user_id"`     // User identifier from header
	ClientIP        string    `json:"client_ip"`   // Client IP address
	IsSSE           bool      `json:"is_sse"`      // Whether this is an SSE streaming request
	StartedAt       time.Time `json:"started_at"`  // When the request started
	HasReceivedData bool      `json:"has_data"`    // Whether data has been received from upstream
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
	stopCh      chan struct{}                     // Channel to signal cleanup goroutine to stop
	cleanupWg   sync.WaitGroup                    // WaitGroup to confirm cleanup goroutine has exited
	clock       internal.Clock                    // Injected clock for testability
}

// NewActiveRequestRegistry creates a new registry for tracking active requests.
func NewActiveRequestRegistry() *ActiveRequestRegistry {
	return &ActiveRequestRegistry{
		requests:    make(map[string]ActiveRequest),
		stickyIndex: make(map[stickyKey]map[string]struct{}),
		clock:       internal.RealClock{},
	}
}

// NewActiveRequestRegistryWithClock creates a new registry with a custom clock for testing.
func NewActiveRequestRegistryWithClock(clock internal.Clock) *ActiveRequestRegistry {
	return &ActiveRequestRegistry{
		requests:    make(map[string]ActiveRequest),
		stickyIndex: make(map[stickyKey]map[string]struct{}),
		clock:       clock,
	}
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
	if oldReq, exists := r.requests[req.RequestID]; exists {
		oldKey := stickyKey{ClientIP: oldReq.ClientIP, UserID: oldReq.UserID, APIType: oldReq.APIType}
		if ids := r.stickyIndex[oldKey]; ids != nil {
			delete(ids, req.RequestID)
			if len(ids) == 0 {
				delete(r.stickyIndex, oldKey)
			}
		}
	}

	r.requests[req.RequestID] = *req

	// Add to sticky index
	key := stickyKey{ClientIP: req.ClientIP, UserID: req.UserID, APIType: req.APIType}
	if r.stickyIndex[key] == nil {
		r.stickyIndex[key] = make(map[string]struct{})
	}
	r.stickyIndex[key][req.RequestID] = struct{}{}
}

// Unregister is idempotent; safe to call multiple times or with non-existent IDs.
func (r *ActiveRequestRegistry) Unregister(requestID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if req, ok := r.requests[requestID]; ok {
		// Remove from sticky index
		key := stickyKey{ClientIP: req.ClientIP, UserID: req.UserID, APIType: req.APIType}
		if ids := r.stickyIndex[key]; ids != nil {
			delete(ids, requestID)
			if len(ids) == 0 {
				delete(r.stickyIndex, key)
			}
		}
		delete(r.requests, requestID)
	}
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
		if req.StartedAt.Before(cutoff) {
			// Remove from sticky index
			key := stickyKey{ClientIP: req.ClientIP, UserID: req.UserID, APIType: req.APIType}
			if ids := r.stickyIndex[key]; ids != nil {
				delete(ids, id)
				if len(ids) == 0 {
					delete(r.stickyIndex, key)
				}
			}
			delete(r.requests, id)
			removed++
		}
	}
	return removed
}

// FindActiveProvider finds an active provider for the given sticky key.
// Only returns providers from requests that have received data (HasReceivedData=true).
// This prevents new requests from inheriting connections that are still waiting for upstream response.
// Returns (providerID, found).
func (r *ActiveRequestRegistry) FindActiveProvider(clientIP, userID, apiType string) (providerID string, found bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	key := stickyKey{ClientIP: clientIP, UserID: userID, APIType: apiType}
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
