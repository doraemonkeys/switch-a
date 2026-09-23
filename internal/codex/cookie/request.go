package providercookie

import (
	"context"
	"errors"
	"net/url"
	"sync"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/identity"
)

type requestState uint8

const (
	requestOpen requestState = iota
	requestCommitted
	requestDiscarded
)

type Request struct {
	service       *Service
	operationID   OperationID
	jarID         JarID
	clientScope   codexidentity.ClientScope
	persisted     bool
	handleValue   string
	publishHandle bool
	release       func()

	reserved *BindingRecord
	mu       sync.Mutex
	overlays map[CookieScope]*Overlay
	state    requestState
}

func (r *Request) ApplyResponse(
	authority codexidentity.CookieAuthority,
	responseURL *url.URL,
	setCookieLines []string,
) ([]RejectedCookie, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	overlay, scope, err := r.overlay(authority)
	if err != nil {
		return nil, err
	}
	result, err := r.service.parser.ParseResponse(responseURL, setCookieLines, canonicalTime(r.service.clock.Now()))
	if err != nil {
		r.trace("response_cookies_parsed", "failed_closed", "boundary_limit", 0, 0, 0)
		return nil, err
	}
	if err := overlay.ApplyBatch(scope, result.Mutations); err != nil {
		r.trace("response_cookies_parsed", "failed_closed", "overlay_limit", 0, len(result.Rejected), 0)
		return nil, err
	}
	r.trace("response_cookies_parsed", "overlay_updated", "", len(result.Mutations), len(result.Rejected), 0)
	return append([]RejectedCookie(nil), result.Rejected...), nil
}

func (r *Request) Select(
	ctx context.Context,
	authority codexidentity.CookieAuthority,
	requestURL *url.URL,
) (string, error) {
	if ctx == nil {
		return "", &ConfigurationError{Field: "context", Reason: "must be provided"}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	overlay, scope, err := r.overlay(authority)
	if err != nil {
		return "", err
	}
	now := canonicalTime(r.service.clock.Now())
	snapshot, _ := NewSnapshot(scope, nil)
	if r.persisted {
		snapshot, err = r.service.repository.Load(ctx, scope, now)
		if err != nil {
			return "", r.service.persistenceFailure(r.operationID, "load_cookies", PersistenceUnavailable, err)
		}
	}
	selected, err := Select(snapshot, overlay, requestURL, now, r.service.hosts)
	if err != nil {
		return "", err
	}
	header, err := Render(selected, r.service.policy)
	if err != nil {
		return "", err
	}
	keys := make([]CookieKey, 0, len(selected))
	for _, cookie := range selected {
		keys = append(keys, cookie.Key())
	}
	if r.persisted {
		if err := r.service.repository.Touch(ctx, scope, keys, now); err != nil {
			return "", r.service.persistenceFailure(r.operationID, "touch_cookies", PersistenceUnavailable, err)
		}
	}
	r.trace("request_cookies_selected", "selected", "", len(selected), 0, 0)
	return header, nil
}

func (r *Request) Commit(ctx context.Context, authority codexidentity.CookieAuthority) (MergeResult, error) {
	if ctx == nil {
		return MergeResult{}, &ConfigurationError{Field: "context", Reason: "must be provided"}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	overlay, scope, err := r.overlay(authority)
	if err != nil {
		return MergeResult{}, err
	}
	changes, err := overlay.Changes(scope)
	if err != nil {
		return MergeResult{}, err
	}
	now := canonicalTime(r.service.clock.Now())
	var result MergeResult
	if r.persisted {
		result, err = r.service.repository.Merge(ctx, scope, changes, now, r.service.policy)
	} else if hasLiveCookies(changes, now) {
		result, err = r.createJar(ctx, authority, changes, now)
	}
	if err != nil {
		return MergeResult{}, r.service.persistenceFailure(r.operationID, "merge_overlay", PersistenceUnavailable, err)
	}
	for candidateScope, candidate := range r.overlays {
		_ = candidate.Discard(candidateScope)
	}
	r.state = requestCommitted
	r.trace("overlay_merged", "committed", "final_boundary", result.Upserted+result.Deleted, 0, result.Evicted)
	if result.ReclaimedBindings > 0 {
		r.service.trace.RecordProviderCookieTrace(TraceEvent{OperationID: r.operationID, Milestone: "capacity_reclaimed", Decision: "committed", ReclaimedBindings: result.ReclaimedBindings})
	}
	return result, nil
}

func (r *Request) Discard(authority codexidentity.CookieAuthority) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state != requestOpen {
		return &StateError{Reason: "request cookie state is closed", Cause: ErrOverlayDiscarded}
	}
	scope, err := NewCookieScope(r.jarID, authority)
	if err != nil {
		return err
	}
	if overlay, exists := r.overlays[scope]; exists {
		if err := overlay.Discard(scope); err != nil {
			return err
		}
		delete(r.overlays, scope)
	}
	r.trace("overlay_discarded", "discarded", "scope_switch_or_replacement", 0, 0, 0)
	return nil
}

func (r *Request) DiscardAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.release != nil {
		r.release()
		r.release = nil
	}
	if r.state != requestOpen {
		return
	}
	for scope, overlay := range r.overlays {
		_ = overlay.Discard(scope)
	}
	r.overlays = nil
	r.state = requestDiscarded
	r.trace("overlay_discarded", "discarded", "request_ended_without_boundary", 0, 0, 0)
}

func (r *Request) overlay(authority codexidentity.CookieAuthority) (*Overlay, CookieScope, error) {
	if r == nil || r.service == nil {
		return nil, CookieScope{}, &StateError{Reason: "request cookie state is uninitialized"}
	}
	if r.state != requestOpen {
		return nil, CookieScope{}, &StateError{Reason: "request cookie state is closed", Cause: ErrOverlayDiscarded}
	}
	scope, err := NewCookieScope(r.jarID, authority)
	if err != nil {
		return nil, CookieScope{}, err
	}
	if overlay, exists := r.overlays[scope]; exists {
		return overlay, scope, nil
	}
	overlay, err := NewOverlay(scope, r.service.policy)
	if err != nil {
		return nil, CookieScope{}, err
	}
	r.overlays[scope] = overlay
	return overlay, scope, nil
}

func (r *Request) trace(milestone, decision, reason string, count, rejected, evicted int) {
	lifecycle := "transient"
	if r.persisted {
		lifecycle = "persistent"
	}
	r.service.trace.RecordProviderCookieTrace(TraceEvent{
		Lifecycle:   lifecycle,
		OperationID: r.operationID,
		Milestone:   milestone,
		Decision:    decision,
		Reason:      reason,
		Count:       count,
		Rejected:    rejected,
		Evicted:     evicted,
	})
}

// GatewaySetCookie publishes HTTP handles only after the durable boundary.
// WebSocket headers close earlier and use ReserveUpgradeCookie instead.
func (r *Request) GatewaySetCookie(scheme ResolvedExternalScheme) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state != requestCommitted || !r.persisted || !r.publishHandle {
		return "", nil
	}
	handle, err := NewGatewayHandleCookie(r.handleValue, scheme)
	if err != nil {
		return "", err
	}
	return handle.HeaderValue()
}

func hasLiveCookies(changes []Mutation, at time.Time) bool {
	for _, change := range changes {
		if cookie, ok := change.Cookie(); ok && !cookie.Expired(at) {
			return true
		}
	}
	return false
}

func (r *Request) createJar(ctx context.Context, authority codexidentity.CookieAuthority, changes []Mutation, at time.Time) (MergeResult, error) {
	candidateJar := r.jarID
	for range bindingGenerationAttempts {
		var record BindingRecord
		handle := r.handleValue
		var err error
		if r.reserved != nil {
			record = *r.reserved
			record.CreatedAt, record.LastAccessAt = at, at
			record.IdleExpiresAt = addDurationClamped(at, r.service.policy.HandleIdleTTL)
			record.AbsoluteExpiresAt = addDurationClamped(at, r.service.policy.HandleAbsoluteTTL)
		} else {
			record, handle, err = r.service.newBinding(r.operationID, candidateJar, r.clientScope, at)
			if err != nil {
				return MergeResult{}, err
			}
		}
		created, err := r.service.repository.CreateJar(ctx, record, authority, changes, r.service.policy)
		if errors.Is(err, ErrIdentifierClash) && r.reserved == nil {
			candidateJar, err = r.service.generateJar(r.operationID)
			if err != nil {
				return MergeResult{}, err
			}
			continue
		}
		if err != nil {
			return MergeResult{}, err
		}
		r.jarID = record.JarID
		r.persisted = true
		r.handleValue = handle
		r.publishHandle = true
		r.release = created.Release
		return created.Merge, nil
	}
	return MergeResult{}, ErrIdentifierClash
}

// ReserveUpgradeCookie must precede the downstream 101, the last opportunity
// to send HTTP headers. The token has no database row until the selected WS
// attempt commits live cookies; abandoned attempts leave only request memory.
func (r *Request) ReserveUpgradeCookie(scheme ResolvedExternalScheme) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state != requestOpen {
		return "", &StateError{Reason: "cannot reserve a handle after the cookie boundary", Cause: ErrOverlayDiscarded}
	}
	if r.persisted && !r.publishHandle {
		return "", nil
	}
	if !r.persisted && r.reserved == nil {
		record, handle, err := r.service.newBinding(r.operationID, r.jarID, r.clientScope, canonicalTime(r.service.clock.Now()))
		if err != nil {
			return "", err
		}
		r.reserved = &record
		r.handleValue = handle
		r.trace("handle_reserved", "transient", "websocket_upgrade", 0, 0, 0)
	}
	handle, err := NewGatewayHandleCookie(r.handleValue, scheme)
	if err != nil {
		return "", err
	}
	return handle.HeaderValue()
}
