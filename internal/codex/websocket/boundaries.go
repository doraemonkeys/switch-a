package codexws

import (
	"context"
	"crypto/sha256"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/doraemonkeys/switch-a/internal/codex/continuity"
	"github.com/doraemonkeys/switch-a/internal/codex/cookie"
	"github.com/doraemonkeys/switch-a/internal/codex/headers"
	"github.com/doraemonkeys/switch-a/internal/codex/identity"
)

// PrepareDial is called only after the authenticator has returned and validated
// AppliedIdentity for the final physical dial URL.
func (o *Operation) PrepareDial(
	ctx context.Context,
	headers http.Header,
	candidate codexidentity.CandidateSnapshot,
	applied codexidentity.AppliedIdentity,
	finalURL *url.URL,
) (*Permit, error) {
	if o == nil {
		return nil, &Failure{Class: FailureStorage, Stage: "prepare_dial", Cause: errors.New("operation is unavailable")}
	}
	if err := candidate.ValidateApplied(applied); err != nil {
		return nil, &Failure{Class: FailureIdentity, Stage: "applied_identity", Cause: err}
	}
	if err := o.bindPhysicalCandidate(candidate); err != nil {
		return nil, err
	}

	permit := &Permit{operation: o}
	var (
		decision codexheaders.Result
		owners   map[[sha256.Size]byte]ownerResolution
	)
	if o.features.Continuity {
		var err error
		decision, owners, err = o.decideClient(ctx, o.headers, codexheaders.MessageView{})
		if err != nil {
			return nil, err
		}
		for _, name := range decision.HeaderNamesToDrop() {
			deleteHeaderFold(headers, name)
		}
	}

	stripGatewayHandleCookie(headers)
	if o.features.ProviderCookieJar {
		if err := o.selectCookiesForDial(ctx, headers, applied.Authority().CookieAuthority(), finalURL); err != nil {
			return nil, err
		}
	}
	if o.features.Continuity {
		var err error
		permit, err = o.prepareClaims(ctx, decision, claimOptions{resolutions: owners})
		if err != nil {
			return nil, err
		}
	}
	return permit, nil
}

// bindPhysicalCandidate records only the identity used by this dial. Security
// constraints are established separately at evidence and visibility boundaries,
// so an undisclosed failed attempt cannot constrain a later replacement.
func (o *Operation) bindPhysicalCandidate(candidate codexidentity.CandidateSnapshot) error {
	if _, err := candidate.ProtocolScope().MarshalBinary(); err != nil {
		return &Failure{Class: FailureIdentity, Stage: "candidate", Cause: err}
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.requiredProtocolScope != nil && !o.requiredProtocolScope.Equal(candidate.ProtocolScope()) {
		return &Failure{Class: FailureIdentity, Stage: "candidate", Cause: errors.New("candidate violates required protocol scope")}
	}
	if o.requiredAuthority != nil && !o.requiredAuthority.Equal(candidate.Authority()) {
		return &Failure{Class: FailureIdentity, Stage: "candidate", Cause: errors.New("candidate violates required authority")}
	}
	if o.visibleRouteTargetID != "" && o.visibleRouteTargetID != candidate.RouteTargetID() {
		return &Failure{Class: FailureIdentity, Stage: "candidate", Cause: errors.New("candidate violates client-visible route target")}
	}
	copyCandidate := candidate
	o.physicalCandidate = &copyCandidate
	return nil
}

func (o *Operation) selectCookiesForDial(
	ctx context.Context,
	headers http.Header,
	authority codexidentity.CookieAuthority,
	finalURL *url.URL,
) error {
	deleteHeaderFold(headers, "Cookie")
	o.mu.Lock()
	previous := o.lastCookieAuthority
	o.mu.Unlock()
	if previous != nil && !previous.Equal(authority) {
		// An overlay belongs to one security authority. A physical replacement may
		// cross authority only while owner-free, and its abandoned overlay must not
		// follow the request into the newly selected CookieScope.
		if err := o.cookieRequest.Discard(*previous); err != nil {
			return cookieFailure("discard_replaced_cookies", err)
		}
	}
	cookieValue, err := o.cookieRequest.Select(ctx, authority, cloneURL(finalURL))
	if err != nil {
		return cookieFailure("select_cookies", err)
	}
	if cookieValue != "" {
		headers.Set("Cookie", cookieValue)
	}
	o.mu.Lock()
	o.lastCookieAuthority = &authority
	o.mu.Unlock()
	return nil
}

func (o *Operation) ApplyHandshake(finalURL *url.URL, headers http.Header) error {
	if o == nil || !o.features.ProviderCookieJar {
		return nil
	}
	o.mu.Lock()
	authority := o.lastCookieAuthority
	o.mu.Unlock()
	if authority == nil {
		return &Failure{Class: FailureStorage, Stage: "handshake_cookies", Cause: errors.New("attempt cookie authority is unavailable")}
	}
	_, err := o.cookieRequest.ApplyResponse(*authority, cloneURL(finalURL), headerValues(headers, "Set-Cookie"))
	if err != nil {
		return cookieFailure("handshake_cookies", err)
	}
	return nil
}

func (o *Operation) CommitCookies(ctx context.Context) error {
	if o == nil || !o.features.ProviderCookieJar {
		return nil
	}
	o.mu.Lock()
	if o.cookieClosed {
		o.mu.Unlock()
		return nil
	}
	authority := o.lastCookieAuthority
	o.mu.Unlock()
	if authority == nil {
		return &Failure{Class: FailureStorage, Stage: "commit_cookies", Cause: errors.New("final cookie authority is unavailable")}
	}
	if _, err := o.cookieRequest.Commit(ctx, *authority); err != nil {
		return cookieFailure("commit_cookies", err)
	}
	o.mu.Lock()
	o.cookieClosed = true
	o.mu.Unlock()
	return nil
}

func (o *Operation) DiscardCookies() {
	if o == nil || !o.features.ProviderCookieJar || o.cookieRequest == nil {
		return
	}
	o.mu.Lock()
	if o.cookieClosed {
		o.mu.Unlock()
		return
	}
	o.cookieClosed = true
	o.mu.Unlock()
	o.cookieRequest.DiscardAll()
}

func (o *Operation) PrepareClientFrame(ctx context.Context, text bool, payload []byte) (*Permit, error) {
	if o == nil || !o.features.Continuity {
		return nil, nil
	}
	if !text {
		return nil, nil
	}
	message := codexheaders.InspectClientFrame(codexheaders.FixtureCodexDesktop0150Alpha8, payload)
	decision, owners, err := o.decideClient(ctx, nil, message)
	if err != nil {
		return nil, err
	}
	return o.prepareClaims(ctx, decision, claimOptions{resolutions: owners})
}

func (o *Operation) PrepareServerHeaders(ctx context.Context, headers http.Header) (*Permit, http.Header, error) {
	projected := make(http.Header)
	if o == nil || !o.features.Continuity {
		return nil, projected, nil
	}
	discovery := codexheaders.DecideServerHeaders(headers, func(codexheaders.BindingCandidate) codexheaders.OwnerStatus {
		return codexheaders.OwnerUnknown
	})
	owners, err := o.resolveOwners(ctx, discovery)
	if err != nil {
		return nil, nil, err
	}
	decision := codexheaders.DecideServerHeaders(headers, o.ownerLookupForBoundScope(owners))
	if decision.Rejected() {
		return nil, nil, protocolFailure("server_headers", decision)
	}
	for name, values := range headers {
		if http.CanonicalHeaderKey(name) == http.CanonicalHeaderKey("X-Codex-Turn-State") {
			projected[http.CanonicalHeaderKey(name)] = append([]string(nil), values...)
		}
	}
	permit, err := o.prepareClaims(ctx, decision, claimOptions{visible: true, resolutions: owners})
	return permit, projected, err
}

type responseTransition uint8

const (
	responseUnchanged responseTransition = iota
	responseActivated
	responseCompleted
)

const (
	eventResponseCreated    = "response.created"
	eventResponseInProgress = "response.in_progress"
	eventResponseCompleted  = "response.completed"
)

type claimOptions struct {
	visible     bool
	response    responseTransition
	resolutions map[[sha256.Size]byte]ownerResolution
}

func responseTransitionFor(eventType string) responseTransition {
	switch eventType {
	case eventResponseCreated, eventResponseInProgress:
		return responseActivated
	case eventResponseCompleted:
		return responseCompleted
	default:
		return responseUnchanged
	}
}

func (o *Operation) PrepareServerFrame(ctx context.Context, text bool, payload []byte) (*Permit, error) {
	if o == nil || !o.features.Continuity {
		return nil, nil
	}
	if !text {
		return nil, nil
	}
	message := codexheaders.InspectServerFrame(codexheaders.FixtureCodexDesktop0150Alpha8, payload)
	discovery := codexheaders.DecideServerMessage(message, func(codexheaders.BindingCandidate) codexheaders.OwnerStatus {
		return codexheaders.OwnerUnknown
	})
	if discovery.Rejected() {
		return nil, protocolFailure("server_frame", discovery)
	}
	owners, err := o.resolveOwners(ctx, discovery)
	if err != nil {
		return nil, err
	}
	decision := codexheaders.DecideServerMessage(message, o.ownerLookupForBoundScope(owners))
	if decision.Rejected() {
		return nil, protocolFailure("server_frame", decision)
	}
	return o.prepareClaims(ctx, decision, claimOptions{
		visible:     true,
		response:    responseTransitionFor(message.EventType()),
		resolutions: owners,
	})
}

func (o *Operation) decideClient(
	ctx context.Context,
	headers http.Header,
	message codexheaders.MessageView,
) (codexheaders.Result, map[[sha256.Size]byte]ownerResolution, error) {
	discovery := codexheaders.DecideClient(codexheaders.ClientInput{
		Headers: headers, Message: message,
		Owners:          func(codexheaders.BindingCandidate) codexheaders.OwnerStatus { return codexheaders.OwnerUnknown },
		AttestationLock: codexheaders.OperationUnlocked,
	})
	owners, err := o.resolveOwners(ctx, discovery)
	if err != nil {
		return codexheaders.Result{}, nil, err
	}
	decision := codexheaders.DecideClient(codexheaders.ClientInput{
		Headers: headers, Message: message,
		Owners:          o.ownerLookupForBoundScope(owners),
		AttestationLock: o.attestationStatus(),
	})
	if decision.Rejected() {
		return decision, nil, protocolFailure("client_frame", decision)
	}
	return decision, owners, nil
}

func (o *Operation) ownerLookupForBoundScope(owners map[[sha256.Size]byte]ownerResolution) codexheaders.OwnerLookup {
	o.mu.Lock()
	var scope *codexidentity.ProtocolScope
	if o.physicalCandidate != nil {
		candidateScope := o.physicalCandidate.ProtocolScope()
		scope = &candidateScope
	}
	o.mu.Unlock()
	return func(candidate codexheaders.BindingCandidate) codexheaders.OwnerStatus {
		resolution, exists := owners[candidateKey(candidate)]
		if !exists {
			return codexheaders.OwnerUnavailable
		}
		if resolution.status == codexheaders.OwnerCurrent && scope != nil &&
			!resolution.binding.Owner.ProtocolScope.Equal(*scope) {
			return codexheaders.OwnerConflict
		}
		return resolution.status
	}
}

func (o *Operation) attestationStatus() codexheaders.OperationLockStatus {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.physicalCandidate == nil {
		return codexheaders.OperationLockUnavailable
	}
	if o.requiredAuthority == nil {
		return codexheaders.OperationUnlocked
	}
	if o.requiredAuthority.Equal(o.physicalCandidate.Authority()) {
		return codexheaders.OperationAuthorityCurrent
	}
	return codexheaders.OperationAuthorityConflict
}

func (o *Operation) prepareClaims(ctx context.Context, result codexheaders.Result, options claimOptions) (*Permit, error) {
	permit := &Permit{operation: o}
	responseLeases := make(map[[sha256.Size]byte]codexcontinuity.Lease)
	for _, decision := range result.Claims() {
		if decision.Claim().Lifetime() == codexheaders.ClaimLifetimeOperation {
			continue
		}
		lease, err := o.prepareClaimLease(ctx, decision, options.visible)
		if err != nil {
			permit.abandon(ctx)
			return nil, err
		}
		permit.leases = append(permit.leases, lease)
		if decision.Field() == codexheaders.FieldResponseReference {
			responseLeases[candidateKey(decision.Candidate())] = lease
		}
	}
	for _, decision := range result.Decisions() {
		if decision.Action() != codexheaders.ActionForward {
			continue
		}
		resolution, exists := options.resolutions[candidateKey(decision.Candidate())]
		if !exists || resolution.status != codexheaders.OwnerCurrent {
			continue
		}
		lease, err := o.acquireExistingLease(ctx, decision)
		if err != nil {
			permit.abandon(ctx)
			return nil, err
		}
		if !containsLease(permit.leases, lease) {
			permit.leases = append(permit.leases, lease)
		}
		if decision.Field() == codexheaders.FieldResponseReference {
			responseLeases[candidateKey(decision.Candidate())] = lease
		}
	}
	attachResponseTransition(permit, result, options.response, responseLeases)
	if err := o.pinDisclosedEvidence(result, options); err != nil {
		permit.abandon(ctx)
		return nil, err
	}
	return permit, nil
}

func (o *Operation) pinDisclosedEvidence(result codexheaders.Result, options claimOptions) error {
	if err := o.applyRequiredOwners(options.resolutions); err != nil {
		return err
	}
	pinProtocolScope := false
	pinAuthority := false
	if options.response != responseUnchanged {
		for _, decision := range result.Decisions() {
			if decision.Field() == codexheaders.FieldResponseReference {
				pinProtocolScope = true
				break
			}
		}
	}
	for _, decision := range result.Claims() {
		switch decision.Claim().Boundary() {
		case codexheaders.ClaimBoundaryProtocolScope:
			pinProtocolScope = true
		case codexheaders.ClaimBoundaryAuthority:
			pinAuthority = true
		}
	}
	return o.pinPhysicalCandidate(pinProtocolScope, pinAuthority, false)
}

func (o *Operation) pinPhysicalCandidate(protocolScope, authority, routeTarget bool) error {
	if !protocolScope && !authority && !routeTarget {
		return nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.physicalCandidate == nil {
		return &Failure{Class: FailureIdentity, Stage: "security_pin", Cause: errors.New("provider identity is not bound")}
	}
	candidate := *o.physicalCandidate
	if protocolScope {
		if err := o.pinProtocolScopeLocked(candidate.ProtocolScope(), "security_pin"); err != nil {
			return err
		}
	}
	if authority || routeTarget {
		candidateAuthority := candidate.Authority()
		if o.requiredAuthority != nil && !o.requiredAuthority.Equal(candidateAuthority) {
			return &Failure{Class: FailureIdentity, Stage: "security_pin", Cause: errors.New("candidate conflicts with required authority")}
		}
		o.requiredAuthority = &candidateAuthority
	}
	if routeTarget {
		if o.visibleRouteTargetID != "" && o.visibleRouteTargetID != candidate.RouteTargetID() {
			return &Failure{Class: FailureIdentity, Stage: "security_pin", Cause: errors.New("candidate conflicts with client-visible route target")}
		}
		o.visibleRouteTargetID = candidate.RouteTargetID()
		o.preferredRouteTargetID = candidate.RouteTargetID()
	}
	return nil
}

// PinClientVisible fixes the physical RouteTarget only after bytes from that
// attempt became observable downstream. Probe acceptance can call this before a
// provider exists; the first upstream frame will establish the actual pin.
func (o *Operation) PinClientVisible() {
	if o == nil {
		return
	}
	_ = o.pinPhysicalCandidate(true, true, true)
}

func (o *Operation) acquireExistingLease(
	ctx context.Context,
	decision codexheaders.Decision,
) (codexcontinuity.Lease, error) {
	candidate, ok := o.candidateSnapshot()
	if !ok {
		return codexcontinuity.Lease{}, &Failure{Class: FailureIdentity, Stage: "acquire_existing", Cause: errors.New("provider identity is not bound")}
	}
	lease, err := o.runtime.continuity.AcquireExisting(ctx, codexcontinuity.ValidateRequest{
		Evidence:              evidence(decision.Candidate()),
		ClientScopeCandidates: append([]codexidentity.ClientScope(nil), o.clientScopes...),
		ProtocolScope:         candidate.ProtocolScope(),
		OperationID:           o.operationID,
	})
	if err != nil {
		return codexcontinuity.Lease{}, continuityFailure("acquire_existing", err)
	}
	return lease, nil
}

func (o *Operation) prepareClaimLease(
	ctx context.Context,
	decision codexheaders.Decision,
	visible bool,
) (codexcontinuity.Lease, error) {
	candidate, ok := o.candidateSnapshot()
	if !ok {
		return codexcontinuity.Lease{}, &Failure{Class: FailureIdentity, Stage: "claim", Cause: errors.New("provider identity is not bound")}
	}
	request := codexcontinuity.ClaimRequest{
		Evidence: evidence(decision.Candidate()),
		Scope: codexcontinuity.Scope{
			CurrentClientScope:    o.currentClientScope,
			ClientScopeCandidates: append([]codexidentity.ClientScope(nil), o.clientScopes...),
			ProtocolScope:         candidate.ProtocolScope(),
			RouteTargetHint:       candidate.RouteTargetID(),
		},
		OperationID: o.operationID,
	}
	var (
		lease codexcontinuity.Lease
		err   error
	)
	if visible {
		lease, err = o.runtime.continuity.PrepareVisible(ctx, request)
	} else {
		lease, err = o.runtime.continuity.Claim(ctx, request)
	}
	if err != nil {
		return codexcontinuity.Lease{}, continuityFailure("claim", err)
	}
	return lease, nil
}

func attachResponseTransition(
	permit *Permit,
	result codexheaders.Result,
	transition responseTransition,
	responseLeases map[[sha256.Size]byte]codexcontinuity.Lease,
) {
	if transition == responseUnchanged {
		return
	}
	for _, decision := range result.Decisions() {
		if decision.Field() != codexheaders.FieldResponseReference {
			continue
		}
		key := candidateKey(decision.Candidate())
		lease, claimed := responseLeases[key]
		if claimed {
			permit.attachResponseLease(lease, transition)
		}
	}
}

func (p *Permit) attachResponseLease(lease codexcontinuity.Lease, transition responseTransition) {
	switch transition {
	case responseActivated:
		p.activate = append(p.activate, lease)
	case responseCompleted:
		p.deactivate = append(p.deactivate, lease.Binding())
	}
}

func (o *Operation) candidateSnapshot() (codexidentity.CandidateSnapshot, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.physicalCandidate == nil {
		return codexidentity.CandidateSnapshot{}, false
	}
	return *o.physicalCandidate, true
}

func (o *Operation) OpenConnection() error {
	if o == nil || !o.features.Continuity {
		return nil
	}
	candidate, ok := o.candidateSnapshot()
	if !ok {
		return &Failure{Class: FailureIdentity, Stage: "open_connection", Cause: errors.New("provider identity is not bound")}
	}
	generation, err := o.runtime.continuity.OpenConnection(o.operationID, candidate.ProtocolScope())
	if err != nil {
		return continuityFailure("open_connection", err)
	}
	o.mu.Lock()
	if o.generation != nil {
		o.mu.Unlock()
		o.runtime.continuity.CloseConnection(generation)
		return &Failure{Class: FailureIdentity, Stage: "open_connection", Cause: errors.New("another connection generation is active")}
	}
	o.generation = &generation
	o.mu.Unlock()
	return nil
}

func (o *Operation) CloseConnection() {
	if o == nil || !o.features.Continuity {
		return
	}
	o.mu.Lock()
	generation := o.generation
	o.generation = nil
	o.mu.Unlock()
	if generation != nil {
		o.runtime.continuity.CloseConnection(*generation)
	}
}

func (o *Operation) currentGeneration() (codexcontinuity.Generation, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.generation == nil {
		return "", false
	}
	return *o.generation, true
}

func deleteHeaderFold(headers http.Header, wanted string) {
	for name := range headers {
		if http.CanonicalHeaderKey(name) == http.CanonicalHeaderKey(wanted) {
			delete(headers, name)
		}
	}
}

func stripGatewayHandleCookie(headers http.Header) {
	values := headerValues(headers, "Cookie")
	deleteHeaderFold(headers, "Cookie")
	for _, value := range values {
		kept := make([]string, 0)
		for _, pair := range strings.Split(value, ";") {
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
