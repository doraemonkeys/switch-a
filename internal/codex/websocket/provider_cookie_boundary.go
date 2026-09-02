package codexws

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/doraemonkeys/switch-a/internal/codex/cookie"
	"github.com/doraemonkeys/switch-a/internal/codex/identity"
)

// providerCookieBoundary keeps the speculative overlay and its publication
// state together because a failed physical attempt must not affect the jar that
// a later replacement observes.
type providerCookieBoundary struct {
	request       *providercookie.Request
	lastAuthority *codexidentity.CookieAuthority
	gatewayHeader string
	closed        bool
}

func (r *Runtime) beginProviderCookies(ctx context.Context, op *Operation, request *http.Request) error {
	if r.providerCookies == nil || r.externalScheme == nil {
		return &Failure{Class: FailureStorage, Stage: "provider_cookie", Cause: errors.New("provider cookie capability is unavailable")}
	}
	scheme, err := r.externalScheme.ResolveExternalScheme(request)
	if err != nil {
		return cookieFailure("external_scheme", err)
	}
	cookieOperationID, err := providercookie.NewOperationID(op.operationID)
	if err != nil {
		return cookieFailure("provider_cookie", err)
	}
	access, err := r.providerCookies.ResolveJar(ctx, cookieOperationID, gatewayHandle(request), op.clientScopes)
	if err != nil {
		return cookieFailure("resolve_cookie_jar", err)
	}
	op.cookieBoundary.request, err = r.providerCookies.BeginRequest(cookieOperationID, access)
	if err != nil {
		return cookieFailure("begin_cookie_request", err)
	}
	if !access.Issued() && !access.Refresh() {
		return nil
	}
	handle, err := providercookie.NewGatewayHandleCookie(access.HandleValue(), scheme)
	if err != nil {
		return cookieFailure("gateway_cookie", err)
	}
	op.cookieBoundary.gatewayHeader, err = handle.HeaderValue()
	if err != nil {
		return cookieFailure("gateway_cookie", err)
	}
	return nil
}

func (o *Operation) GatewaySetCookie() string {
	if o == nil {
		return ""
	}
	return o.cookieBoundary.gatewayHeader
}

func (o *Operation) selectCookiesForDial(
	ctx context.Context,
	headers http.Header,
	authority codexidentity.CookieAuthority,
	finalURL *url.URL,
) error {
	deleteHeaderFold(headers, "Cookie")
	o.mu.Lock()
	previous := o.cookieBoundary.lastAuthority
	o.mu.Unlock()
	if previous != nil {
		// Every physical dial owns a distinct speculative overlay. Discarding the
		// prior authority even when a retry stays on that authority prevents a
		// rejected handshake from contributing cookies to the selected attempt.
		if err := o.cookieBoundary.request.Discard(*previous); err != nil {
			return cookieFailure("discard_replaced_cookies", err)
		}
	}
	cookieValue, err := o.cookieBoundary.request.Select(ctx, authority, cloneURL(finalURL))
	if err != nil {
		return cookieFailure("select_cookies", err)
	}
	if cookieValue != "" {
		headers.Set("Cookie", cookieValue)
	}
	o.mu.Lock()
	o.cookieBoundary.lastAuthority = &authority
	o.mu.Unlock()
	return nil
}

func (o *Operation) ApplyHandshake(finalURL *url.URL, headers http.Header) error {
	if o == nil {
		return nil
	}
	o.mu.Lock()
	authority := o.cookieBoundary.lastAuthority
	o.mu.Unlock()
	if authority == nil {
		return &Failure{Class: FailureStorage, Stage: "handshake_cookies", Cause: errors.New("attempt cookie authority is unavailable")}
	}
	_, err := o.cookieBoundary.request.ApplyResponse(*authority, cloneURL(finalURL), headerValues(headers, "Set-Cookie"))
	if err != nil {
		return cookieFailure("handshake_cookies", err)
	}
	return nil
}

func (o *Operation) CommitCookies(ctx context.Context) error {
	if o == nil {
		return nil
	}
	o.mu.Lock()
	if o.cookieBoundary.closed {
		o.mu.Unlock()
		return nil
	}
	authority := o.cookieBoundary.lastAuthority
	o.mu.Unlock()
	if authority == nil {
		return &Failure{Class: FailureStorage, Stage: "commit_cookies", Cause: errors.New("final cookie authority is unavailable")}
	}
	if _, err := o.cookieBoundary.request.Commit(ctx, *authority); err != nil {
		return cookieFailure("commit_cookies", err)
	}
	o.mu.Lock()
	o.cookieBoundary.closed = true
	o.mu.Unlock()
	return nil
}

func (o *Operation) DiscardCookies() {
	if o == nil || o.cookieBoundary.request == nil {
		return
	}
	o.mu.Lock()
	if o.cookieBoundary.closed {
		o.mu.Unlock()
		return
	}
	o.cookieBoundary.closed = true
	o.mu.Unlock()
	o.cookieBoundary.request.DiscardAll()
}

func gatewayHandle(request *http.Request) string {
	values := make([]string, 0, 1)
	for _, cookie := range request.Cookies() {
		if cookie.Name == providercookie.GatewayHandleName {
			values = append(values, cookie.Value)
		}
	}
	if len(values) == 1 {
		return values[0]
	}
	if len(values) > 1 {
		return "invalid-multiple-handle"
	}
	return ""
}

func stripGatewayHandleCookie(headers http.Header) {
	values := headerValues(headers, "Cookie")
	deleteHeaderFold(headers, "Cookie")
	for _, value := range values {
		kept := make([]string, 0)
		for pair := range strings.SplitSeq(value, ";") {
			pair = strings.TrimSpace(pair)
			name, _, hasValue := strings.Cut(pair, "=")
			if hasValue && name == providercookie.GatewayHandleName {
				continue
			}
			if pair != "" {
				kept = append(kept, pair)
			}
		}
		if len(kept) > 0 {
			headers.Add("Cookie", strings.Join(kept, "; "))
		}
	}
}

func cloneURL(source *url.URL) *url.URL {
	if source == nil {
		return nil
	}
	copyURL := *source
	return &copyURL
}
