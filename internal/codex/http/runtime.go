// Package codexhttp composes the Codex security deep modules at the HTTP
// attempt boundary. It owns no persistence or protocol parsing rules.
package codexhttp

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/doraemonkeys/switch-a/internal/codex/clientcredential"
	"github.com/doraemonkeys/switch-a/internal/codex/clientidentity"
	"github.com/doraemonkeys/switch-a/internal/codex/continuity"
	"github.com/doraemonkeys/switch-a/internal/codex/cookie"
	"github.com/doraemonkeys/switch-a/internal/codex/headers"
	"github.com/doraemonkeys/switch-a/internal/codex/identity"
	"github.com/doraemonkeys/switch-a/internal/codex/provenance"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/upstreamtransport"
)

const codexAPIType = "codex"

type ClientIdentityResolver interface {
	Resolve(context.Context, []byte) (clientidentity.Resolution, error)
}

type Continuity interface {
	Resolve(context.Context, codexcontinuity.ResolveRequest) (codexcontinuity.Resolution, error)
	AcquireExisting(context.Context, codexcontinuity.ValidateRequest) (codexcontinuity.Lease, error)
	Claim(context.Context, codexcontinuity.ClaimRequest) (codexcontinuity.Lease, error)
	Adopt(context.Context, codexcontinuity.ClaimRequest) (codexcontinuity.Lease, error)
	PrepareVisible(context.Context, codexcontinuity.ClaimRequest) (codexcontinuity.Lease, error)
	Commit(context.Context, codexcontinuity.Lease) (codexcontinuity.Binding, error)
	AbandonBeforeDisclosure(context.Context, codexcontinuity.Lease) error
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
		return nil, fmt.Errorf("initialize Codex HTTP runtime: client identities, continuity, provider cookies, and external scheme are required")
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

type Operation struct {
	runtime        *Runtime
	operationID    string
	apiType        string
	recoveryPolicy model.ConversationRecoveryPolicy
	provenance     *codexprovenance.Ledger

	currentClientScope codexidentity.ClientScope
	clientIdentity     clientidentity.Resolution
	clientScopes       []codexidentity.ClientScope
	hasClientScope     bool
	clientDecision     codexheaders.Result
	owners             map[[sha256.Size]byte]ownerResolution

	mu                     sync.Mutex
	requiredProtocolScope  *codexidentity.ProtocolScope
	requiredAuthority      *codexidentity.UpstreamAuthority
	preferredRouteTargetID string
	visibleRouteTargetID   string
	requestClaimsCommitted bool

	cookieRequest       *providercookie.Request
	lastCookieAuthority *codexidentity.CookieAuthority
	gatewaySetCookie    string
	cookieClosed        bool
}

// RequiresClientEvidence expresses the continuity consumer's admission dependency.
// Header claims cannot rule out conflicting or state-bearing body claims.
func (r *Runtime) RequiresClientEvidence(apiType string, hasBody bool, requestPath string) bool {
	if apiType != codexAPIType || !hasBody {
		return false
	}
	path := strings.TrimPrefix(requestPath, "/codex")
	path = strings.TrimPrefix(path, "/v1")
	// Only response contracts carry conversation ownership. A search or models
	// payload cannot introduce an owner late in its business data.
	return path == "/responses" || path == "/responses/compact"
}

func IsJSONContentType(contentType string) bool {
	media, _, err := mime.ParseMediaType(contentType)
	return err == nil && (media == "application/json" || strings.HasSuffix(media, "+json"))
}

// RequestUsesJSON keeps opaque extension bodies outside the JSON protocol
// converter, including nonconversation endpoints with an explicit media type.
func RequestUsesJSON(request *http.Request) bool {
	if value := request.Header.Get("Content-Type"); value != "" {
		return IsJSONContentType(value)
	}
	path := strings.TrimPrefix(strings.TrimPrefix(request.URL.EscapedPath(), "/codex"), "/v1")
	return path == "/responses" || path == "/responses/compact" || path == "/alpha/search"
}

func (r *Runtime) Begin(
	ctx context.Context,
	request *http.Request,
	apiType string,
	operationID string,
	recoveryPolicy model.ConversationRecoveryPolicy,
	evidence codexheaders.ClientEvidence,
) (*Operation, error) {
	var identity clientidentity.Resolution
	if apiType == codexAPIType {
		if request == nil {
			return nil, clientError("begin", errors.New("request is required"))
		}
		var err error
		identity, err = r.ResolveClientIdentity(ctx, request.Header)
		if err != nil {
			return nil, err
		}
	}
	return r.BeginResolved(ctx, request, apiType, operationID, recoveryPolicy, evidence, identity)
}

// BeginResolved consumes the identity frozen at ingress, while body-dependent
// ownership waits separately for the conversation contract's semantic facts.
func (r *Runtime) BeginResolved(ctx context.Context, request *http.Request, apiType, operationID string, recoveryPolicy model.ConversationRecoveryPolicy, evidence codexheaders.ClientEvidence, identity clientidentity.Resolution) (*Operation, error) {
	op := &Operation{runtime: r, operationID: operationID, apiType: apiType,
		recoveryPolicy: model.NormalizeConversationRecoveryPolicy(string(recoveryPolicy))}
	if apiType != codexAPIType {
		return op, nil
	}
	if r == nil {
		return nil, dependencyError("begin", errors.New("codex HTTP runtime is unavailable"))
	}
	if request == nil {
		return nil, clientError("begin", errors.New("request is required"))
	}
	message, discovery := discoverClientEvidence(request.Header, evidence)
	identity.Aliases = append([]codexidentity.ClientScope(nil), identity.Aliases...)
	op.clientIdentity = identity
	op.currentClientScope = identity.Primary
	op.clientScopes = append([]codexidentity.ClientScope(nil), identity.Aliases...)
	op.hasClientScope = true
	if err := op.beginContinuity(ctx, request.Header, message, discovery); err != nil {
		return nil, err
	}
	if err := op.beginProviderCookies(ctx, request); err != nil {
		return nil, err
	}
	return op, nil
}

func discoverClientEvidence(headers http.Header, message codexheaders.ClientEvidence) (codexheaders.ClientEvidence, codexheaders.Result) {
	discovery := codexheaders.DecideClientEvidence(codexheaders.ClientEvidenceInput{
		Headers: headers, Evidence: message,
		Owners: func(codexheaders.BindingCandidate) codexheaders.OwnerStatus {
			return codexheaders.OwnerUnknown
		},
		AttestationLock: codexheaders.OperationUnlocked,
	})
	return message, discovery
}

func (r *Runtime) ResolveClientIdentity(ctx context.Context, headers http.Header) (clientidentity.Resolution, error) {
	if r == nil || r.clientIdentities == nil {
		return clientidentity.Resolution{}, dependencyError("client_scope", errors.New("client identity resolver is unavailable"))
	}
	credential := clientcredential.Extract(map[string][]string(headers))
	if credential.State != clientcredential.StateSingle {
		return clientidentity.Resolution{}, clientError("client_scope", fmt.Errorf("client credential is not a single canonical value (state %s)", credential.State))
	}
	defer credential.Clear()
	resolution, err := r.clientIdentities.Resolve(ctx, credential.Token)
	if err != nil {
		return clientidentity.Resolution{}, dependencyError("client_scope", err)
	}
	return resolution, nil
}

func (o *Operation) ClientIdentity() clientidentity.Resolution {
	if o == nil {
		return clientidentity.Resolution{}
	}
	result := o.clientIdentity
	result.Aliases = append([]codexidentity.ClientScope(nil), result.Aliases...)
	return result
}

func (o *Operation) beginContinuity(
	ctx context.Context,
	headers http.Header,
	message codexheaders.ClientEvidence,
	discovery codexheaders.Result,
) error {
	if o.runtime.continuity == nil {
		return dependencyError("continuity", errors.New("continuity service is unavailable"))
	}
	if len(discovery.Decisions()) > 0 && !o.hasClientScope {
		return clientError("client_scope", errors.New("state-bearing request requires one client credential"))
	}
	return o.resolveClientDecision(ctx, headers, message, discovery)
}

func (o *Operation) beginProviderCookies(ctx context.Context, request *http.Request) error {
	if o.runtime.providerCookies == nil || o.runtime.externalScheme == nil {
		return dependencyError("provider_cookie", errors.New("provider cookie capability is unavailable"))
	}
	scheme, err := o.runtime.externalScheme.ResolveExternalScheme(request)
	if err != nil {
		return dependencyError("external_scheme", err)
	}
	cookieOperationID, err := providercookie.NewOperationID(o.operationID)
	if err != nil {
		return dependencyError("provider_cookie", err)
	}
	access, err := o.runtime.providerCookies.ResolveJar(ctx, cookieOperationID, gatewayHandle(request), o.clientScopes)
	if err != nil {
		return dependencyError("provider_cookie", err)
	}
	o.cookieRequest, err = o.runtime.providerCookies.BeginRequest(cookieOperationID, access)
	if err != nil {
		return dependencyError("provider_cookie", err)
	}
	if !access.Issued() && !access.Refresh() {
		return nil
	}
	handle, err := providercookie.NewGatewayHandleCookie(access.HandleValue(), scheme)
	if err != nil {
		return dependencyError("provider_cookie", err)
	}
	o.gatewaySetCookie, err = handle.HeaderValue()
	if err != nil {
		return dependencyError("provider_cookie", err)
	}
	return nil
}

func (o *Operation) RequestPolicy() upstreamtransport.RequestPolicy {
	if o != nil && o.apiType == codexAPIType {
		return upstreamtransport.RequestPolicy{Cookies: upstreamtransport.ServerManagedCookies}
	}
	return upstreamtransport.RequestPolicy{Headers: upstreamtransport.PreserveClientHeaders}
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

func (o *Operation) resolveClientDecision(
	ctx context.Context,
	headers http.Header,
	message codexheaders.ClientEvidence,
	discovery codexheaders.Result,
) error {
	o.provenance = codexprovenance.NewLedger(codexprovenance.Config{
		Resolver: o.runtime.continuity, RecoveryPolicy: o.recoveryPolicy,
		ClientScopeCandidates: o.clientScopes, APIType: o.apiType, OperationID: o.operationID,
	})
	o.resolveClientOwners(ctx, discovery)
	if err := o.applyResolvedOwnerConstraints(); err != nil {
		return err
	}
	admission := codexheaders.StateAdmissionStrict
	if o.requiredProtocolScope != nil {
		admission = codexheaders.StateAdmissionAnchored
	}
	o.clientDecision = codexheaders.DecideClientEvidence(codexheaders.ClientEvidenceInput{
		Headers: headers, Evidence: message,
		Owners:          o.resolvedOwnerStatus,
		AttestationLock: codexheaders.OperationUnlocked,
		StateAdmission:  admission,
	})
	if err := o.clientDecisionError(); err != nil {
		return err
	}
	return nil
}

func (o *Operation) resolveClientOwners(ctx context.Context, discovery codexheaders.Result) {
	o.owners = make(map[[sha256.Size]byte]ownerResolution)
	for _, decision := range discovery.Decisions() {
		candidate := decision.Candidate()
		if _, persistent := candidate.PersistentNamespace(); !persistent {
			continue
		}
		key := candidateKey(candidate)
		if _, exists := o.owners[key]; exists {
			continue
		}
		resolution, err := o.provenance.ObserveRequest(ctx, evidence(candidate))
		o.owners[key] = o.classifyProvenanceResolution(resolution, err)
	}
}

func classifyOwnerResolution(binding codexcontinuity.Binding, err error) ownerResolution {
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
	return resolution
}

func continuityPersistenceUnavailable(err error) bool {
	return codexcontinuity.IsError(err, codexcontinuity.ErrorUnavailable) ||
		codexcontinuity.IsError(err, codexcontinuity.ErrorCapacity)
}

func (o *Operation) resolvedOwnerStatus(candidate codexheaders.BindingCandidate) codexheaders.OwnerStatus {
	if resolution, exists := o.owners[candidateKey(candidate)]; exists {
		return resolution.status
	}
	return codexheaders.OwnerUnavailable
}

func (o *Operation) clientDecisionError() error {
	if !o.clientDecision.Rejected() {
		return nil
	}
	for _, decision := range o.clientDecision.Decisions() {
		switch decision.Reason() {
		case codexheaders.ReasonOwnerUnavailable, codexheaders.ReasonOwnerUnknown, codexheaders.ReasonOwnerConflict:
			resolution, exists := o.owners[candidateKey(decision.Candidate())]
			if !exists || resolution.err == nil {
				continue
			}
			if decision.Reason() == codexheaders.ReasonOwnerUnavailable {
				return dependencyError("continuity_owner", resolution.err)
			}
			// The recovery adapter classifies the exact continuity cause. Keeping
			// it below the HTTP error preserves unknown, expired, and conflict.
			return clientError("continuity_owner", resolution.err)
		}
	}
	return clientError("continuity_owner", decisionError(o.clientDecision))
}

func (o *Operation) applyResolvedOwnerConstraints() error {
	if o.allowsAccountSwitch() {
		return nil
	}
	var routePreference codexcontinuity.RouteTargetPreference
	for _, resolution := range o.owners {
		if resolution.status != codexheaders.OwnerCurrent {
			continue
		}
		scope := resolution.binding.Owner.ProtocolScope
		if scope.APIType() != o.apiType {
			return clientError("continuity_scope", errors.New("owner protocol API type conflicts with this request"))
		}
		if o.requiredProtocolScope != nil && !o.requiredProtocolScope.Equal(scope) {
			return clientError("continuity_scope", errors.New("request evidence belongs to multiple protocol scopes"))
		}
		copyScope := scope
		o.requiredProtocolScope = &copyScope
		authority := scope.Authority()
		o.requiredAuthority = &authority
		routePreference = routePreference.Add(resolution.binding.Owner.RouteTargetHint)
	}
	if preferred, consistent := routePreference.Value(); consistent {
		o.preferredRouteTargetID = preferred
	} else {
		o.preferredRouteTargetID = ""
	}
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

func decisionError(result codexheaders.Result) error {
	for _, decision := range result.Decisions() {
		if decision.Action() == codexheaders.ActionReject {
			return fmt.Errorf("%s rejected: %s", decision.Field(), decision.Reason())
		}
	}
	return errors.New("codex request was rejected")
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
