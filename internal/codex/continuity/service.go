package codexcontinuity

import (
	"context"
	"fmt"
	"reflect"

	"github.com/doraemonkeys/switch-a/internal/codex/identity"
)

type Config struct {
	Store         Store
	Digester      OpaqueDigester
	Policy        Policy
	Clock         Clock
	Observer      Observer
	GenerationIDs GenerationIDSource
}

type Service struct {
	store       Store
	digester    OpaqueDigester
	policy      Policy
	clock       Clock
	observer    Observer
	generations *generationRegistry
	provenance  *provenanceStore
}

func NewService(config Config) (*Service, error) {
	if nilInterface(config.Store) || nilInterface(config.Digester) {
		return nil, errorOf(ErrorInvalidInput, "", "", "store and opaque digester are required", nil)
	}
	if len(config.Policy.limits) != len(allKinds) {
		return nil, errorOf(ErrorInvalidInput, "", "", "a complete continuity policy is required", nil)
	}
	if config.Clock == nil {
		config.Clock = realClock{}
	}
	service := &Service{
		store:    config.Store,
		digester: config.Digester,
		policy:   config.Policy,
		clock:    config.Clock,
		observer: config.Observer,
	}
	service.generations = newGenerationRegistry(config.GenerationIDs, config.Clock, config.Observer)
	service.provenance = newProvenanceStore()
	return service, nil
}

func nilInterface(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

func (s *Service) Claim(ctx context.Context, request ClaimRequest) (Lease, error) {
	if !request.Evidence.Kind.ClientClaimable() {
		return Lease{}, errorOf(
			ErrorInvalidTransition,
			request.Evidence.Kind,
			request.OperationID,
			"unknown turn state and response references cannot be claimed from client input",
			nil,
		)
	}
	return s.claim(ctx, request, false)
}

// Adopt reserves previously issued state only after the transport has derived
// an independent ProtocolScope anchor. Keeping adoption distinct from Claim
// prevents ordinary first-provider selection from legitimizing arbitrary
// client-supplied state.
func (s *Service) Adopt(ctx context.Context, request ClaimRequest) (Lease, error) {
	if request.Evidence.Kind != KindTurnState && request.Evidence.Kind != KindResponseReference {
		return Lease{}, errorOf(
			ErrorInvalidTransition,
			request.Evidence.Kind,
			request.OperationID,
			"only existing turn state and response references can be adopted",
			nil,
		)
	}
	return s.claim(ctx, request, false)
}

func (s *Service) PrepareVisible(ctx context.Context, request ClaimRequest) (Lease, error) {
	if !request.Evidence.Kind.ResponseIssuable() {
		return Lease{}, errorOf(
			ErrorInvalidTransition,
			request.Evidence.Kind,
			request.OperationID,
			"only upstream-issued turn state and response references can be prepared for visibility",
			nil,
		)
	}
	return s.claim(ctx, request, true)
}

func (s *Service) claim(ctx context.Context, request ClaimRequest, allowProvenanceFallback bool) (Lease, error) {
	prepared, err := s.prepareClaim(request)
	if err != nil {
		return Lease{}, err
	}
	result, err := s.store.Claim(ctx, prepared)
	if err != nil {
		s.emit("claim", "unavailable", request.OperationID, Binding{})
		if allowProvenanceFallback {
			return s.claimProvenance(ctx, prepared, "durable_unavailable")
		}
		return Lease{}, unavailable(request.Evidence.Kind, request.OperationID, "claim", err)
	}
	if result.Decision == StoreCapacity && allowProvenanceFallback {
		s.emit("claim", string(result.Decision), request.OperationID, result.Binding)
		return s.claimProvenance(ctx, prepared, "durable_capacity")
	}
	if result.Decision != StoreClaimed && result.Decision != StoreOwned {
		s.emit("claim", string(result.Decision), request.OperationID, result.Binding)
		return Lease{}, decisionError(result.Decision, request.Evidence.Kind, request.OperationID)
	}
	if err := s.rememberProvenance(prepared, result.Binding, request.OperationID); err != nil {
		return Lease{}, err
	}
	s.emit("claim", string(result.Decision), request.OperationID, result.Binding)
	return Lease{binding: result.Binding, created: result.Decision == StoreClaimed, origin: leaseOriginDurable}, nil
}

func (s *Service) claimProvenance(ctx context.Context, prepared StoreClaim, cause string) (Lease, error) {
	result, _ := s.provenance.Claim(ctx, prepared)
	if result.Decision != StoreClaimed && result.Decision != StoreOwned {
		s.emit("provenance_claim", string(result.Decision), prepared.OperationID, result.Binding)
		return Lease{}, decisionError(result.Decision, prepared.Kind, prepared.OperationID)
	}
	s.emit("provenance_claim", cause, prepared.OperationID, result.Binding)
	return Lease{binding: result.Binding, created: result.Decision == StoreClaimed, origin: leaseOriginProvenance}, nil
}

func (s *Service) rememberProvenance(command StoreClaim, binding Binding, operationID string) error {
	result := s.provenance.remember(command, binding)
	switch result.Decision {
	case StoreOwned:
		s.emit("provenance_remember", "owned", operationID, result.Binding)
		return nil
	case StoreCapacity:
		// Durable ownership remains authoritative; cache pressure only reduces
		// the scope of outage recovery and must not replace a valid request.
		s.emit("provenance_remember", "capacity", operationID, binding)
		return nil
	default:
		s.emit("provenance_remember", string(result.Decision), operationID, result.Binding)
		return decisionError(result.Decision, binding.Kind, operationID)
	}
}

func (s *Service) ResolveOwner(ctx context.Context, request ResolveRequest) (Binding, error) {
	lookup, err := s.prepareLookup(
		request.Evidence,
		request.ClientScopeCandidates,
		nil,
		request.OperationID,
	)
	if err != nil {
		return Binding{}, err
	}
	resolved, err := s.lookup(ctx, "resolve", lookup)
	if err != nil {
		return Binding{}, err
	}
	return resolved.binding, nil
}

func (s *Service) Validate(ctx context.Context, request ValidateRequest) (Binding, error) {
	lease, err := s.acquireExisting(ctx, "validate", request)
	if err != nil {
		return Binding{}, err
	}
	return lease.binding, nil
}

// AcquireExisting returns commit authority only after the durable store proves
// that the value already belongs to the requested client and protocol scopes.
// It never enters Claim, so client-supplied Turn State and response references
// cannot turn an unknown value into an owner as a side effect of finalization.
func (s *Service) AcquireExisting(ctx context.Context, request ValidateRequest) (Lease, error) {
	return s.acquireExisting(ctx, "acquire_existing", request)
}

func (s *Service) acquireExisting(ctx context.Context, action string, request ValidateRequest) (Lease, error) {
	if _, err := request.ProtocolScope.MarshalBinary(); err != nil {
		return Lease{}, errorOf(ErrorInvalidInput, request.Evidence.Kind, request.OperationID, "protocol scope is invalid", err)
	}
	lookup, err := s.prepareLookup(
		request.Evidence,
		request.ClientScopeCandidates,
		&request.ProtocolScope,
		request.OperationID,
	)
	if err != nil {
		return Lease{}, err
	}
	resolved, err := s.lookup(ctx, action, lookup)
	if err != nil {
		return Lease{}, err
	}
	return Lease{binding: resolved.binding, origin: resolved.origin}, nil
}

type resolvedBinding struct {
	binding Binding
	origin  leaseOrigin
}

func (s *Service) lookup(ctx context.Context, action string, lookup StoreLookup) (resolvedBinding, error) {
	result, err := s.store.Lookup(ctx, lookup)
	if err != nil {
		s.emit(action, "unavailable", lookup.OperationID, Binding{})
		return s.lookupProvenance(ctx, action, lookup, false, unavailable(lookup.Kind, lookup.OperationID, action, err))
	}
	if result.Decision == StoreUnknown {
		s.emit(action, string(result.Decision), lookup.OperationID, result.Binding)
		return s.lookupProvenance(ctx, action, lookup, true, decisionError(result.Decision, lookup.Kind, lookup.OperationID))
	}
	if result.Decision != StoreOwned {
		s.emit(action, string(result.Decision), lookup.OperationID, result.Binding)
		return resolvedBinding{binding: result.Binding}, decisionError(result.Decision, lookup.Kind, lookup.OperationID)
	}
	mirror := StoreClaim{
		Kind: lookup.Kind, CurrentDigest: result.Binding.Digest,
		DigestCandidates: lookup.DigestCandidates, Owner: result.Binding.Owner,
		ClientScopeCandidates: lookup.ClientScopeCandidates, OperationID: lookup.OperationID,
		Now: lookup.Now, Limits: lookup.Limits,
	}
	if err := s.rememberProvenance(mirror, result.Binding, lookup.OperationID); err != nil {
		return resolvedBinding{}, err
	}
	s.emit(action, "owned", lookup.OperationID, result.Binding)
	return resolvedBinding{binding: result.Binding, origin: leaseOriginDurable}, nil
}

func (s *Service) lookupProvenance(
	ctx context.Context,
	action string,
	lookup StoreLookup,
	durableUnknown bool,
	durableErr error,
) (resolvedBinding, error) {
	result, _ := s.provenance.Lookup(ctx, lookup)
	if result.Decision == StoreUnknown {
		s.emit("provenance_lookup", "unknown", lookup.OperationID, Binding{})
		return resolvedBinding{}, durableErr
	}
	if result.Decision != StoreOwned {
		s.emit("provenance_lookup", string(result.Decision), lookup.OperationID, result.Binding)
		return resolvedBinding{binding: result.Binding}, decisionError(result.Decision, lookup.Kind, lookup.OperationID)
	}
	if durableUnknown {
		return s.restoreDurableProvenance(ctx, action, lookup, result.Binding)
	}
	s.emit("provenance_lookup", action+"_owned", lookup.OperationID, result.Binding)
	return resolvedBinding{binding: result.Binding, origin: leaseOriginProvenance}, nil
}

func (s *Service) restoreDurableProvenance(
	ctx context.Context,
	action string,
	lookup StoreLookup,
	binding Binding,
) (resolvedBinding, error) {
	command := StoreClaim{
		Kind: binding.Kind, CurrentDigest: binding.Digest,
		DigestCandidates: lookup.DigestCandidates, Owner: binding.Owner,
		ClientScopeCandidates: lookup.ClientScopeCandidates,
		OperationID:           binding.ClaimOperationID, Now: lookup.Now, Limits: lookup.Limits,
	}
	result, err := s.store.Claim(ctx, command)
	if err != nil {
		s.emit("provenance_restore", "unavailable", lookup.OperationID, binding)
		return resolvedBinding{binding: binding, origin: leaseOriginProvenance}, nil
	}
	if result.Decision == StoreCapacity {
		s.emit("provenance_restore", "capacity", lookup.OperationID, binding)
		return resolvedBinding{binding: binding, origin: leaseOriginProvenance}, nil
	}
	if result.Decision != StoreClaimed && result.Decision != StoreOwned {
		s.emit("provenance_restore", string(result.Decision), lookup.OperationID, result.Binding)
		return resolvedBinding{binding: result.Binding}, decisionError(result.Decision, lookup.Kind, lookup.OperationID)
	}
	if err := s.rememberProvenance(command, result.Binding, lookup.OperationID); err != nil {
		return resolvedBinding{}, err
	}
	s.emit("provenance_restore", action+"_"+string(result.Decision), lookup.OperationID, result.Binding)
	return resolvedBinding{binding: result.Binding, origin: leaseOriginDurable}, nil
}

func (s *Service) Commit(ctx context.Context, lease Lease) (Binding, error) {
	if err := validateLease(lease); err != nil {
		return Binding{}, err
	}
	limits, _ := s.policy.Limits(lease.binding.Kind)
	command := StoreCommit{
		Binding: lease.binding,
		Now:     s.clock.Now().UTC(),
		Limits:  limits,
	}
	if lease.origin == leaseOriginProvenance {
		return s.commitProvenance(ctx, command, "provenance")
	}
	result, err := s.store.Commit(ctx, command)
	if err != nil {
		s.emit("commit", "unavailable", lease.binding.ClaimOperationID, lease.binding)
		durableErr := unavailable(lease.binding.Kind, lease.binding.ClaimOperationID, "commit", err)
		binding, provenanceErr := s.commitProvenance(ctx, command, "durable_unavailable")
		if provenanceErr == nil {
			return binding, nil
		}
		if IsError(provenanceErr, ErrorUnknown) {
			return Binding{}, durableErr
		}
		return Binding{}, provenanceErr
	}
	if result.Decision == StoreUnknown {
		return s.commitMissingDurableBinding(ctx, command)
	}
	if result.Decision != StoreCommitted {
		s.emit("commit", string(result.Decision), lease.binding.ClaimOperationID, result.Binding)
		return Binding{}, decisionError(result.Decision, lease.binding.Kind, lease.binding.ClaimOperationID)
	}
	outcome := "committed"
	if lease.binding.Lifecycle == LifecycleCommitted && result.Binding.ExpiresAt.After(lease.binding.ExpiresAt) {
		outcome = "renewed"
	}
	if _, provenanceErr := s.commitProvenance(ctx, command, "durable_"+outcome); provenanceErr != nil &&
		!IsError(provenanceErr, ErrorUnknown) && !IsError(provenanceErr, ErrorCapacity) {
		return Binding{}, provenanceErr
	}
	s.emit("commit", outcome, lease.binding.ClaimOperationID, result.Binding)
	return result.Binding, nil
}

func (s *Service) commitMissingDurableBinding(ctx context.Context, command StoreCommit) (Binding, error) {
	s.emit("commit", "unknown", command.Binding.ClaimOperationID, command.Binding)
	binding, err := s.commitProvenance(ctx, command, "durable_unknown")
	if err == nil {
		return binding, nil
	}
	if !IsError(err, ErrorUnknown) {
		return Binding{}, err
	}
	return Binding{}, decisionError(StoreUnknown, command.Binding.Kind, command.Binding.ClaimOperationID)
}

func (s *Service) commitProvenance(ctx context.Context, command StoreCommit, outcome string) (Binding, error) {
	result, _ := s.provenance.Commit(ctx, command)
	if result.Decision != StoreCommitted {
		s.emit("provenance_commit", string(result.Decision), command.Binding.ClaimOperationID, result.Binding)
		return Binding{}, decisionError(result.Decision, command.Binding.Kind, command.Binding.ClaimOperationID)
	}
	s.emit("provenance_commit", outcome, command.Binding.ClaimOperationID, result.Binding)
	return result.Binding, nil
}

// AbandonBeforeDisclosure is intentionally limited to the operation that
// created the pending row. A retry that merely observed an older uncertain
// pending owner cannot release it for another ProtocolScope.
func (s *Service) AbandonBeforeDisclosure(ctx context.Context, lease Lease) error {
	if err := validateLease(lease); err != nil {
		return err
	}
	if !lease.created {
		return errorOf(
			ErrorInvalidTransition,
			lease.binding.Kind,
			lease.binding.ClaimOperationID,
			"only the operation that created a pending binding may abandon it",
			nil,
		)
	}
	limits, _ := s.policy.Limits(lease.binding.Kind)
	command := StoreAbandon{
		Binding: lease.binding,
		Now:     s.clock.Now().UTC(),
		Limits:  limits,
	}
	if lease.origin == leaseOriginProvenance {
		return s.abandonProvenance(ctx, command)
	}
	result, err := s.store.Abandon(ctx, command)
	if err != nil {
		return unavailable(lease.binding.Kind, lease.binding.ClaimOperationID, "abandon", err)
	}
	if result.Decision != StoreAbandoned {
		return decisionError(result.Decision, lease.binding.Kind, lease.binding.ClaimOperationID)
	}
	if err := s.abandonProvenance(ctx, command); err != nil && !IsError(err, ErrorUnknown) {
		return err
	}
	s.emit("abandon", "abandoned", lease.binding.ClaimOperationID, lease.binding)
	return nil
}

func (s *Service) abandonProvenance(ctx context.Context, command StoreAbandon) error {
	result, _ := s.provenance.Abandon(ctx, command)
	if result.Decision != StoreAbandoned {
		return decisionError(result.Decision, command.Binding.Kind, command.Binding.ClaimOperationID)
	}
	s.emit("provenance_abandon", "abandoned", command.Binding.ClaimOperationID, command.Binding)
	return nil
}

func (s *Service) Cleanup(ctx context.Context) (CleanupResult, error) {
	result, err := s.store.Cleanup(ctx, StoreCleanup{
		Now:    s.clock.Now().UTC(),
		Policy: s.policy.Entries(),
	})
	if err != nil {
		return CleanupResult{}, unavailable("", "cleanup", "cleanup", err)
	}
	observe(s.observer, Event{
		At:          s.clock.Now().UTC(),
		Action:      "cleanup",
		Outcome:     fmt.Sprintf("expired=%d,tombstoned=%d,deleted=%d", result.Expired, result.Tombstoned, result.Deleted),
		OperationID: "cleanup",
	})
	return result, nil
}

func (s *Service) RequiredHMACVersions(ctx context.Context) ([]string, error) {
	versions, err := s.store.RequiredHMACVersions(ctx)
	if err != nil {
		return nil, unavailable("", "startup", "list required HMAC versions", err)
	}
	return versions, nil
}

func (s *Service) OpenConnection(sessionID string, scope codexidentity.ProtocolScope) (Generation, error) {
	return s.generations.open(sessionID, scope)
}

func (s *Service) ActivateResponse(generation Generation, lease Lease) error {
	if err := validateLease(lease); err != nil {
		return err
	}
	return s.generations.activate(generation, lease.binding)
}

func (s *Service) DeactivateResponse(generation Generation, binding Binding) error {
	return s.generations.deactivate(generation, binding)
}

func (s *Service) CloseConnection(generation Generation) {
	s.generations.close(generation)
}

func (s *Service) prepareClaim(request ClaimRequest) (StoreClaim, error) {
	if err := validateEvidence(request.Evidence); err != nil {
		return StoreClaim{}, err
	}
	operationID, err := validateLabel(request.OperationID, "operation ID", MaxOperationIDBytes, false)
	if err != nil {
		return StoreClaim{}, err
	}
	currentClient, clients, err := validateScope(request.Scope)
	if err != nil {
		return StoreClaim{}, err
	}
	namespace, _ := request.Evidence.Kind.Namespace()
	currentDigest, err := s.digester.OpaqueDigest(namespace, request.Evidence.DigestInput)
	if err != nil {
		return StoreClaim{}, unavailable(request.Evidence.Kind, operationID, "sign opaque evidence", err)
	}
	digests, err := s.digestCandidates(request.Evidence, namespace, operationID)
	if err != nil {
		return StoreClaim{}, err
	}
	limits, _ := s.policy.Limits(request.Evidence.Kind)
	return StoreClaim{
		Kind:                  request.Evidence.Kind,
		CurrentDigest:         currentDigest,
		DigestCandidates:      digests,
		Owner:                 Owner{ClientScope: currentClient, ProtocolScope: request.Scope.ProtocolScope, RouteTargetHint: request.Scope.RouteTargetHint},
		ClientScopeCandidates: clients,
		OperationID:           operationID,
		Now:                   s.clock.Now().UTC(),
		Limits:                limits,
	}, nil
}

func (s *Service) prepareLookup(
	evidence Evidence,
	clients []codexidentity.ClientScope,
	protocolScope *codexidentity.ProtocolScope,
	operationID string,
) (StoreLookup, error) {
	if err := validateEvidence(evidence); err != nil {
		return StoreLookup{}, err
	}
	operationID, err := validateLabel(operationID, "operation ID", MaxOperationIDBytes, false)
	if err != nil {
		return StoreLookup{}, err
	}
	clients, err = validateClientScopes(clients)
	if err != nil {
		return StoreLookup{}, err
	}
	namespace, _ := evidence.Kind.Namespace()
	digests, err := s.digestCandidates(evidence, namespace, operationID)
	if err != nil {
		return StoreLookup{}, err
	}
	limits, _ := s.policy.Limits(evidence.Kind)
	return StoreLookup{
		Kind:                  evidence.Kind,
		DigestCandidates:      digests,
		ClientScopeCandidates: clients,
		ProtocolScope:         protocolScope,
		OperationID:           operationID,
		Now:                   s.clock.Now().UTC(),
		Limits:                limits,
	}, nil
}

func (s *Service) digestCandidates(
	evidence Evidence,
	namespace codexidentity.OpaqueNamespace,
	operationID string,
) ([]codexidentity.OpaqueDigest, error) {
	digests, err := s.digester.OpaqueDigestCandidates(namespace, evidence.DigestInput)
	if err != nil {
		return nil, unavailable(evidence.Kind, operationID, "derive opaque lookup candidates", err)
	}
	if len(digests) == 0 || len(digests) > MaxLookupCandidates {
		return nil, errorOf(ErrorUnavailable, evidence.Kind, operationID, "opaque lookup candidate count is invalid", nil)
	}
	return digests, nil
}

func validateEvidence(evidence Evidence) error {
	if err := evidence.Kind.Validate(); err != nil {
		return err
	}
	if len(evidence.DigestInput) == 0 || len(evidence.DigestInput) > MaxDigestInputBytes {
		return errorOf(ErrorInvalidInput, evidence.Kind, "", "digest input is empty or too large", nil)
	}
	return nil
}

func validateScope(scope Scope) (codexidentity.ClientScope, []codexidentity.ClientScope, error) {
	if _, err := scope.ProtocolScope.MarshalBinary(); err != nil {
		return codexidentity.ClientScope{}, nil, errorOf(ErrorInvalidInput, "", "", "protocol scope is invalid", err)
	}
	if _, err := validateLabel(scope.RouteTargetHint, "route target hint", MaxRouteTargetIDBytes, true); err != nil {
		return codexidentity.ClientScope{}, nil, err
	}
	if _, err := scope.CurrentClientScope.MarshalBinary(); err != nil {
		return codexidentity.ClientScope{}, nil, errorOf(ErrorInvalidInput, "", "", "current client scope is invalid", err)
	}
	candidates := append([]codexidentity.ClientScope{scope.CurrentClientScope}, scope.ClientScopeCandidates...)
	validated, err := validateClientScopes(candidates)
	return scope.CurrentClientScope, validated, err
}

func validateClientScopes(scopes []codexidentity.ClientScope) ([]codexidentity.ClientScope, error) {
	if len(scopes) == 0 || len(scopes) > MaxLookupCandidates {
		return nil, errorOf(ErrorInvalidInput, "", "", "client scope candidate count is invalid", nil)
	}
	result := make([]codexidentity.ClientScope, 0, len(scopes))
	for _, scope := range scopes {
		if _, err := scope.MarshalBinary(); err != nil {
			return nil, errorOf(ErrorInvalidInput, "", "", "client scope candidate is invalid", err)
		}
		duplicate := false
		for _, existing := range result {
			duplicate = duplicate || existing.Equal(scope)
		}
		if !duplicate {
			result = append(result, scope)
		}
	}
	return result, nil
}

func validateLease(lease Lease) error {
	if err := lease.binding.Kind.Validate(); err != nil {
		return err
	}
	if lease.binding.ClaimOperationID == "" ||
		(lease.binding.Lifecycle != LifecyclePending && lease.binding.Lifecycle != LifecycleCommitted) {
		return errorOf(ErrorInvalidTransition, lease.binding.Kind, lease.binding.ClaimOperationID, "lease is not active", nil)
	}
	if lease.origin != leaseOriginDurable && lease.origin != leaseOriginProvenance {
		return errorOf(ErrorInvalidTransition, lease.binding.Kind, lease.binding.ClaimOperationID, "lease origin is invalid", nil)
	}
	return nil
}

func decisionError(decision StoreDecision, kind Kind, operationID string) error {
	switch decision {
	case StoreUnknown:
		return errorOf(ErrorUnknown, kind, operationID, "owner binding is unknown", nil)
	case StoreExpired:
		return errorOf(ErrorExpired, kind, operationID, "owner binding is expired", nil)
	case StoreConflict:
		return errorOf(ErrorConflict, kind, operationID, "owner binding belongs to another scope or operation", nil)
	case StoreCapacity:
		return errorOf(ErrorCapacity, kind, operationID, "binding capacity is exhausted", nil)
	default:
		return errorOf(ErrorInvalidTransition, kind, operationID, "store returned an invalid decision", nil)
	}
}

func (s *Service) emit(action, outcome, operationID string, binding Binding) {
	event := Event{
		At:          s.clock.Now().UTC(),
		Action:      action,
		Outcome:     outcome,
		OperationID: operationID,
		BindingKind: binding.Kind,
		Lifecycle:   binding.Lifecycle,
	}
	if binding.Kind != "" {
		event.KeyVersion = binding.Digest.KeyVersion()
		event.ClientVersion = binding.Owner.ClientScope.KeyVersion()
		event.ProtocolScope = binding.Owner.ProtocolScope.String()
		event.RouteTargetHint = binding.Owner.RouteTargetHint
	}
	observe(s.observer, event)
}
