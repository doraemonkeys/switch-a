package providercookie

import (
	"context"
	"net/url"
	"sync"

	"github.com/doraemonkeys/switch-a/internal/codex/identity"
)

type Request struct {
	service     *Service
	operationID OperationID
	jarID       JarID

	mu       sync.Mutex
	overlays map[CookieScope]*Overlay
	closed   bool
}

func (s *Service) BeginRequest(operationID OperationID, access JarAccess) (*Request, error) {
	if _, err := NewOperationID(string(operationID)); err != nil {
		return nil, err
	}
	if access.jarID == (JarID{}) {
		return nil, &ConfigurationError{Field: "jar_access", Reason: "must be initialized"}
	}
	return &Request{
		service:     s,
		operationID: operationID,
		jarID:       access.jarID,
		overlays:    make(map[CookieScope]*Overlay),
	}, nil
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
	snapshot, err := r.service.repository.Load(ctx, scope, now)
	if err != nil {
		return "", r.service.persistenceFailure(r.operationID, "load_cookies", PersistenceUnavailable, err)
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
	if err := r.service.repository.Touch(ctx, scope, keys, now); err != nil {
		return "", r.service.persistenceFailure(r.operationID, "touch_cookies", PersistenceUnavailable, err)
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
	result, err := r.service.repository.Merge(ctx, scope, changes, canonicalTime(r.service.clock.Now()), r.service.policy)
	if err != nil {
		return MergeResult{}, r.service.persistenceFailure(r.operationID, "merge_overlay", PersistenceUnavailable, err)
	}
	for candidateScope, candidate := range r.overlays {
		_ = candidate.Discard(candidateScope)
	}
	r.closed = true
	r.trace("overlay_merged", "committed", "final_boundary", result.Upserted+result.Deleted, 0, result.Evicted)
	return result, nil
}

func (r *Request) Discard(authority codexidentity.CookieAuthority) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
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
	if r.closed {
		return
	}
	for scope, overlay := range r.overlays {
		_ = overlay.Discard(scope)
	}
	r.overlays = nil
	r.closed = true
	r.trace("overlay_discarded", "discarded", "request_ended_without_boundary", 0, 0, 0)
}

func (r *Request) overlay(authority codexidentity.CookieAuthority) (*Overlay, CookieScope, error) {
	if r == nil || r.service == nil {
		return nil, CookieScope{}, &StateError{Reason: "request cookie state is uninitialized"}
	}
	if r.closed {
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
	r.service.trace.RecordProviderCookieTrace(TraceEvent{
		OperationID: r.operationID,
		Milestone:   milestone,
		Decision:    decision,
		Reason:      reason,
		Count:       count,
		Rejected:    rejected,
		Evicted:     evicted,
	})
}
