package codexcontinuity_test

import (
	"context"
	"testing"
	"time"

	codexcontinuity "github.com/doraemonkeys/switch-a/internal/codex/continuity"
	codexidentity "github.com/doraemonkeys/switch-a/internal/codex/identity"
	codexprovenance "github.com/doraemonkeys/switch-a/internal/codex/provenance"
	"github.com/doraemonkeys/switch-a/internal/model"
)

func TestRecoveryValidatesRetainedExpiredOwnerAcrossDurableOutage(t *testing.T) {
	for _, outage := range []bool{false, true} {
		t.Run(map[bool]string{false: "sqlite tombstone", true: "provenance fallback"}[outage], func(t *testing.T) {
			fixture := newFixture(t, keyringDocument("h1", map[string]byte{"h1": 1}), policyWith(10, time.Hour))
			defer fixture.close()
			clientA := clientScopes(t, fixture.digester, "client-a")
			clientB := clientScopes(t, fixture.digester, "client-b")
			scopeA := protocolScope(t, "vendor", "https://api.example.com", "account-a", "codex")
			input := evidence(codexcontinuity.KindTurnState, "old-state")
			lease := requirePrepare(t, fixture.service, codexcontinuity.ClaimRequest{
				Evidence: input, Scope: scopeFor(clientA, scopeA, "route-a"), OperationID: "issue",
			})
			if _, err := fixture.service.Commit(context.Background(), lease); err != nil {
				t.Fatal(err)
			}
			fixture.clock.Advance(time.Hour + time.Minute)
			if outage {
				db, err := fixture.db.DB()
				if err != nil {
					t.Fatal(err)
				}
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
			}
			request := codexcontinuity.ResolveRequest{Evidence: input, ClientScopeCandidates: clientA, OperationID: "recover"}
			resolution, err := fixture.service.Resolve(context.Background(), request)
			if err != nil || resolution.Status != codexcontinuity.ResolutionExpired || resolution.Owner == nil || !resolution.Owner.ProtocolScope.Equal(scopeA) {
				t.Fatalf("expired source = %#v, %v", resolution, err)
			}
			request.ClientScopeCandidates = clientB
			if _, err := fixture.service.Resolve(context.Background(), request); !codexcontinuity.IsError(err, codexcontinuity.ErrorConflict) {
				t.Fatalf("expired foreign client = %v", err)
			}
			for _, test := range []struct {
				clients []codexidentity.ClientScope
				apiType string
				wantErr bool
			}{
				{clients: clientA, apiType: "codex"},
				{clients: clientB, apiType: "codex", wantErr: true},
				{clients: clientA, apiType: "responses", wantErr: true},
			} {
				ledger := codexprovenance.NewLedger(codexprovenance.Config{
					Resolver: fixture.service, RecoveryPolicy: model.ConversationRecoverySwitchAccountPreserveConversation,
					ClientScopeCandidates: test.clients, APIType: test.apiType, OperationID: "admit",
				})
				_, err := ledger.ObserveRequest(context.Background(), input)
				if test.wantErr && !codexcontinuity.IsError(err, codexcontinuity.ErrorConflict) {
					t.Fatalf("isolation error = %v", err)
				}
				if !test.wantErr && err != nil {
					t.Fatal(err)
				}
			}
			if _, err := fixture.service.ResolveOwner(context.Background(), codexcontinuity.ResolveRequest{Evidence: input, ClientScopeCandidates: clientA, OperationID: "strict"}); !codexcontinuity.IsError(err, codexcontinuity.ErrorExpired) {
				t.Fatalf("strict expired resolution = %v", err)
			}
		})
	}
}
