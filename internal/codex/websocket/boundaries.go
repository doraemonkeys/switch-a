package codexws

import (
	"context"
	"crypto/sha256"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/doraemonkeys/switch-a/internal/codex/continuity"
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

	decision, owners, err := o.decideClient(ctx, o.headers, codexheaders.MessageView{})
	if err != nil {
		return nil, err
	}
	for _, name := range decision.HeaderNamesToDrop() {
		deleteHeaderFold(headers, name)
	}

	stripGatewayHandleCookie(headers)
	if err := o.selectCookiesForDial(ctx, headers, applied.Authority().CookieAuthority(), finalURL); err != nil {
		return nil, err
	}
	permit, err := o.prepareClaims(ctx, decision, claimOptions{resolutions: owners})
	if err != nil {
		return nil, err
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

// CommitVisibility is the one-way boundary for a selected physical attempt.
// It is intentionally absent from ordinary 101 handling: only projected Turn
// State or a successfully written upstream application frame reaches it.
func (o *Operation) CommitVisibility(ctx context.Context) error {
	if o == nil {
		return nil
	}
	o.mu.Lock()
	if o.visibilityCommitted {
		o.mu.Unlock()
		return nil
	}
	o.mu.Unlock()
	if err := o.pinPhysicalCandidate(true, true, true); err != nil {
		return err
	}
	if err := o.CommitCookies(ctx); err != nil {
		return err
	}
	o.mu.Lock()
	o.visibilityCommitted = true
	o.mu.Unlock()
	return nil
}

func (o *Operation) ClassifyClientFrame(ctx context.Context, text bool, payload []byte) *ClientFramePermit {
	permit := &ClientFramePermit{
		operation:           o,
		disposition:         ClientFrameForward,
		replayEligible:      true,
		replacementEligible: true,
		trace: ClientFrameTrace{
			Kind: ClientFrameOpaque, Decision: ClientFrameForward,
		},
	}
	if o == nil || !text {
		return permit
	}
	message := codexheaders.InspectClientFrame(payload)
	switch message.EventType() {
	case "response.create":
		permit.trace.Kind = ClientFrameResponseCreate
		permit.trace.EventType = "response.create"
	case "response.append":
		permit.trace.Kind = ClientFrameResponseAppend
		permit.trace.EventType = "response.append"
		permit.replayEligible = false
		permit.replacementEligible = false
		permit.currentConnectionRequired = true
	case "response.inject":
		permit.trace.Kind = ClientFrameResponseInject
		permit.trace.EventType = "response.inject"
		permit.replayEligible = false
		permit.replacementEligible = false
		permit.currentConnectionRequired = true
	}
	decision, owners, err := o.decideClient(ctx, nil, message)
	if err == nil && permit.currentConnectionRequired {
		if _, active := o.currentGeneration(); !active {
			err = reconnectRequiredFailure("client_frame_connection")
		}
	}
	if err != nil {
		permit.disposition = ClientFrameReject
		permit.replayEligible = false
		permit.replacementEligible = false
		permit.rejection = err
		permit.trace.Decision = ClientFrameReject
		return permit
	}
	permit.decision = decision
	permit.resolutions = owners
	return permit
}

func (o *Operation) PrepareClientFrame(ctx context.Context, text bool, payload []byte) (*Permit, error) {
	if o == nil || !text {
		return nil, nil
	}
	frame := o.ClassifyClientFrame(ctx, text, payload)
	permit, err := frame.PrepareDelivery(ctx)
	if err != nil || permit != nil {
		return permit, err
	}
	// Legacy callers use a non-nil permit as the successful text-frame signal.
	// The state-machine relay consumes ClientFramePermit directly, so this empty
	// durable permit carries no classification or replay policy.
	return &Permit{operation: o}, nil
}

func (o *Operation) PrepareServerHeaders(ctx context.Context, headers http.Header) (*Permit, http.Header, error) {
	projected := projectPassableHandshakeHeaders(headers)
	if o == nil {
		return nil, projected, nil
	}
	discovery := codexheaders.DecideServerHeaders(headers, func(codexheaders.BindingCandidate) codexheaders.OwnerStatus {
		return codexheaders.OwnerUnknown
	})
	owners, err := o.resolveServerOwners(ctx, discovery)
	if err != nil {
		return nil, nil, err
	}
	decision := codexheaders.DecideServerHeaders(headers, o.ownerLookupForBoundScope(owners))
	if decision.Rejected() {
		if err := resolvedOwnerFailure("server_headers", decision, owners); err != nil {
			return nil, nil, err
		}
		return nil, nil, protocolFailure("server_headers", decision)
	}
	for name, values := range headers {
		if http.CanonicalHeaderKey(name) == http.CanonicalHeaderKey("X-Codex-Turn-State") {
			projected[http.CanonicalHeaderKey(name)] = append([]string(nil), values...)
		}
	}
	permit, err := o.prepareClaims(ctx, decision, claimOptions{visible: true, resolutions: owners})
	if permit != nil && len(headerValues(projected, "X-Codex-Turn-State")) > 0 {
		permit.pinProtocolScope = true
		permit.pinAuthority = true
		permit.pinRouteTarget = true
	}
	return permit, projected, err
}

type responseTransition uint8

const (
	responseUnchanged responseTransition = iota
	responseActivated
	responseTerminal
)

type claimOptions struct {
	visible     bool
	response    responseTransition
	resolutions map[[sha256.Size]byte]ownerResolution
}

func responseTransitionFor(lifecycle codexheaders.ResponseLifecycle) responseTransition {
	switch lifecycle {
	case codexheaders.ResponseLifecycleActive:
		return responseActivated
	case codexheaders.ResponseLifecycleTerminal:
		return responseTerminal
	default:
		return responseUnchanged
	}
}

func (o *Operation) PrepareServerFrame(ctx context.Context, text bool, payload []byte) (*Permit, error) {
	if o == nil {
		return nil, nil
	}
	if !text {
		return nil, nil
	}
	message := codexheaders.InspectServerFrame(payload)
	discovery := codexheaders.DecideServerMessage(message, func(codexheaders.BindingCandidate) codexheaders.OwnerStatus {
		return codexheaders.OwnerUnknown
	})
	if discovery.Rejected() {
		return nil, protocolFailure("server_frame", discovery)
	}
	owners, err := o.resolveServerOwners(ctx, discovery)
	if err != nil {
		return nil, err
	}
	decision := codexheaders.DecideServerMessage(message, o.ownerLookupForBoundScope(owners))
	if decision.Rejected() {
		if err := resolvedOwnerFailure("server_frame", decision, owners); err != nil {
			return nil, err
		}
		return nil, protocolFailure("server_frame", decision)
	}
	return o.prepareClaims(ctx, decision, claimOptions{
		visible:     true,
		response:    responseTransitionFor(message.ResponseLifecycle()),
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
	owners, err := o.resolveRequestOwners(ctx, discovery)
	if err != nil {
		return codexheaders.Result{}, nil, err
	}
	if err := o.applyRequiredOwners(owners); err != nil {
		return codexheaders.Result{}, nil, err
	}
	if err := o.anchorUnresolvedEvidenceToPhysicalCandidate(discovery, owners); err != nil {
		return codexheaders.Result{}, nil, err
	}
	decision := codexheaders.DecideClient(codexheaders.ClientInput{
		Headers: headers, Message: message,
		Owners:          o.ownerLookupForBoundScope(owners),
		AttestationLock: o.attestationStatus(),
		StateAdmission:  o.stateAdmission(),
	})
	if decision.Rejected() {
		if err := resolvedOwnerFailure("client_frame", decision, owners); err != nil {
			return decision, nil, err
		}
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
	if o.AllowsAccountSwitch() && !o.visibilityCommitted && !routeTarget {
		return nil
	}
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

func (o *Operation) candidateSnapshot() (codexidentity.CandidateSnapshot, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.physicalCandidate == nil {
		return codexidentity.CandidateSnapshot{}, false
	}
	return *o.physicalCandidate, true
}

func (o *Operation) OpenConnection() error {
	if o == nil {
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
	if o == nil {
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

func projectPassableHandshakeHeaders(headers http.Header) http.Header {
	projected := make(http.Header)
	connectionNominated := make(map[string]struct{})
	for _, value := range headerValues(headers, "Connection") {
		for name := range strings.SplitSeq(value, ",") {
			if name = strings.TrimSpace(name); name != "" {
				connectionNominated[http.CanonicalHeaderKey(name)] = struct{}{}
			}
		}
	}
	for name, values := range headers {
		canonical := http.CanonicalHeaderKey(name)
		if !passableHandshakeHeader(canonical, connectionNominated) {
			continue
		}
		projected[canonical] = append([]string(nil), values...)
	}
	return projected
}

func passableHandshakeHeader(name string, connectionNominated map[string]struct{}) bool {
	if _, nominated := connectionNominated[name]; nominated {
		return false
	}
	if strings.HasPrefix(strings.ToLower(name), "sec-websocket-") {
		return false
	}
	if codexheaders.IsManagedHeader(name) {
		return false
	}
	switch name {
	case "Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Proxy-Connection",
		"Te", "Trailer", "Transfer-Encoding", "Upgrade", "Cookie", "Set-Cookie",
		"Authorization", "X-Api-Key":
		return false
	default:
		return true
	}
}
