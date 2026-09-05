package codexprovenance

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	codexcontinuity "github.com/doraemonkeys/switch-a/internal/codex/continuity"
	codexidentity "github.com/doraemonkeys/switch-a/internal/codex/identity"
	"github.com/doraemonkeys/switch-a/internal/model"
)

type resolverFunc func(context.Context, codexcontinuity.ResolveRequest) (codexcontinuity.Resolution, error)

func (f resolverFunc) Resolve(ctx context.Context, request codexcontinuity.ResolveRequest) (codexcontinuity.Resolution, error) {
	return f(ctx, request)
}

func TestEntranceResolutionSurvivesStoreChangeAndResponseEcho(t *testing.T) {
	client := testClient(t, 1)
	owner := testOwner(t, client, "account-a", "codex")
	for _, status := range []codexcontinuity.ResolutionStatus{
		codexcontinuity.ResolutionOwned, codexcontinuity.ResolutionUnknown,
		codexcontinuity.ResolutionExpired, codexcontinuity.ResolutionUnavailable,
	} {
		t.Run(string(status), func(t *testing.T) {
			calls := 0
			entrance := codexcontinuity.Resolution{Status: status}
			if status == codexcontinuity.ResolutionOwned || status == codexcontinuity.ResolutionExpired {
				entrance.Owner = &owner
			}
			ledger := NewLedger(Config{
				Resolver: resolverFunc(func(_ context.Context, request codexcontinuity.ResolveRequest) (codexcontinuity.Resolution, error) {
					calls++
					if calls > 1 {
						t.Fatal("replay or echo re-resolved entrance state")
					}
					if request.OperationID != "operation" || !request.ClientScopeCandidates[0].Equal(client) {
						t.Fatal("resolver lost operation identity")
					}
					request.ClientScopeCandidates[0] = testClient(t, 9)
					return entrance, nil
				}),
				RecoveryPolicy:        model.ConversationRecoverySwitchAccountPreserveConversation,
				ClientScopeCandidates: []codexidentity.ClientScope{client}, APIType: "codex", OperationID: "operation",
			})
			evidence := codexcontinuity.Evidence{Kind: codexcontinuity.KindTurnState, DigestInput: []byte("category-prefixed-secret")}
			resolved, err := ledger.ObserveRequest(context.Background(), evidence)
			if err != nil {
				t.Fatal(err)
			}
			if resolved.Owner != nil {
				resolved.Owner.RouteTargetHint = "mutated"
			}
			// Reusing and mutating the transport's byte buffer cannot modify a key.
			evidence.DigestInput[0] = 'X'
			evidence.DigestInput = []byte("category-prefixed-secret")
			replay, err := ledger.ObserveRequest(context.Background(), evidence)
			if err != nil || replay.Status != status {
				t.Fatalf("replay = %#v, %v", replay, err)
			}
			echo, exists := ledger.LookupRequest(evidence)
			if !exists || echo.Status != status || calls != 1 {
				t.Fatalf("echo = %#v, exists %v, calls %d", echo, exists, calls)
			}
			if echo.Owner != nil && (echo.Owner.RouteTargetHint != "route-account-a" || replay.Owner.RouteTargetHint != "route-account-a") {
				t.Fatal("returned resolution aliases ledger owner")
			}
			if err := ledger.ValidateOwner(echo, testOwner(t, client, "account-b", "codex").ProtocolScope); err != nil {
				t.Fatalf("recovery revalidated echo against B: %v", err)
			}
			evidence.DigestInput = []byte("new-provider-b-state")
			if _, exists := ledger.LookupRequest(evidence); exists {
				t.Fatal("response-only state entered request ledger")
			}
			if len(ledger.requests) != 1 {
				t.Fatal("response lookup mutated request ledger")
			}
		})
	}
}

func TestMixedAuthoritiesPreserveClientAndAPIIsolation(t *testing.T) {
	client := testClient(t, 1)
	ownerA, ownerB := testOwner(t, client, "account-a", "codex"), testOwner(t, client, "account-b", "codex")
	ledger := NewLedger(Config{
		Resolver: resolverFunc(func(_ context.Context, request codexcontinuity.ResolveRequest) (codexcontinuity.Resolution, error) {
			owner := ownerA
			if request.Evidence.Kind == codexcontinuity.KindSessionID {
				owner = ownerB
			}
			return codexcontinuity.Resolution{Status: codexcontinuity.ResolutionOwned, Owner: &owner}, nil
		}),
		RecoveryPolicy:        model.ConversationRecoverySwitchAccountPreserveConversation,
		ClientScopeCandidates: []codexidentity.ClientScope{client}, APIType: "codex", OperationID: "mixed",
	})
	for _, kind := range []codexcontinuity.Kind{codexcontinuity.KindThreadID, codexcontinuity.KindSessionID} {
		resolved, err := ledger.ObserveRequest(context.Background(), codexcontinuity.Evidence{Kind: kind, DigestInput: []byte("same-bytes")})
		if err != nil || ledger.ValidateOwner(resolved, ownerB.ProtocolScope) != nil {
			t.Fatalf("mixed source admission = %#v, %v", resolved, err)
		}
	}
	if len(ledger.requests) != 2 {
		t.Fatal("different kinds collided")
	}
	for _, policy := range []model.ConversationRecoveryPolicy{"", model.ConversationRecoveryPreserveConversation, model.ConversationRecoverySwitchAccountPreserveConversation} {
		for _, status := range []codexcontinuity.ResolutionStatus{codexcontinuity.ResolutionOwned, codexcontinuity.ResolutionExpired} {
			t.Run(fmt.Sprintf("%s/%s", policy, status), func(t *testing.T) {
				l := NewLedger(Config{RecoveryPolicy: policy, ClientScopeCandidates: []codexidentity.ClientScope{client}, APIType: "codex", OperationID: "verify"})
				resolution := codexcontinuity.Resolution{Status: status, Owner: &ownerA}
				err := l.ValidateOwner(resolution, ownerB.ProtocolScope)
				if policy == model.ConversationRecoverySwitchAccountPreserveConversation {
					if err != nil {
						t.Fatal(err)
					}
				} else if !codexcontinuity.IsError(err, codexcontinuity.ErrorConflict) {
					t.Fatalf("strict mixed-authority error = %v", err)
				}
				for _, foreign := range []codexcontinuity.Owner{
					testOwner(t, testClient(t, 2), "account-a", "codex"),
					testOwner(t, client, "account-a", "responses"),
				} {
					resolution.Owner = &foreign
					if err := l.ValidateOwner(resolution, foreign.ProtocolScope); !codexcontinuity.IsError(err, codexcontinuity.ErrorConflict) {
						t.Fatalf("foreign source admitted: %v", err)
					}
				}
			})
		}
	}
}

func TestLedgerConcurrentObservationAndConfigSnapshot(t *testing.T) {
	client := testClient(t, 1)
	owner := testOwner(t, client, "account-a", "codex")
	calls := 0
	config := Config{
		Resolver: resolverFunc(func(context.Context, codexcontinuity.ResolveRequest) (codexcontinuity.Resolution, error) {
			calls++
			return codexcontinuity.Resolution{Status: codexcontinuity.ResolutionOwned, Owner: &owner}, nil
		}),
		ClientScopeCandidates: []codexidentity.ClientScope{client}, APIType: "codex", OperationID: "concurrent",
	}
	ledger := NewLedger(config)
	config.ClientScopeCandidates[0] = testClient(t, 2)
	const readers = 12
	var group sync.WaitGroup
	for range readers {
		group.Go(func() {
			_, err := ledger.ObserveRequest(context.Background(), codexcontinuity.Evidence{Kind: codexcontinuity.KindTurnState, DigestInput: []byte("state")})
			if err != nil {
				t.Error(err)
			}
		})
	}
	group.Wait()
	if calls != 1 {
		t.Fatalf("concurrent resolutions = %d", calls)
	}
}

func TestLedgerRejectsInvalidResolutionAndUnclassifiedErrors(t *testing.T) {
	client := testClient(t, 1)
	owner := testOwner(t, client, "a", "codex")
	for _, resolution := range []codexcontinuity.Resolution{
		{}, {Status: codexcontinuity.ResolutionOwned},
		{Status: codexcontinuity.ResolutionUnknown, Owner: &owner},
		{Status: codexcontinuity.ResolutionUnavailable, Owner: &owner},
	} {
		ledger := NewLedger(Config{Resolver: resolverFunc(func(context.Context, codexcontinuity.ResolveRequest) (codexcontinuity.Resolution, error) {
			return resolution, nil
		})})
		if _, err := ledger.ObserveRequest(context.Background(), codexcontinuity.Evidence{Kind: codexcontinuity.KindTurnState, DigestInput: []byte("state")}); !codexcontinuity.IsError(err, codexcontinuity.ErrorInvalidTransition) {
			t.Fatalf("invalid resolution = %#v, error %v", resolution, err)
		}
	}
	ledger := NewLedger(Config{})
	for _, evidence := range []codexcontinuity.Evidence{{}, {Kind: codexcontinuity.KindTurnState}, {Kind: codexcontinuity.KindTurnState, DigestInput: []byte("state")}} {
		if _, err := ledger.ObserveRequest(context.Background(), evidence); !codexcontinuity.IsError(err, codexcontinuity.ErrorInvalidInput) {
			t.Fatalf("invalid input error = %v", err)
		}
	}
	failure := errors.New("unclassified")
	ledger = NewLedger(Config{Resolver: resolverFunc(func(context.Context, codexcontinuity.ResolveRequest) (codexcontinuity.Resolution, error) {
		return codexcontinuity.Resolution{}, failure
	})})
	if _, err := ledger.ObserveRequest(context.Background(), codexcontinuity.Evidence{Kind: codexcontinuity.KindTurnState, DigestInput: []byte("state")}); err != failure {
		t.Fatalf("resolver error replaced: %v", err)
	}
	foreign := testOwner(t, testClient(t, 2), "a", "codex")
	ledger = NewLedger(Config{ClientScopeCandidates: []codexidentity.ClientScope{client}, APIType: "codex", Resolver: resolverFunc(func(context.Context, codexcontinuity.ResolveRequest) (codexcontinuity.Resolution, error) {
		return codexcontinuity.Resolution{Status: codexcontinuity.ResolutionExpired, Owner: &foreign}, nil
	})})
	if _, err := ledger.ObserveRequest(context.Background(), codexcontinuity.Evidence{Kind: codexcontinuity.KindTurnState, DigestInput: []byte("state")}); !codexcontinuity.IsError(err, codexcontinuity.ErrorConflict) {
		t.Fatalf("expired foreign owner error = %v", err)
	}
}

func TestOpaqueDegradationIsSelective(t *testing.T) {
	for _, kind := range []codexcontinuity.ErrorKind{codexcontinuity.ErrorUnknown, codexcontinuity.ErrorExpired, codexcontinuity.ErrorUnavailable} {
		if !IsOpaqueDegradation(fmt.Errorf("wrapped: %w", &codexcontinuity.Error{Kind: kind})) {
			t.Fatalf("%s was not degradable", kind)
		}
	}
	for _, err := range []error{nil, errors.New("unclassified"), context.Canceled, context.DeadlineExceeded,
		&codexcontinuity.Error{Kind: codexcontinuity.ErrorConflict},
		&codexcontinuity.Error{Kind: codexcontinuity.ErrorCapacity},
		&codexcontinuity.Error{Kind: codexcontinuity.ErrorInvalidInput},
		&codexcontinuity.Error{Kind: codexcontinuity.ErrorInvalidTransition},
	} {
		if IsOpaqueDegradation(err) {
			t.Fatalf("unexpected degradation: %v", err)
		}
	}
}

func testClient(t *testing.T, seed byte) codexidentity.ClientScope {
	t.Helper()
	scope, err := codexidentity.ClientScopeFromDigest("h1", [codexidentity.DigestSize]byte{seed})
	if err != nil {
		t.Fatal(err)
	}
	return scope
}

func testOwner(t *testing.T, client codexidentity.ClientScope, account, apiType string) codexcontinuity.Owner {
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
	return codexcontinuity.Owner{ClientScope: client, ProtocolScope: scope, RouteTargetHint: "route-" + account}
}
