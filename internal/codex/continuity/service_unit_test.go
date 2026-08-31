package codexcontinuity

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/identity"
)

type stubStore struct {
	claim    func(StoreClaim) (StoreResult, error)
	lookup   func(StoreLookup) (StoreResult, error)
	commit   func(StoreCommit) (StoreResult, error)
	abandon  func(StoreAbandon) (StoreResult, error)
	cleanup  func(StoreCleanup) (CleanupResult, error)
	versions func() ([]string, error)
}

func (s *stubStore) Claim(_ context.Context, command StoreClaim) (StoreResult, error) {
	if s.claim != nil {
		return s.claim(command)
	}
	return StoreResult{Decision: StoreClaimed, Binding: bindingFromClaim(command)}, nil
}

func (s *stubStore) Lookup(_ context.Context, command StoreLookup) (StoreResult, error) {
	if s.lookup != nil {
		return s.lookup(command)
	}
	return StoreResult{Decision: StoreUnknown}, nil
}

func (s *stubStore) Commit(_ context.Context, command StoreCommit) (StoreResult, error) {
	if s.commit != nil {
		return s.commit(command)
	}
	binding := command.Binding
	binding.Lifecycle = LifecycleCommitted
	return StoreResult{Decision: StoreCommitted, Binding: binding}, nil
}

func (s *stubStore) Abandon(_ context.Context, command StoreAbandon) (StoreResult, error) {
	if s.abandon != nil {
		return s.abandon(command)
	}
	return StoreResult{Decision: StoreAbandoned, Binding: command.Binding}, nil
}

func (s *stubStore) Cleanup(_ context.Context, command StoreCleanup) (CleanupResult, error) {
	if s.cleanup != nil {
		return s.cleanup(command)
	}
	return CleanupResult{}, nil
}

func (s *stubStore) RequiredHMACVersions(context.Context) ([]string, error) {
	if s.versions != nil {
		return s.versions()
	}
	return nil, nil
}

type stubDigester struct {
	current    codexidentity.OpaqueDigest
	candidates []codexidentity.OpaqueDigest
	signErr    error
	lookupErr  error
}

func (d stubDigester) OpaqueDigest(codexidentity.OpaqueNamespace, []byte) (codexidentity.OpaqueDigest, error) {
	return d.current, d.signErr
}

func (d stubDigester) OpaqueDigestCandidates(codexidentity.OpaqueNamespace, []byte) ([]codexidentity.OpaqueDigest, error) {
	return append([]codexidentity.OpaqueDigest(nil), d.candidates...), d.lookupErr
}

type nilStore struct{ *stubStore }
type nilDigester struct{ *stubDigester }

func TestPolicyValidationAndKindContracts(t *testing.T) {
	valid := validPolicyConfig()
	policy, err := NewPolicy(valid)
	if err != nil || len(policy.Entries()) != len(allKinds) {
		t.Fatalf("valid policy = %#v, %v", policy, err)
	}
	copied := policy.Entries()
	delete(copied, KindThreadID)
	if _, exists := policy.Limits(KindThreadID); !exists {
		t.Fatal("policy Entries returned an alias")
	}

	for name, configured := range map[string]map[Kind]Limits{
		"missing": func() map[Kind]Limits {
			value := validPolicyConfig()
			delete(value, KindThreadID)
			return value
		}(),
		"unsupported": func() map[Kind]Limits {
			value := validPolicyConfig()
			delete(value, KindThreadID)
			value[Kind("future")] = testLimits()
			return value
		}(),
		"zero ttl": func() map[Kind]Limits {
			value := validPolicyConfig()
			value[KindThreadID] = Limits{}
			return value
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewPolicy(configured); err == nil {
				t.Fatal("NewPolicy succeeded")
			}
		})
	}
	if _, ok := policy.Limits(Kind("future")); ok {
		t.Fatal("future kind unexpectedly has limits")
	}
	if _, err := Kind("future").Namespace(); !IsError(err, ErrorInvalidInput) {
		t.Fatalf("future namespace error = %v", err)
	}
	if Kind("future").ClientClaimable() || Kind("future").ResponseIssuable() {
		t.Fatal("future kind inherited claim semantics")
	}
}

func TestConstructorAndInputValidation(t *testing.T) {
	policy := mustPolicy(t)
	digest := testOpaqueDigest(t, KindTurnMetadata, 1, "h1")
	validStore := &stubStore{}
	validDigester := stubDigester{current: digest, candidates: []codexidentity.OpaqueDigest{digest}}

	for name, config := range map[string]Config{
		"nil store":          {Digester: validDigester, Policy: policy},
		"typed nil store":    {Store: (*nilStore)(nil), Digester: validDigester, Policy: policy},
		"nil digester":       {Store: validStore, Policy: policy},
		"typed nil digester": {Store: validStore, Digester: (*nilDigester)(nil), Policy: policy},
		"empty policy":       {Store: validStore, Digester: validDigester},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewService(config); !IsError(err, ErrorInvalidInput) {
				t.Fatalf("NewService error = %v", err)
			}
		})
	}

	service, err := NewService(Config{Store: validStore, Digester: validDigester, Policy: policy})
	if err != nil {
		t.Fatal(err)
	}
	scope := testScope(t, "account", "codex")
	client := testClientScope(t, 2, "h1")
	validRequest := ClaimRequest{
		Evidence:    Evidence{Kind: KindTurnMetadata, DigestInput: []byte("value")},
		Scope:       Scope{CurrentClientScope: client, ClientScopeCandidates: []codexidentity.ClientScope{client}, ProtocolScope: scope},
		OperationID: "operation",
	}
	if _, err := service.Claim(context.Background(), validRequest); err != nil {
		t.Fatalf("default clock claim: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*ClaimRequest)
	}{
		{name: "unknown kind", mutate: func(r *ClaimRequest) { r.Evidence.Kind = "future" }},
		{name: "empty input", mutate: func(r *ClaimRequest) { r.Evidence.DigestInput = nil }},
		{name: "large input", mutate: func(r *ClaimRequest) { r.Evidence.DigestInput = make([]byte, MaxDigestInputBytes+1) }},
		{name: "empty operation", mutate: func(r *ClaimRequest) { r.OperationID = "" }},
		{name: "noncanonical operation", mutate: func(r *ClaimRequest) { r.OperationID = " operation" }},
		{name: "control operation", mutate: func(r *ClaimRequest) { r.OperationID = "bad\n" }},
		{name: "large route", mutate: func(r *ClaimRequest) { r.Scope.RouteTargetHint = strings.Repeat("r", MaxRouteTargetIDBytes+1) }},
		{name: "invalid current client", mutate: func(r *ClaimRequest) { r.Scope.CurrentClientScope = codexidentity.ClientScope{} }},
		{name: "invalid protocol", mutate: func(r *ClaimRequest) { r.Scope.ProtocolScope = codexidentity.ProtocolScope{} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := validRequest
			test.mutate(&request)
			if _, err := service.Claim(context.Background(), request); err == nil {
				t.Fatal("Claim succeeded")
			}
		})
	}

	tooMany := make([]codexidentity.ClientScope, MaxLookupCandidates+1)
	for index := range tooMany {
		tooMany[index] = client
	}
	_, err = service.ResolveOwner(context.Background(), ResolveRequest{
		Evidence: validRequest.Evidence, ClientScopeCandidates: tooMany, OperationID: "many-clients",
	})
	if !IsError(err, ErrorInvalidInput) {
		t.Fatalf("too many clients = %v", err)
	}
	_, err = service.ResolveOwner(context.Background(), ResolveRequest{
		Evidence: validRequest.Evidence, ClientScopeCandidates: []codexidentity.ClientScope{{}}, OperationID: "invalid-client",
	})
	if !IsError(err, ErrorInvalidInput) {
		t.Fatalf("invalid client = %v", err)
	}
	_, err = service.Validate(context.Background(), ValidateRequest{
		Evidence: validRequest.Evidence, ClientScopeCandidates: []codexidentity.ClientScope{client}, OperationID: "invalid-protocol",
	})
	if !IsError(err, ErrorInvalidInput) {
		t.Fatalf("invalid protocol = %v", err)
	}
}

func TestDigesterAndStoreFailuresAreTypedUnavailable(t *testing.T) {
	policy := mustPolicy(t)
	digest := testOpaqueDigest(t, KindTurnMetadata, 1, "h1")
	client := testClientScope(t, 2, "h1")
	scope := testScope(t, "account", "codex")
	request := ClaimRequest{
		Evidence:    Evidence{Kind: KindTurnMetadata, DigestInput: []byte("value")},
		Scope:       Scope{CurrentClientScope: client, ProtocolScope: scope},
		OperationID: "operation",
	}

	for name, digester := range map[string]stubDigester{
		"sign":     {signErr: errors.New("sign failed")},
		"lookup":   {current: digest, lookupErr: errors.New("lookup failed")},
		"empty":    {current: digest},
		"too many": {current: digest, candidates: make([]codexidentity.OpaqueDigest, MaxLookupCandidates+1)},
	} {
		t.Run(name, func(t *testing.T) {
			service, err := NewService(Config{Store: &stubStore{}, Digester: digester, Policy: policy})
			if err != nil {
				t.Fatal(err)
			}
			_, err = service.Claim(context.Background(), request)
			if !IsError(err, ErrorUnavailable) {
				t.Fatalf("Claim error = %v", err)
			}
		})
	}

	storeFailure := errors.New("database unavailable")
	store := &stubStore{
		claim: func(StoreClaim) (StoreResult, error) { return StoreResult{}, storeFailure },
	}
	service, err := NewService(Config{
		Store: store, Digester: stubDigester{current: digest, candidates: []codexidentity.OpaqueDigest{digest}}, Policy: policy,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Claim(context.Background(), request)
	if !IsError(err, ErrorUnavailable) || !errors.Is(err, storeFailure) {
		t.Fatalf("store claim error = %v", err)
	}
}

func TestStoreDecisionsCommitAndAbandonFailures(t *testing.T) {
	policy := mustPolicy(t)
	digest := testOpaqueDigest(t, KindTurnMetadata, 1, "h1")
	client := testClientScope(t, 2, "h1")
	scope := testScope(t, "account", "codex")
	request := ClaimRequest{
		Evidence:    Evidence{Kind: KindTurnMetadata, DigestInput: []byte("value")},
		Scope:       Scope{CurrentClientScope: client, ProtocolScope: scope},
		OperationID: "operation",
	}
	digester := stubDigester{current: digest, candidates: []codexidentity.OpaqueDigest{digest}}

	for decision, want := range map[StoreDecision]ErrorKind{
		StoreUnknown:         ErrorUnknown,
		StoreExpired:         ErrorExpired,
		StoreConflict:        ErrorConflict,
		StoreCapacity:        ErrorCapacity,
		StoreDecision("bad"): ErrorUnavailable,
	} {
		t.Run(string(decision), func(t *testing.T) {
			store := &stubStore{claim: func(StoreClaim) (StoreResult, error) {
				return StoreResult{Decision: decision}, nil
			}}
			service, err := NewService(Config{Store: store, Digester: digester, Policy: policy})
			if err != nil {
				t.Fatal(err)
			}
			_, err = service.Claim(context.Background(), request)
			if !IsError(err, want) {
				t.Fatalf("Claim error = %v, want %s", err, want)
			}
		})
	}

	store := &stubStore{}
	service, err := NewService(Config{Store: store, Digester: digester, Policy: policy})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := service.Claim(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	store.commit = func(StoreCommit) (StoreResult, error) { return StoreResult{}, errors.New("commit failed") }
	committed, err := service.Commit(context.Background(), lease)
	if err != nil || committed.Lifecycle != LifecycleCommitted {
		t.Fatalf("commit provenance fallback = %#v, %v", committed, err)
	}
	store.commit = func(StoreCommit) (StoreResult, error) { return StoreResult{Decision: StoreExpired}, nil }
	if _, err := service.Commit(context.Background(), lease); !IsError(err, ErrorExpired) {
		t.Fatalf("commit expired = %v", err)
	}
	missingStore := &stubStore{commit: func(StoreCommit) (StoreResult, error) {
		return StoreResult{Decision: StoreUnknown}, nil
	}}
	missingService, err := NewService(Config{Store: missingStore, Digester: digester, Policy: policy})
	if err != nil {
		t.Fatal(err)
	}
	missingLease, err := missingService.Claim(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if committed, err := missingService.Commit(context.Background(), missingLease); err != nil || committed.Lifecycle != LifecycleCommitted {
		t.Fatalf("commit missing durable row = %#v, %v", committed, err)
	}
	store.abandon = func(StoreAbandon) (StoreResult, error) { return StoreResult{}, errors.New("abandon failed") }
	if err := service.AbandonBeforeDisclosure(context.Background(), lease); !IsError(err, ErrorUnavailable) {
		t.Fatalf("abandon unavailable = %v", err)
	}
	store.abandon = func(StoreAbandon) (StoreResult, error) { return StoreResult{Decision: StoreConflict}, nil }
	if err := service.AbandonBeforeDisclosure(context.Background(), lease); !IsError(err, ErrorConflict) {
		t.Fatalf("abandon conflict = %v", err)
	}

	if _, err := service.Commit(context.Background(), Lease{}); !IsError(err, ErrorInvalidInput) {
		t.Fatalf("zero lease commit = %v", err)
	}
	if err := service.AbandonBeforeDisclosure(context.Background(), Lease{}); !IsError(err, ErrorInvalidInput) {
		t.Fatalf("zero lease abandon = %v", err)
	}
	if err := service.ActivateResponse("generation", Lease{}); !IsError(err, ErrorInvalidInput) {
		t.Fatalf("zero lease activation = %v", err)
	}
}

func TestResponseProvenanceIsAtomicAcrossAuthoritiesDuringStoreOutage(t *testing.T) {
	digest := testOpaqueDigest(t, KindTurnState, 11, "h1")
	client := testClientScope(t, 12, "h1")
	scopeA := testScope(t, "account-a", "codex")
	scopeB := testScope(t, "account-b", "codex")
	storeFailure := errors.New("database unavailable")
	store := &stubStore{
		claim:  func(StoreClaim) (StoreResult, error) { return StoreResult{}, storeFailure },
		lookup: func(StoreLookup) (StoreResult, error) { return StoreResult{}, storeFailure },
		commit: func(StoreCommit) (StoreResult, error) { return StoreResult{}, storeFailure },
	}
	service, err := NewService(Config{
		Store: store,
		Digester: stubDigester{
			current: digest, candidates: []codexidentity.OpaqueDigest{digest},
		},
		Policy: mustPolicy(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	request := ClaimRequest{
		Evidence: Evidence{Kind: KindTurnState, DigestInput: []byte("state")},
		Scope: Scope{
			CurrentClientScope: client, ClientScopeCandidates: []codexidentity.ClientScope{client},
			ProtocolScope: scopeA,
		},
		OperationID: "response-a",
	}
	lease, err := service.PrepareVisible(context.Background(), request)
	if err != nil || lease.origin != leaseOriginProvenance {
		t.Fatalf("provenance lease = %#v, %v", lease, err)
	}
	conflict := request
	conflict.Scope.ProtocolScope = scopeB
	conflict.OperationID = "response-b"
	if _, err := service.PrepareVisible(context.Background(), conflict); !IsError(err, ErrorConflict) {
		t.Fatalf("cross-authority response claim = %v", err)
	}
	if _, err := service.Commit(context.Background(), lease); err != nil {
		t.Fatal("commit provenance lease:", err)
	}
	recovered, err := service.ResolveOwner(context.Background(), ResolveRequest{
		Evidence: request.Evidence, ClientScopeCandidates: []codexidentity.ClientScope{client}, OperationID: "next-turn",
	})
	if err != nil || !recovered.Owner.ProtocolScope.Equal(scopeA) {
		t.Fatalf("recovered owner = %#v, %v", recovered, err)
	}
	if _, err := service.AcquireExisting(context.Background(), ValidateRequest{
		Evidence: request.Evidence, ClientScopeCandidates: []codexidentity.ClientScope{client},
		ProtocolScope: scopeB, OperationID: "wrong-authority",
	}); !IsError(err, ErrorConflict) {
		t.Fatalf("cross-authority acquire = %v", err)
	}
}

func TestProvenanceLookupRepairsRecoveredDurableStore(t *testing.T) {
	digest := testOpaqueDigest(t, KindResponseReference, 13, "h1")
	client := testClientScope(t, 14, "h1")
	scope := testScope(t, "account", "codex")
	outage := true
	var durable Binding
	store := &stubStore{}
	store.claim = func(command StoreClaim) (StoreResult, error) {
		if outage {
			return StoreResult{}, errors.New("database unavailable")
		}
		if durable.Kind == "" {
			durable = bindingFromClaim(command)
			return StoreResult{Decision: StoreClaimed, Binding: durable}, nil
		}
		return StoreResult{Decision: StoreOwned, Binding: durable}, nil
	}
	store.lookup = func(StoreLookup) (StoreResult, error) {
		if outage {
			return StoreResult{}, errors.New("database unavailable")
		}
		if durable.Kind == "" {
			return StoreResult{Decision: StoreUnknown}, nil
		}
		return StoreResult{Decision: StoreOwned, Binding: durable}, nil
	}
	digester := stubDigester{current: digest, candidates: []codexidentity.OpaqueDigest{digest}}
	service, err := NewService(Config{Store: store, Digester: digester, Policy: mustPolicy(t)})
	if err != nil {
		t.Fatal(err)
	}
	request := ClaimRequest{
		Evidence: Evidence{Kind: KindResponseReference, DigestInput: []byte("response")},
		Scope: Scope{
			CurrentClientScope: client, ClientScopeCandidates: []codexidentity.ClientScope{client},
			ProtocolScope: scope,
		},
		OperationID: "response",
	}
	lease, err := service.PrepareVisible(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Commit(context.Background(), lease); err != nil {
		t.Fatal(err)
	}

	outage = false
	resolved, err := service.ResolveOwner(context.Background(), ResolveRequest{
		Evidence: request.Evidence, ClientScopeCandidates: []codexidentity.ClientScope{client}, OperationID: "repair",
	})
	if err != nil || durable.Kind == "" || !resolved.Owner.ProtocolScope.Equal(scope) {
		t.Fatalf("repair result=%#v durable=%#v err=%v", resolved, durable, err)
	}

	restarted, err := NewService(Config{Store: store, Digester: digester, Policy: mustPolicy(t)})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err = restarted.ResolveOwner(context.Background(), ResolveRequest{
		Evidence: request.Evidence, ClientScopeCandidates: []codexidentity.ClientScope{client}, OperationID: "after-restart",
	})
	if err != nil || !resolved.Owner.ProtocolScope.Equal(scope) {
		t.Fatalf("durable recovery result=%#v err=%v", resolved, err)
	}
}

func TestFormattingAndGenerationValidation(t *testing.T) {
	cause := errors.New("cause")
	err := errorOf(ErrorUnavailable, KindTurnState, "bad\noperation", "reason", cause)
	if !strings.Contains(err.Error(), "operation_id=invalid") || !errors.Is(err, cause) {
		t.Fatalf("formatted error = %v", err)
	}
	var nilError *Error
	if nilError.Error() != "<nil>" || nilError.Unwrap() != nil {
		t.Fatal("nil error formatting changed")
	}
	if safeLabel("") != "invalid" || safeLabel("ok") != "ok" {
		t.Fatal("safeLabel contract changed")
	}

	digest := testOpaqueDigest(t, KindResponseReference, 1, "h1")
	client := testClientScope(t, 2, "h1")
	scope := testScope(t, "account", "codex")
	binding := Binding{
		Kind:             KindResponseReference,
		Digest:           digest,
		Owner:            Owner{ClientScope: client, ProtocolScope: scope},
		Lifecycle:        LifecyclePending,
		ClaimOperationID: "operation",
	}
	if !strings.Contains(binding.String(), "digest=redacted") || binding.GoString() != binding.String() {
		t.Fatalf("binding formatting = %#v", binding)
	}
	if Generation("").String() != "connection-generation(invalid)" || !strings.Contains(Generation("gen").String(), "gen") {
		t.Fatal("generation formatting changed")
	}

	service, err := NewService(Config{
		Store:         &stubStore{},
		Digester:      stubDigester{current: digest, candidates: []codexidentity.OpaqueDigest{digest}},
		Policy:        mustPolicy(t),
		GenerationIDs: &fixedUnitGenerationIDs{id: "gen"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.OpenConnection("", scope); !IsError(err, ErrorInvalidInput) {
		t.Fatalf("empty session = %v", err)
	}
	if _, err := service.OpenConnection("session", codexidentity.ProtocolScope{}); !IsError(err, ErrorInvalidInput) {
		t.Fatalf("invalid scope = %v", err)
	}
	generation, err := service.OpenConnection("session", scope)
	if err != nil {
		t.Fatal(err)
	}
	if generation != "gen" {
		t.Fatalf("generation = %q", generation)
	}
	service.CloseConnection("missing")
}

type fixedUnitGenerationIDs struct{ id string }

func (s *fixedUnitGenerationIDs) NewGenerationID() string { return s.id }

func validPolicyConfig() map[Kind]Limits {
	result := make(map[Kind]Limits, len(allKinds))
	for _, kind := range allKinds {
		result[kind] = testLimits()
	}
	return result
}

func testLimits() Limits {
	return Limits{PendingTTL: time.Minute, CommittedIdleTTL: time.Hour, TombstoneTTL: time.Minute, MaxBindings: 10}
}

func mustPolicy(t *testing.T) Policy {
	t.Helper()
	policy, err := NewPolicy(validPolicyConfig())
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

func testOpaqueDigest(t *testing.T, kind Kind, seed byte, version string) codexidentity.OpaqueDigest {
	t.Helper()
	var sum [codexidentity.DigestSize]byte
	for index := range sum {
		sum[index] = seed + byte(index)
	}
	namespace, err := kind.Namespace()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := codexidentity.OpaqueDigestFromParts(namespace, version, sum)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func testClientScope(t *testing.T, seed byte, version string) codexidentity.ClientScope {
	t.Helper()
	var sum [codexidentity.DigestSize]byte
	for index := range sum {
		sum[index] = seed + byte(index)
	}
	scope, err := codexidentity.ClientScopeFromDigest(version, sum)
	if err != nil {
		t.Fatal(err)
	}
	return scope
}

func testScope(t *testing.T, account, apiType string) codexidentity.ProtocolScope {
	t.Helper()
	origin, err := codexidentity.ParseOrigin("https://api.example.com")
	if err != nil {
		t.Fatal(err)
	}
	subject, err := codexidentity.NewAccountCredentialSubject(account)
	if err != nil {
		t.Fatal(err)
	}
	authority, err := codexidentity.NewUpstreamAuthority("vendor", origin, subject)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := codexidentity.NewProtocolScope(authority, apiType)
	if err != nil {
		t.Fatal(err)
	}
	return scope
}

func bindingFromClaim(command StoreClaim) Binding {
	return Binding{
		Kind:             command.Kind,
		Digest:           command.CurrentDigest,
		Owner:            command.Owner,
		Lifecycle:        LifecyclePending,
		ClaimOperationID: command.OperationID,
		CreatedAt:        command.Now,
		UpdatedAt:        command.Now,
		ExpiresAt:        command.Now.Add(command.Limits.PendingTTL),
	}
}

func TestErrorFormattingIncludesKnownOperation(t *testing.T) {
	err := &Error{Kind: ErrorConflict, BindingKind: KindSessionID, OperationID: "operation", Reason: "reason"}
	if got := err.Error(); got != "codex continuity: conflict (binding_kind=session_id) (operation_id=operation): reason" {
		t.Fatalf("Error() = %q", got)
	}
	if !errors.Is(fmt.Errorf("wrapped: %w", err), &Error{Kind: ErrorConflict}) {
		t.Fatal("wrapped typed error did not match")
	}
}
