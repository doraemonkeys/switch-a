package codexws

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"

	"github.com/doraemonkeys/switch-a/internal/codex/clientcredential"
	"github.com/doraemonkeys/switch-a/internal/codex/continuity"
	"github.com/doraemonkeys/switch-a/internal/codex/cookie"
	"github.com/doraemonkeys/switch-a/internal/codex/headers"
	"github.com/doraemonkeys/switch-a/internal/codex/identity"
	"github.com/doraemonkeys/switch-a/internal/codex/startup"
)

const codexAPIType = "codex"

type FeatureSource interface {
	Snapshot() codexstartup.Snapshot
}

type FeatureSourceFunc func() codexstartup.Snapshot

func (f FeatureSourceFunc) Snapshot() codexstartup.Snapshot { return f() }

type ClientScopeDigester interface {
	ClientScope([]byte) (codexidentity.ClientScope, error)
	ClientScopeCandidates([]byte) ([]codexidentity.ClientScope, error)
}

type Continuity interface {
	ResolveOwner(context.Context, codexcontinuity.ResolveRequest) (codexcontinuity.Binding, error)
	AcquireExisting(context.Context, codexcontinuity.ValidateRequest) (codexcontinuity.Lease, error)
	Claim(context.Context, codexcontinuity.ClaimRequest) (codexcontinuity.Lease, error)
	PrepareVisible(context.Context, codexcontinuity.ClaimRequest) (codexcontinuity.Lease, error)
	Commit(context.Context, codexcontinuity.Lease) (codexcontinuity.Binding, error)
	AbandonBeforeDisclosure(context.Context, codexcontinuity.Lease) error
	OpenConnection(string, codexidentity.ProtocolScope) (codexcontinuity.Generation, error)
	ActivateResponse(codexcontinuity.Generation, codexcontinuity.Lease) error
	DeactivateResponse(codexcontinuity.Generation, codexcontinuity.Binding) error
	CloseConnection(codexcontinuity.Generation)
	ValidateInject(context.Context, codexcontinuity.ValidateRequest, codexcontinuity.Generation) (codexcontinuity.Binding, error)
}

type ProviderCookies interface {
	ResolveJar(context.Context, providercookie.OperationID, string, []codexidentity.ClientScope) (providercookie.JarAccess, error)
	BeginRequest(providercookie.OperationID, providercookie.JarAccess) (*providercookie.Request, error)
}

type ExternalSchemeResolver interface {
	ResolveExternalScheme(*http.Request) (providercookie.ResolvedExternalScheme, error)
}

type Config struct {
	Features        FeatureSource
	ClientScopes    ClientScopeDigester
	Continuity      Continuity
	ProviderCookies ProviderCookies
	ExternalScheme  ExternalSchemeResolver
}

type Runtime struct {
	features        FeatureSource
	clientScopes    ClientScopeDigester
	continuity      Continuity
	providerCookies ProviderCookies
	externalScheme  ExternalSchemeResolver
}

func New(config Config) *Runtime {
	return &Runtime{
		features: config.Features, clientScopes: config.ClientScopes,
		continuity: config.Continuity, providerCookies: config.ProviderCookies,
		externalScheme: config.ExternalScheme,
	}
}

type ownerResolution struct {
	status  codexheaders.OwnerStatus
	binding codexcontinuity.Binding
	err     error
}

// Operation is request-local. Its mutex protects the two relay directions,
// which may validate and commit state concurrently after the handshake.
type Operation struct {
	runtime     *Runtime
	features    codexstartup.Snapshot
	operationID string
	apiType     string
	headers     http.Header

	currentClientScope codexidentity.ClientScope
	clientScopes       []codexidentity.ClientScope
	hasClientScope     bool

	mu                     sync.Mutex
	requiredProtocolScope  *codexidentity.ProtocolScope
	requiredAuthority      *codexidentity.UpstreamAuthority
	preferredRouteTargetID string
	visibleRouteTargetID   string
	physicalCandidate      *codexidentity.CandidateSnapshot
	generation             *codexcontinuity.Generation

	cookieRequest       *providercookie.Request
	lastCookieAuthority *codexidentity.CookieAuthority
	gatewaySetCookie    string
	cookieClosed        bool
}

func (r *Runtime) Begin(ctx context.Context, request *http.Request, apiType, operationID string) (*Operation, error) {
	features := r.featureSnapshot()
	op := &Operation{runtime: r, features: features, operationID: operationID, apiType: apiType}
	if apiType != codexAPIType || (!features.Continuity && !features.ProviderCookieJar) {
		return op, nil
	}
	if request == nil {
		return nil, &Failure{Class: FailureProtocol, Stage: "begin", Cause: errors.New("request is required")}
	}
	op.headers = request.Header.Clone()

	discovery, err := initialClientEvidence(request.Header, features.Continuity)
	if err != nil {
		return nil, err
	}
	if err := r.bindClientScope(op, request.Header, features.ProviderCookieJar || len(discovery.Decisions()) > 0); err != nil {
		return nil, err
	}
	if features.Continuity {
		if r.continuity == nil {
			return nil, &Failure{Class: FailureStorage, Stage: "continuity", Cause: errors.New("continuity service is unavailable")}
		}
		if err := op.inspectClientInput(ctx, request.Header, codexheaders.MessageView{}, false); err != nil {
			return nil, err
		}
	}
	if features.ProviderCookieJar {
		if err := r.beginProviderCookies(ctx, op, request); err != nil {
			return nil, err
		}
	}
	return op, nil
}

func (r *Runtime) featureSnapshot() codexstartup.Snapshot {
	if r == nil || r.features == nil {
		return codexstartup.Snapshot{}
	}
	return r.features.Snapshot()
}

func initialClientEvidence(headers http.Header, enabled bool) (codexheaders.Result, error) {
	if !enabled {
		return codexheaders.Result{}, nil
	}
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

func (r *Runtime) bindClientScope(op *Operation, headers http.Header, required bool) error {
	if r == nil || r.clientScopes == nil {
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
	current, err := r.clientScopes.ClientScope(credential.Token)
	if err != nil {
		return &Failure{Class: FailureStorage, Stage: "client_scope", Cause: err}
	}
	candidates, err := r.clientScopes.ClientScopeCandidates(credential.Token)
	if err != nil {
		return &Failure{Class: FailureStorage, Stage: "client_scope", Cause: err}
	}
	op.currentClientScope = current
	op.clientScopes = append([]codexidentity.ClientScope(nil), candidates...)
	op.hasClientScope = true
	return nil
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
	op.cookieRequest, err = r.providerCookies.BeginRequest(cookieOperationID, access)
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
	op.gatewaySetCookie, err = handle.HeaderValue()
	if err != nil {
		return cookieFailure("gateway_cookie", err)
	}
	return nil
}

func (o *Operation) Features() codexstartup.Snapshot {
	if o == nil {
		return codexstartup.Snapshot{}
	}
	return o.features
}

func (o *Operation) GatewaySetCookie() string {
	if o == nil {
		return ""
	}
	return o.gatewaySetCookie
}

func (o *Operation) NeedsOwnerBootstrap() bool {
	if o == nil || !o.features.Continuity {
		return false
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.requiredProtocolScope == nil
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
	if o == nil || !o.features.Continuity {
		return nil
	}
	if !text {
		return &Failure{Class: FailureProtocol, Stage: "bootstrap_frame", Cause: errors.New("owner bootstrap requires a text frame")}
	}
	message := codexheaders.InspectClientFrame(codexheaders.FixtureCodexDesktop0150Alpha8, payload)
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
	owners, err := o.resolveOwners(ctx, discovery)
	if err != nil {
		return err
	}
	decision := codexheaders.DecideClient(codexheaders.ClientInput{
		Headers: headers, Message: message,
		Owners:          ownerLookup(owners),
		AttestationLock: codexheaders.OperationUnlocked,
	})
	if decision.Rejected() {
		return protocolFailure("client_input", decision)
	}
	if err := o.applyRequiredOwners(owners); err != nil {
		return err
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
		default:
			return nil, continuityFailure("resolve_owner", err)
		}
		owners[key] = resolution
	}
	return owners, nil
}

func (o *Operation) applyRequiredOwners(owners map[[sha256.Size]byte]ownerResolution) error {
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
		if o.preferredRouteTargetID == "" {
			o.preferredRouteTargetID = resolution.binding.Owner.RouteTargetHint
		} else if o.preferredRouteTargetID != resolution.binding.Owner.RouteTargetHint {
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

func cloneURL(source *url.URL) *url.URL {
	if source == nil {
		return nil
	}
	copyURL := *source
	return &copyURL
}
