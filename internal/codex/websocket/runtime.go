package codexws

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"sync"

	"github.com/doraemonkeys/switch-a/internal/codex/clientcredential"
	"github.com/doraemonkeys/switch-a/internal/codex/clientidentity"
	"github.com/doraemonkeys/switch-a/internal/codex/continuity"
	"github.com/doraemonkeys/switch-a/internal/codex/cookie"
	"github.com/doraemonkeys/switch-a/internal/codex/headers"
	"github.com/doraemonkeys/switch-a/internal/codex/identity"
	"github.com/doraemonkeys/switch-a/internal/codex/provenance"
	"github.com/doraemonkeys/switch-a/internal/model"
)

const codexAPIType = "codex"

type ClientIdentityResolver interface {
	Resolve(context.Context, []byte) (clientidentity.Resolution, error)
}

type Continuity interface {
	Resolve(context.Context, codexcontinuity.ResolveRequest) (codexcontinuity.Resolution, error)
	ResolveOwner(context.Context, codexcontinuity.ResolveRequest) (codexcontinuity.Binding, error)
	AcquireExisting(context.Context, codexcontinuity.ValidateRequest) (codexcontinuity.Lease, error)
	Claim(context.Context, codexcontinuity.ClaimRequest) (codexcontinuity.Lease, error)
	Adopt(context.Context, codexcontinuity.ClaimRequest) (codexcontinuity.Lease, error)
	PrepareVisible(context.Context, codexcontinuity.ClaimRequest) (codexcontinuity.Lease, error)
	Commit(context.Context, codexcontinuity.Lease) (codexcontinuity.Binding, error)
	AbandonBeforeDisclosure(context.Context, codexcontinuity.Lease) error
	OpenConnection(string, codexidentity.ProtocolScope) (codexcontinuity.Generation, error)
	ActivateResponse(codexcontinuity.Generation, codexcontinuity.Lease) error
	DeactivateResponse(codexcontinuity.Generation, codexcontinuity.Binding) error
	CloseConnection(codexcontinuity.Generation)
}

type ProviderCookies interface {
	ResolveJar(context.Context, providercookie.OperationID, string, []codexidentity.ClientScope) (providercookie.JarAccess, error)
	BeginRequest(providercookie.OperationID, providercookie.JarAccess) (*providercookie.Request, error)
}

type ExternalSchemeResolver interface {
	ResolveExternalScheme(*http.Request) (providercookie.ResolvedExternalScheme, error)
}

type Config struct {
	ClientIdentities ClientIdentityResolver
	Continuity       Continuity
	ProviderCookies  ProviderCookies
	ExternalScheme   ExternalSchemeResolver
}

type Runtime struct {
	clientIdentities ClientIdentityResolver
	continuity       Continuity
	providerCookies  ProviderCookies
	externalScheme   ExternalSchemeResolver
}

func New(config Config) (*Runtime, error) {
	if config.ClientIdentities == nil || config.Continuity == nil || config.ProviderCookies == nil || config.ExternalScheme == nil {
		return nil, fmt.Errorf("initialize Codex WebSocket runtime: client scopes, continuity, provider cookies, and external scheme are required")
	}
	return &Runtime{
		clientIdentities: config.ClientIdentities, continuity: config.Continuity,
		providerCookies: config.ProviderCookies, externalScheme: config.ExternalScheme,
	}, nil
}

type ownerResolution struct {
	status  codexheaders.OwnerStatus
	binding codexcontinuity.Binding
	err     error
}

// Operation is request-local. Its mutex protects the two relay directions,
// which may validate and commit state concurrently after the handshake.
type Operation struct {
	recoveryPolicy model.ConversationRecoveryPolicy
	ledger         *codexprovenance.Ledger
	runtime        *Runtime
	operationID    string
	apiType        string
	headers        http.Header

	clientIdentity     clientidentity.Resolution
	currentClientScope codexidentity.ClientScope
	clientScopes       []codexidentity.ClientScope
	hasClientScope     bool

	mu                     sync.Mutex
	requiredProtocolScope  *codexidentity.ProtocolScope
	requiredAuthority      *codexidentity.UpstreamAuthority
	preferredRouteTargetID string
	routeTargetPreference  codexcontinuity.RouteTargetPreference
	visibleRouteTargetID   string
	physicalCandidate      *codexidentity.CandidateSnapshot
	generation             *codexcontinuity.Generation

	cookieBoundary      providerCookieBoundary
	visibilityCommitted bool
	replacementClosed   bool
}

func (r *Runtime) Begin(ctx context.Context, request *http.Request, apiType, operationID string, policy model.ConversationRecoveryPolicy) (*Operation, error) {
	if apiType != codexAPIType {
		return nil, &Failure{Class: FailureProtocol, Stage: "begin", Cause: errors.New("codex WebSocket runtime only accepts Codex traffic")}
	}
	if r == nil {
		return nil, &Failure{Class: FailureStorage, Stage: "begin", Cause: errors.New("codex WebSocket runtime is unavailable")}
	}
	op := &Operation{runtime: r, operationID: operationID, apiType: apiType, recoveryPolicy: model.NormalizeConversationRecoveryPolicy(string(policy))}
	if request == nil {
		return nil, &Failure{Class: FailureProtocol, Stage: "begin", Cause: errors.New("request is required")}
	}
	op.headers = request.Header.Clone()

	if _, err := initialClientEvidence(request.Header); err != nil {
		return nil, err
	}
	if err := r.bindClientScope(ctx, op, request.Header, true); err != nil {
		return nil, err
	}
	op.ledger = codexprovenance.NewLedger(codexprovenance.Config{Resolver: r.continuity, RecoveryPolicy: op.recoveryPolicy, ClientScopeCandidates: op.clientScopes, APIType: apiType, OperationID: operationID})
	if err := op.inspectClientInput(ctx, request.Header, codexheaders.MessageView{}, false); err != nil {
		return nil, err
	}
	if err := r.beginProviderCookies(ctx, op, request); err != nil {
		return nil, err
	}
	return op, nil
}

func initialClientEvidence(headers http.Header) (codexheaders.Result, error) {
	// This pass exists only to decide whether client identity is required. Treat
	// syntactically valid evidence as owned so policy is deferred until the
	// authoritative ResolveOwner pass, including HTTP-to-WS continuity.
	discovery := codexheaders.DecideClient(codexheaders.ClientInput{
		Headers:         headers,
		Owners:          func(codexheaders.BindingCandidate) codexheaders.OwnerStatus { return codexheaders.OwnerCurrent },
		AttestationLock: codexheaders.OperationAuthorityCurrent,
	})
	if discovery.Rejected() {
		return codexheaders.Result{}, protocolFailure("request_headers", discovery)
	}
	return discovery, nil
}

func (r *Runtime) bindClientScope(ctx context.Context, op *Operation, headers http.Header, required bool) error {
	if r == nil || r.clientIdentities == nil {
		return &Failure{Class: FailureStorage, Stage: "client_scope", Cause: errors.New("client scope digester is unavailable")}
	}
	credential := clientcredential.Extract(map[string][]string(headers))
	if credential.State != clientcredential.StateSingle {
		if required {
			return &Failure{Class: FailureIdentity, Stage: "client_scope", Cause: fmt.Errorf("client credential state %s is not usable", credential.State)}
		}
		return nil
	}
	defer credential.Clear()
	resolution, err := r.clientIdentities.Resolve(ctx, credential.Token)
	if err != nil {
		return &Failure{Class: FailureStorage, Stage: "client_scope", Cause: err}
	}
	op.clientIdentity = resolution
	op.currentClientScope = resolution.Primary
	op.clientScopes = append([]codexidentity.ClientScope(nil), resolution.Aliases...)
	op.hasClientScope = true
	return nil
}

func (o *Operation) ClientIdentity() clientidentity.Resolution {
	if o == nil {
		return clientidentity.Resolution{}
	}
	result := o.clientIdentity
	result.Aliases = append([]codexidentity.ClientScope(nil), result.Aliases...)
	return result
}

func (o *Operation) NeedsOwnerBootstrap() bool {
	return false
}

func (o *Operation) ReplacementAllowed() bool {
	if o == nil {
		return true
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return !o.replacementClosed
}

func (o *Operation) closeReplacement() {
	if o == nil {
		return
	}
	o.mu.Lock()
	o.replacementClosed = true
	o.mu.Unlock()
}

func (o *Operation) RequiredAuthority() (*codexidentity.UpstreamAuthority, string) {
	if o == nil {
		return nil, ""
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.requiredAuthority == nil {
		return nil, ""
	}
	authority := *o.requiredAuthority
	return &authority, o.preferredRouteTargetID
}

func (o *Operation) InspectBootstrapFrame(ctx context.Context, text bool, payload []byte) error {
	if o == nil {
		return nil
	}
	if !text {
		return nil
	}
	message := codexheaders.InspectClientFrame(payload)
	return o.inspectClientInput(ctx, o.headers, message, true)
}

func (o *Operation) inspectClientInput(
	ctx context.Context,
	headers http.Header,
	message codexheaders.MessageView,
	requireScope bool,
) error {
	if o == nil || o.runtime == nil || o.runtime.continuity == nil {
		return &Failure{Class: FailureStorage, Stage: "continuity", Cause: errors.New("continuity service is unavailable")}
	}
	discovery := codexheaders.DecideClient(codexheaders.ClientInput{
		Headers: headers, Message: message,
		Owners:          func(codexheaders.BindingCandidate) codexheaders.OwnerStatus { return codexheaders.OwnerUnknown },
		AttestationLock: codexheaders.OperationUnlocked,
	})
	if len(discovery.Decisions()) > 0 && !o.hasClientScope {
		return &Failure{Class: FailureIdentity, Stage: "client_scope", Cause: errors.New("state-bearing frame requires one client credential")}
	}
	owners, err := o.resolveRequestOwners(ctx, discovery)
	if err != nil {
		return err
	}
	if err := o.applyRequiredOwners(owners); err != nil {
		return err
	}
	if err := o.anchorUnresolvedEvidenceToPhysicalCandidate(discovery, owners); err != nil {
		return err
	}
	decision := codexheaders.DecideClient(codexheaders.ClientInput{
		Headers: headers, Message: message,
		Owners:          ownerLookup(owners),
		AttestationLock: codexheaders.OperationUnlocked,
		StateAdmission:  o.stateAdmission(),
	})
	if decision.Rejected() {
		if err := resolvedOwnerFailure("client_input", decision, owners); err != nil {
			return err
		}
		return protocolFailure("client_input", decision)
	}
	if requireScope && o.requiredProtocolScope == nil && len(decision.Decisions()) > 0 {
		// Unknown claimable metadata is useful only after a provider establishes
		// its ProtocolScope; it intentionally does not fabricate an owner now.
		return nil
	}
	return nil
}

func (o *Operation) resolveOwners(ctx context.Context, result codexheaders.Result) (map[[sha256.Size]byte]ownerResolution, error) {
	owners := make(map[[sha256.Size]byte]ownerResolution)
	for _, decision := range result.Decisions() {
		candidate := decision.Candidate()
		if _, persistent := candidate.PersistentNamespace(); !persistent {
			continue
		}
		key := candidateKey(candidate)
		if _, exists := owners[key]; exists {
			continue
		}
		binding, err := o.runtime.continuity.ResolveOwner(ctx, codexcontinuity.ResolveRequest{
			Evidence: evidence(candidate), ClientScopeCandidates: o.clientScopes, OperationID: o.operationID,
		})
		resolution := ownerResolution{binding: binding, err: err}
		switch {
		case err == nil:
			resolution.status = codexheaders.OwnerCurrent
		case codexcontinuity.IsError(err, codexcontinuity.ErrorUnknown):
			resolution.status = codexheaders.OwnerUnknown
		case codexcontinuity.IsError(err, codexcontinuity.ErrorConflict),
			codexcontinuity.IsError(err, codexcontinuity.ErrorExpired):
			resolution.status = codexheaders.OwnerConflict
		case continuityPersistenceUnavailable(err):
			resolution.status = codexheaders.OwnerStoreUnavailable
		default:
			resolution.status = codexheaders.OwnerUnavailable
		}
		owners[key] = resolution
	}
	return owners, nil
}

func continuityPersistenceUnavailable(err error) bool {
	return codexcontinuity.IsError(err, codexcontinuity.ErrorUnavailable) ||
		codexcontinuity.IsError(err, codexcontinuity.ErrorCapacity)
}

func (o *Operation) stateAdmission() codexheaders.StateAdmission {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.requiredProtocolScope != nil {
		return codexheaders.StateAdmissionAnchored
	}
	return codexheaders.StateAdmissionStrict
}

func (o *Operation) anchorUnresolvedEvidenceToPhysicalCandidate(
	result codexheaders.Result,
	owners map[[sha256.Size]byte]ownerResolution,
) error {
	if o.AllowsAccountSwitch() {
		return nil
	}
	needsAnchor := false
	for _, decision := range result.Decisions() {
		resolution, exists := owners[candidateKey(decision.Candidate())]
		if !exists {
			continue
		}
		isUnknownExistingState := resolution.status == codexheaders.OwnerUnknown &&
			(decision.Field() == codexheaders.FieldTurnState || decision.Field() == codexheaders.FieldResponseReference)
		isAuxiliaryStoreOutage := resolution.status == codexheaders.OwnerStoreUnavailable &&
			decision.Field() != codexheaders.FieldTurnState && decision.Field() != codexheaders.FieldResponseReference
		if isAuxiliaryStoreOutage || isUnknownExistingState {
			needsAnchor = true
			break
		}
	}
	if !needsAnchor {
		return nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.requiredProtocolScope != nil || o.physicalCandidate == nil || o.generation == nil {
		return nil
	}
	return o.pinProtocolScopeLocked(o.physicalCandidate.ProtocolScope(), "state_adoption")
}

func (o *Operation) applyRequiredOwners(owners map[[sha256.Size]byte]ownerResolution) error {
	if o.AllowsAccountSwitch() {
		return nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, resolution := range owners {
		if resolution.status != codexheaders.OwnerCurrent {
			continue
		}
		scope := resolution.binding.Owner.ProtocolScope
		if scope.APIType() != o.apiType {
			return &Failure{Class: FailureIdentity, Stage: "continuity_scope", Cause: errors.New("owner API type conflicts with request")}
		}
		if err := o.pinProtocolScopeLocked(scope, "continuity_scope"); err != nil {
			return err
		}
		if o.visibleRouteTargetID == "" {
			o.routeTargetPreference = o.routeTargetPreference.Add(resolution.binding.Owner.RouteTargetHint)
		}
	}
	if o.visibleRouteTargetID == "" {
		if preferred, consistent := o.routeTargetPreference.Value(); consistent {
			o.preferredRouteTargetID = preferred
		} else {
			o.preferredRouteTargetID = ""
		}
	}
	return nil
}

func (o *Operation) pinProtocolScopeLocked(scope codexidentity.ProtocolScope, stage string) error {
	if o.requiredProtocolScope != nil && !o.requiredProtocolScope.Equal(scope) {
		return &Failure{Class: FailureIdentity, Stage: stage, Cause: errors.New("evidence belongs to multiple protocol scopes")}
	}
	authority := scope.Authority()
	if o.requiredAuthority != nil && !o.requiredAuthority.Equal(authority) {
		return &Failure{Class: FailureIdentity, Stage: stage, Cause: errors.New("protocol scope conflicts with required authority")}
	}
	copyScope := scope
	o.requiredProtocolScope = &copyScope
	o.requiredAuthority = &authority
	return nil
}

func candidateKey(candidate codexheaders.BindingCandidate) [sha256.Size]byte {
	return sha256.Sum256(candidate.DigestInput())
}

func evidence(candidate codexheaders.BindingCandidate) codexcontinuity.Evidence {
	return codexcontinuity.Evidence{Kind: continuityKind(candidate.Field()), DigestInput: candidate.DigestInput()}
}

func continuityKind(field codexheaders.Field) codexcontinuity.Kind {
	switch field {
	case codexheaders.FieldThreadID:
		return codexcontinuity.KindThreadID
	case codexheaders.FieldSessionID:
		return codexcontinuity.KindSessionID
	case codexheaders.FieldConversationID:
		return codexcontinuity.KindConversationID
	case codexheaders.FieldWindowID:
		return codexcontinuity.KindWindowID
	case codexheaders.FieldTurnState:
		return codexcontinuity.KindTurnState
	case codexheaders.FieldTurnMetadata:
		return codexcontinuity.KindTurnMetadata
	case codexheaders.FieldResponseReference:
		return codexcontinuity.KindResponseReference
	default:
		return ""
	}
}

func ownerLookup(owners map[[sha256.Size]byte]ownerResolution) codexheaders.OwnerLookup {
	return func(candidate codexheaders.BindingCandidate) codexheaders.OwnerStatus {
		if resolution, exists := owners[candidateKey(candidate)]; exists {
			return resolution.status
		}
		return codexheaders.OwnerUnavailable
	}
}

func resolvedOwnerFailure(
	stage string,
	result codexheaders.Result,
	owners map[[sha256.Size]byte]ownerResolution,
) error {
	for _, decision := range result.Decisions() {
		if decision.Action() != codexheaders.ActionReject {
			continue
		}
		resolution, exists := owners[candidateKey(decision.Candidate())]
		if exists && resolution.err != nil {
			return continuityFailure(stage, resolution.err)
		}
	}
	return nil
}
