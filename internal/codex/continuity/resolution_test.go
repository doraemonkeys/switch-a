package codexcontinuity

import (
	"context"
	"errors"
	"testing"

	codexidentity "github.com/doraemonkeys/switch-a/internal/codex/identity"
)

func TestResolveSeparatesSourceFromAvailability(t *testing.T) {
	client := testClientScope(t, 1, "h1")
	digest := testOpaqueDigest(t, KindTurnState, 1, "h1")
	owner := Owner{ClientScope: client, ProtocolScope: testScope(t, "account-a", "codex"), RouteTargetHint: "route-a"}
	for _, test := range []struct {
		name        string
		decision    StoreDecision
		storeErr    error
		retainOwner bool
		want        ResolutionStatus
		wantErr     ErrorKind
	}{
		{name: "owned", decision: StoreOwned, retainOwner: true, want: ResolutionOwned},
		{name: "unknown", decision: StoreUnknown, want: ResolutionUnknown},
		{name: "expired owner", decision: StoreExpired, retainOwner: true, want: ResolutionExpired},
		{name: "expired without owner", decision: StoreExpired, want: ResolutionExpired},
		{name: "outage", storeErr: errors.New("database offline"), want: ResolutionUnavailable},
		{name: "conflict", decision: StoreConflict, retainOwner: true, wantErr: ErrorConflict},
		{name: "capacity", decision: StoreCapacity, wantErr: ErrorCapacity},
		{name: "invalid decision", decision: StoreDecision("invalid"), wantErr: ErrorInvalidTransition},
		{name: "canceled", storeErr: context.Canceled, wantErr: ErrorUnavailable},
		{name: "deadline", storeErr: context.DeadlineExceeded, wantErr: ErrorUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &stubStore{lookup: func(StoreLookup) (StoreResult, error) {
				result := StoreResult{Decision: test.decision}
				if test.retainOwner {
					result.Binding = Binding{Kind: KindTurnState, Digest: digest, Owner: owner}
				}
				return result, test.storeErr
			}}
			service, err := NewService(Config{Store: store, Digester: stubDigester{candidates: []codexidentity.OpaqueDigest{digest}}, Policy: mustPolicy(t)})
			if err != nil {
				t.Fatal(err)
			}
			request := ResolveRequest{Evidence: Evidence{Kind: KindTurnState, DigestInput: []byte("state")}, ClientScopeCandidates: []codexidentity.ClientScope{client}, OperationID: "resolve"}
			resolution, err := service.Resolve(context.Background(), request)
			if test.wantErr != "" {
				if !IsError(err, test.wantErr) {
					t.Fatalf("error = %v", err)
				}
				return
			}
			if err != nil || resolution.Status != test.want || (resolution.Owner != nil) != test.retainOwner {
				t.Fatalf("resolution = %#v, %v", resolution, err)
			}
			if resolution.Owner != nil && !resolution.Owner.Equal(owner) {
				t.Fatal("source owner changed")
			}
			if test.retainOwner {
				request.ClientScopeCandidates = []codexidentity.ClientScope{testClientScope(t, 2, "h1")}
				if _, err := service.Resolve(context.Background(), request); !IsError(err, ErrorConflict) {
					t.Fatalf("foreign client admitted retained owner: %v", err)
				}
			}
		})
	}
}

func TestResolveInvalidEvidenceDoesNotBecomeOpaque(t *testing.T) {
	digest := testOpaqueDigest(t, KindTurnState, 1, "h1")
	service, err := NewService(Config{Store: &stubStore{}, Digester: stubDigester{candidates: []codexidentity.OpaqueDigest{digest}}, Policy: mustPolicy(t)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Resolve(context.Background(), ResolveRequest{}); !IsError(err, ErrorInvalidInput) {
		t.Fatalf("invalid evidence resolution = %v", err)
	}
}

func TestResolveOwnerRetainsStrictExpiredContract(t *testing.T) {
	client := testClientScope(t, 1, "h1")
	digest := testOpaqueDigest(t, KindTurnState, 1, "h1")
	store := &stubStore{lookup: func(StoreLookup) (StoreResult, error) {
		return StoreResult{Decision: StoreExpired, Binding: Binding{Kind: KindTurnState, Owner: Owner{ClientScope: client, ProtocolScope: testScope(t, "a", "codex")}}}, nil
	}}
	service, err := NewService(Config{Store: store, Digester: stubDigester{candidates: []codexidentity.OpaqueDigest{digest}}, Policy: mustPolicy(t)})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := service.ResolveOwner(context.Background(), ResolveRequest{Evidence: Evidence{Kind: KindTurnState, DigestInput: []byte("state")}, ClientScopeCandidates: []codexidentity.ClientScope{client}, OperationID: "strict"})
	if !IsError(err, ErrorExpired) || binding.Kind != "" {
		t.Fatalf("strict adapter changed: %#v, %v", binding, err)
	}
}
