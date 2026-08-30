package codexhttp

import (
	"context"
	"crypto/sha256"
	"errors"
	"net/http"
	"net/url"
	"sync"

	"github.com/doraemonkeys/switch-a/internal/codex/continuity"
	"github.com/doraemonkeys/switch-a/internal/codex/headers"
	"github.com/doraemonkeys/switch-a/internal/codex/identity"
	"github.com/doraemonkeys/switch-a/internal/upstreamtransport"
)

type Attempt struct {
	operation     *Operation
	protocolScope codexidentity.ProtocolScope
	authority     codexidentity.UpstreamAuthority
	routeTargetID string
	responseURL   *url.URL

	requestClaimLeases []codexcontinuity.Lease
	pinProtocolScope   bool
	pinAuthority       bool
	settlement         attemptSettlement
}

const identityContentCoding = "identity"

type attemptSettlement uint8

const (
	attemptUnsettled attemptSettlement = iota
	attemptDisclosureStarted
	attemptDisclosed
	attemptAbandoned
)

func (o *Operation) PrepareAttempt(
	ctx context.Context,
	request *http.Request,
	candidate codexidentity.CandidateSnapshot,
	applied codexidentity.AppliedIdentity,
) (*Attempt, error) {
	if o == nil {
		return nil, dependencyError("attempt", errors.New("operation is required"))
	}
	if request == nil || request.URL == nil {
		return nil, clientError("attempt", errors.New("upstream request is required"))
	}
	attempt := &Attempt{operation: o, responseURL: cloneURL(request.URL)}
	if o.apiType != codexAPIType {
		return attempt, nil
	}
	if err := candidate.ValidateApplied(applied); err != nil {
		return nil, identityError("applied_identity", err)
	}
	attempt.protocolScope = candidate.ProtocolScope()
	attempt.authority = applied.Authority()
	attempt.routeTargetID = candidate.RouteTargetID()

	o.mu.Lock()
	defer o.mu.Unlock()
	if o.requiredAuthority != nil && !o.requiredAuthority.Equal(attempt.authority) {
		return nil, identityError("required_authority", errors.New("selected attempt crosses the required authority"))
	}
	if o.requiredProtocolScope != nil && !o.requiredProtocolScope.Equal(attempt.protocolScope) {
		return nil, identityError("required_protocol_scope", errors.New("selected attempt crosses the required protocol scope"))
	}

	for _, name := range o.clientDecision.HeaderNamesToDrop() {
		request.Header.Del(name)
	}
	// Response protocol ownership requires event boundaries to survive transport.
	// Codex requests can become SSE even when Accept does not advertise it, so the
	// attempt must negotiate identity before the upstream response is selected.
	request.Header.Set("Accept-Encoding", identityContentCoding)
	if err := o.prepareAttemptCookiesLocked(ctx, request, attempt.authority.CookieAuthority()); err != nil {
		return nil, err
	}
	if err := o.prepareRequestContinuityLocked(ctx, attempt); err != nil {
		return nil, err
	}
	for _, decision := range o.clientDecision.Decisions() {
		if decision.Field() == codexheaders.FieldAttestation {
			attempt.pinAuthority = true
			break
		}
	}
	return attempt, nil
}

func (o *Operation) prepareAttemptCookiesLocked(
	ctx context.Context,
	request *http.Request,
	authority codexidentity.CookieAuthority,
) error {
	if o.cookieRequest == nil {
		return dependencyError("provider_cookie", errors.New("request cookie state is unavailable"))
	}
	if o.lastCookieAuthority != nil && !o.lastCookieAuthority.Equal(authority) {
		if err := o.cookieRequest.Discard(*o.lastCookieAuthority); err != nil {
			return dependencyError("provider_cookie_discard", err)
		}
	}
	header, err := o.cookieRequest.Select(ctx, authority, request.URL)
	if err != nil {
		return dependencyError("provider_cookie_select", err)
	}
	request.Header.Del("Cookie")
	if header != "" {
		request.Header.Set("Cookie", header)
	}
	copyAuthority := authority
	o.lastCookieAuthority = &copyAuthority
	return nil
}

func (o *Operation) prepareRequestContinuityLocked(
	ctx context.Context,
	attempt *Attempt,
) error {
	if o.requestClaimsCommitted {
		return nil
	}
	var leases []codexcontinuity.Lease
	pinProtocolScope := false
	for _, decision := range o.clientDecision.Decisions() {
		candidate := decision.Candidate()
		if _, persistent := candidate.PersistentNamespace(); !persistent {
			continue
		}
		pinProtocolScope = pinProtocolScope || decisionPinsProtocolScope(decision)
		lease, prepared, err := o.prepareRequestLease(
			ctx, decision, attempt.protocolScope, attempt.routeTargetID,
		)
		if err != nil {
			o.abandonRequestLeases(ctx, leases)
			return err
		}
		if !prepared {
			continue
		}
		leases = append(leases, lease)
	}
	attempt.requestClaimLeases = leases
	attempt.pinProtocolScope = pinProtocolScope
	attempt.pinAuthority = pinProtocolScope
	return nil
}

func (o *Operation) prepareRequestLease(
	ctx context.Context,
	decision codexheaders.Decision,
	scope codexidentity.ProtocolScope,
	routeTargetID string,
) (codexcontinuity.Lease, bool, error) {
	candidate := decision.Candidate()
	var (
		lease codexcontinuity.Lease
		err   error
		stage = "continuity_claim"
	)
	switch decision.Action() {
	case codexheaders.ActionForward:
		stage = "continuity_validate"
		lease, err = o.runtime.continuity.AcquireExisting(ctx, codexcontinuity.ValidateRequest{
			Evidence: evidence(candidate), ClientScopeCandidates: o.clientScopes,
			ProtocolScope: scope, OperationID: o.operationID,
		})
	case codexheaders.ActionClaim:
		if decision.Claim().Lifetime() != codexheaders.ClaimLifetimeDurable {
			return codexcontinuity.Lease{}, false, nil
		}
		lease, err = o.runtime.continuity.Claim(ctx, o.requestClaim(candidate, scope, routeTargetID))
	case codexheaders.ActionAdopt:
		stage = "continuity_adopt"
		lease, err = o.runtime.continuity.Adopt(ctx, o.requestClaim(candidate, scope, routeTargetID))
	default:
		return codexcontinuity.Lease{}, false, nil
	}
	if err == nil {
		return lease, true, nil
	}
	if continuityPersistenceUnavailable(err) &&
		(decision.Action() == codexheaders.ActionForward || decision.Action() == codexheaders.ActionClaim) {
		// The attempt retains a provisional ProtocolScope even without a durable
		// lease. Disclosure promotes it, while a proven pre-write failure may
		// replace it; existing-state adoption still needs an atomic reservation.
		return codexcontinuity.Lease{}, false, nil
	}
	if continuityPersistenceUnavailable(err) {
		return codexcontinuity.Lease{}, false, dependencyError(stage, err)
	}
	return codexcontinuity.Lease{}, false, clientError(stage, err)
}

func decisionPinsProtocolScope(decision codexheaders.Decision) bool {
	switch decision.Action() {
	case codexheaders.ActionForward, codexheaders.ActionForwardDegraded,
		codexheaders.ActionClaim, codexheaders.ActionAdopt:
		return true
	default:
		return false
	}
}

func (o *Operation) requestClaim(
	candidate codexheaders.BindingCandidate,
	scope codexidentity.ProtocolScope,
	routeTargetID string,
) codexcontinuity.ClaimRequest {
	return codexcontinuity.ClaimRequest{
		Evidence: evidence(candidate),
		Scope: codexcontinuity.Scope{
			CurrentClientScope: o.currentClientScope, ClientScopeCandidates: o.clientScopes,
			ProtocolScope: scope, RouteTargetHint: routeTargetID,
		},
		OperationID: o.operationID,
	}
}

func (o *Operation) abandonRequestLeases(ctx context.Context, leases []codexcontinuity.Lease) {
	abandonContext := context.WithoutCancel(ctx)
	for _, lease := range leases {
		if lease.NewlyClaimed() {
			_ = o.runtime.continuity.AbandonBeforeDisclosure(abandonContext, lease)
		}
	}
}

// MarkDisclosed promotes attempt-local constraints only after the transport
// reports that request data may have crossed the physical boundary.
func (a *Attempt) MarkDisclosed(ctx context.Context) error {
	if a == nil || a.operation == nil || a.operation.apiType != codexAPIType {
		return nil
	}
	o := a.operation
	o.mu.Lock()
	defer o.mu.Unlock()
	if a.settlement == attemptAbandoned {
		return identityError("continuity_disclosure", errors.New("abandoned attempt cannot be disclosed"))
	}
	if a.settlement == attemptDisclosed {
		return nil
	}
	if err := o.pinAttemptLocked(a); err != nil {
		return err
	}
	a.settlement = attemptDisclosureStarted
	commitContext := context.WithoutCancel(ctx)
	for len(a.requestClaimLeases) > 0 {
		if _, err := o.runtime.continuity.Commit(commitContext, a.requestClaimLeases[0]); err != nil {
			return dependencyError("continuity_disclosure", err)
		}
		a.requestClaimLeases = a.requestClaimLeases[1:]
	}
	o.requestClaimsCommitted = true
	a.settlement = attemptDisclosed
	return nil
}

// AbandonBeforeDisclosure releases only attempt-created pending claims. If the
// persistence layer cannot prove the release, the attempt authority is promoted
// conservatively so a later retry cannot create a conflicting owner elsewhere.
func (a *Attempt) AbandonBeforeDisclosure(ctx context.Context) error {
	if a == nil || a.operation == nil || a.operation.apiType != codexAPIType {
		return nil
	}
	o := a.operation
	o.mu.Lock()
	defer o.mu.Unlock()
	switch a.settlement {
	case attemptAbandoned:
		return nil
	case attemptDisclosureStarted, attemptDisclosed:
		return identityError("continuity_abandon", errors.New("disclosed attempt cannot be abandoned"))
	}

	abandonContext := context.WithoutCancel(ctx)
	var firstErr error
	for _, lease := range a.requestClaimLeases {
		if !lease.NewlyClaimed() {
			continue
		}
		if err := o.runtime.continuity.AbandonBeforeDisclosure(abandonContext, lease); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	a.requestClaimLeases = nil
	if firstErr != nil {
		if err := o.pinAttemptLocked(a); err != nil {
			return err
		}
		a.settlement = attemptAbandoned
		return dependencyError("continuity_abandon", firstErr)
	}
	a.settlement = attemptAbandoned
	return nil
}

func (o *Operation) pinAttemptLocked(attempt *Attempt) error {
	if attempt.pinProtocolScope {
		if o.requiredProtocolScope != nil && !o.requiredProtocolScope.Equal(attempt.protocolScope) {
			return identityError("required_protocol_scope", errors.New("disclosed attempt crosses the required protocol scope"))
		}
		copyScope := attempt.protocolScope
		o.requiredProtocolScope = &copyScope
	}
	if attempt.pinAuthority {
		if o.requiredAuthority != nil && !o.requiredAuthority.Equal(attempt.authority) {
			return identityError("required_authority", errors.New("disclosed attempt crosses the required authority"))
		}
		copyAuthority := attempt.authority
		o.requiredAuthority = &copyAuthority
	}
	return nil
}

func (a *Attempt) ObserveResponse(head *upstreamtransport.ResponseHead) error {
	if a == nil || a.operation == nil || a.operation.apiType != codexAPIType || head == nil {
		return nil
	}
	o := a.operation
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.cookieRequest == nil {
		return dependencyError("provider_cookie", errors.New("request cookie state is unavailable"))
	}
	if _, err := o.cookieRequest.ApplyResponse(
		a.authority.CookieAuthority(), a.responseURL, head.SourceHeader.Values("Set-Cookie"),
	); err != nil {
		return dependencyError("provider_cookie_response", err)
	}
	// SourceHeader remains an immutable transport observation; only the client
	// projection is stripped so provider cookies never cross the gateway.
	head.Header.Del("Set-Cookie")
	return nil
}

type Visibility struct {
	operation *Operation
	leases    []codexcontinuity.Lease
	mu        sync.Mutex
	committed bool
}

func (a *Attempt) PrepareVisible(ctx context.Context, headers http.Header) (*Visibility, error) {
	visibility := &Visibility{}
	if a == nil || a.operation == nil {
		return visibility, nil
	}
	o := a.operation
	visibility.operation = o
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.apiType != codexAPIType {
		return visibility, nil
	}
	leases, err := o.prepareResponseContinuityLocked(ctx, a.protocolScope, a.routeTargetID, headers)
	if err != nil {
		return nil, err
	}
	visibility.leases = leases
	if o.cookieRequest == nil {
		return nil, dependencyError("provider_cookie", errors.New("request cookie state is unavailable"))
	}
	if _, err := o.cookieRequest.Commit(ctx, a.authority.CookieAuthority()); err != nil {
		// Response pending owners intentionally remain pending when Cookie merge
		// fails; releasing them could allow another Authority to claim a value
		// that the client may observe after an uncertain adapter failure.
		return nil, dependencyError("provider_cookie_commit", err)
	}
	o.cookieClosed = true
	headers.Del("Set-Cookie")
	if o.gatewaySetCookie != "" {
		headers.Add("Set-Cookie", o.gatewaySetCookie)
	}
	return visibility, nil
}

func (o *Operation) prepareResponseContinuityLocked(
	ctx context.Context,
	scope codexidentity.ProtocolScope,
	routeTargetID string,
	headers http.Header,
) ([]codexcontinuity.Lease, error) {
	discovery := codexheaders.DecideServerHeaders(headers, func(codexheaders.BindingCandidate) codexheaders.OwnerStatus {
		return codexheaders.OwnerUnknown
	})
	decision, leases, err := o.prepareServerDecisionLocked(
		ctx, scope, routeTargetID, discovery,
		func(owners codexheaders.OwnerLookup) codexheaders.Result {
			return codexheaders.DecideServerHeaders(headers, owners)
		},
	)
	for _, name := range decision.HeaderNamesToDrop() {
		headers.Del(name)
	}
	return leases, err
}

func (o *Operation) prepareServerMessageLocked(
	ctx context.Context,
	scope codexidentity.ProtocolScope,
	routeTargetID string,
	message codexheaders.MessageView,
) ([]codexcontinuity.Lease, error) {
	discovery := codexheaders.DecideServerMessage(message, func(codexheaders.BindingCandidate) codexheaders.OwnerStatus {
		return codexheaders.OwnerUnknown
	})
	_, leases, err := o.prepareServerDecisionLocked(
		ctx, scope, routeTargetID, discovery,
		func(owners codexheaders.OwnerLookup) codexheaders.Result {
			return codexheaders.DecideServerMessage(message, owners)
		},
	)
	return leases, err
}

func (o *Operation) prepareServerDecisionLocked(
	ctx context.Context,
	scope codexidentity.ProtocolScope,
	routeTargetID string,
	discovery codexheaders.Result,
	decide func(codexheaders.OwnerLookup) codexheaders.Result,
) (codexheaders.Result, []codexcontinuity.Lease, error) {
	if len(discovery.Decisions()) == 0 {
		return discovery, nil, nil
	}
	if !o.hasClientScope {
		return discovery, nil, clientError("response_continuity", errors.New("response state requires one client credential"))
	}
	statuses := make(map[[sha256.Size]byte]codexheaders.OwnerStatus)
	lookupErrors := make(map[[sha256.Size]byte]error)
	existingLeases := make(map[[sha256.Size]byte]codexcontinuity.Lease)
	for _, decision := range discovery.Decisions() {
		candidate := decision.Candidate()
		if _, persistent := candidate.PersistentNamespace(); !persistent {
			continue
		}
		lease, err := o.runtime.continuity.AcquireExisting(ctx, codexcontinuity.ValidateRequest{
			Evidence: evidence(candidate), ClientScopeCandidates: o.clientScopes,
			ProtocolScope: scope, OperationID: o.operationID,
		})
		switch {
		case err == nil:
			statuses[candidateKey(candidate)] = codexheaders.OwnerCurrent
			existingLeases[candidateKey(candidate)] = lease
		case codexcontinuity.IsError(err, codexcontinuity.ErrorUnknown):
			statuses[candidateKey(candidate)] = codexheaders.OwnerUnknown
		case codexcontinuity.IsError(err, codexcontinuity.ErrorConflict),
			codexcontinuity.IsError(err, codexcontinuity.ErrorExpired):
			statuses[candidateKey(candidate)] = codexheaders.OwnerConflict
		case continuityPersistenceUnavailable(err):
			statuses[candidateKey(candidate)] = codexheaders.OwnerStoreUnavailable
			lookupErrors[candidateKey(candidate)] = err
		default:
			statuses[candidateKey(candidate)] = codexheaders.OwnerUnavailable
			lookupErrors[candidateKey(candidate)] = err
		}
	}
	decision := decide(func(candidate codexheaders.BindingCandidate) codexheaders.OwnerStatus {
		if status, exists := statuses[candidateKey(candidate)]; exists {
			return status
		}
		return codexheaders.OwnerUnavailable
	})
	if decision.Rejected() {
		for _, item := range decision.Decisions() {
			if item.Reason() == codexheaders.ReasonOwnerUnavailable {
				return decision, nil, dependencyError("response_continuity", lookupErrors[candidateKey(item.Candidate())])
			}
		}
		return decision, nil, clientError("response_continuity", decisionError(decision))
	}
	var leases []codexcontinuity.Lease
	for _, item := range decision.Decisions() {
		if item.Action() != codexheaders.ActionForward {
			continue
		}
		if lease, exists := existingLeases[candidateKey(item.Candidate())]; exists {
			leases = append(leases, lease)
		}
	}
	for _, claim := range decision.Claims() {
		lease, err := o.runtime.continuity.PrepareVisible(ctx, codexcontinuity.ClaimRequest{
			Evidence: evidence(claim.Candidate()),
			Scope: codexcontinuity.Scope{
				CurrentClientScope: o.currentClientScope, ClientScopeCandidates: o.clientScopes,
				ProtocolScope: scope, RouteTargetHint: routeTargetID,
			},
			OperationID: o.operationID,
		})
		if err != nil {
			return decision, nil, dependencyError("response_continuity_prepare", err)
		}
		leases = append(leases, lease)
	}
	return decision, leases, nil
}

func (v *Visibility) Commit(ctx context.Context) error {
	if v == nil || v.operation == nil {
		return nil
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.committed {
		return nil
	}
	commitContext := context.WithoutCancel(ctx)
	for len(v.leases) > 0 {
		if _, err := v.operation.runtime.continuity.Commit(commitContext, v.leases[0]); err != nil {
			return dependencyError("response_continuity_commit", err)
		}
		v.leases = v.leases[1:]
	}
	v.committed = true
	return nil
}

func (o *Operation) Discard() {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.cookieRequest != nil && !o.cookieClosed {
		o.cookieRequest.DiscardAll()
		o.cookieClosed = true
	}
}
