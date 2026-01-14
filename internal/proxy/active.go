package proxy

import (
	"sync"
	"time"

	"switch-a/internal"
)

// ActiveRequest represents a request currently being processed by the proxy.
// It captures metadata needed for live monitoring without storing the full request/response.
type ActiveRequest struct {
	RequestID  string    `json:"request_id"`  // UUID identifying this request
	ProviderID string    `json:"provider_id"` // Selected provider handling the request
	Model      string    `json:"model"`       // Model being called (e.g., "claude-3-opus")
	APIType    string    `json:"api_type"`    // API type (claude, codex, gemini, custom:*)
	UserID     string    `json:"user_id"`     // User identifier from header
	ClientIP   string    `json:"client_ip"`   // Client IP address
	IsSSE      bool      `json:"is_sse"`      // Whether this is an SSE streaming request
	StartedAt  time.Time `json:"started_at"`  // When the request started
}

// ActiveRequestRegistry tracks requests currently being processed.
//
// Design note: This is a single-instance, in-memory implementation.
// It does not support distributed deployments with multiple proxy instances.
// For multi-instance scenarios, consider using Redis or a similar distributed store.
type ActiveRequestRegistry struct {
	mu       sync.RWMutex
	requests map[string]ActiveRequest
	stopCh   chan struct{}  // Channel to signal cleanup goroutine to stop
	clock    internal.Clock // Injected clock for testability
}

// NewActiveRequestRegistry creates a new registry for tracking active requests.
func NewActiveRequestRegistry() *ActiveRequestRegistry {
	return &ActiveRequestRegistry{
		requests: make(map[string]ActiveRequest),
		clock:    internal.RealClock{},
	}
}

// NewActiveRequestRegistryWithClock creates a new registry with a custom clock for testing.
func NewActiveRequestRegistryWithClock(clock internal.Clock) *ActiveRequestRegistry {
	return &ActiveRequestRegistry{
		requests: make(map[string]ActiveRequest),
		clock:    clock,
	}
}

// Register adds a request to the registry.
// If a request with the same ID already exists, it will be overwritten.
func (r *ActiveRequestRegistry) Register(req *ActiveRequest) {
	if req == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests[req.RequestID] = *req
}

// Unregister removes a request from the registry.
// If the request ID does not exist, this is a no-op.
func (r *ActiveRequestRegistry) Unregister(requestID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.requests, requestID)
}

// UpdateSSE updates the IsSSE field for an active request.
// This is called after the response type is determined from the upstream.
// If the request ID does not exist, this is a no-op.
func (r *ActiveRequestRegistry) UpdateSSE(requestID string, isSSE bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if req, ok := r.requests[requestID]; ok {
		req.IsSSE = isSSE
		r.requests[requestID] = req
	}
}

// List returns a snapshot copy of all active requests.
// The returned slice is safe to use without synchronization.
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
			delete(r.requests, id)
			removed++
		}
	}
	return removed
}

// Default cleanup configuration constants.
const (
	// defaultCleanupInterval is how often to check for stale requests.
	defaultCleanupInterval = 1 * time.Minute
	// defaultStaleMaxAge is how old a request must be to be considered stale.
	// This is set conservatively high to avoid removing long-running SSE streams.
	defaultStaleMaxAge = 30 * time.Minute
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
	r.mu.Unlock()

	go func() {
		ticker := time.NewTicker(defaultCleanupInterval)
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

// StopCleanup stops the background cleanup goroutine.
// Safe to call multiple times or if cleanup was never started.
func (r *ActiveRequestRegistry) StopCleanup() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopCh != nil {
		close(r.stopCh)
		r.stopCh = nil
	}
}
