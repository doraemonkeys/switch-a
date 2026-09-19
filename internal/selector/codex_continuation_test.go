package selector

import (
	"context"
	"errors"
	"testing"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/codex/continuation"
	"github.com/doraemonkeys/switch-a/internal/codex/continuity"
	"github.com/doraemonkeys/switch-a/internal/codex/identity"
	"github.com/doraemonkeys/switch-a/internal/model"
)

func TestCodexContinuationSelectionBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name                                        string
		outbound, inbound                           continuation.Boundary
		sameIdentity, sourceAvailable, vendorDenied bool
		want                                        string
	}{
		{name: "current route beats strategy", sourceAvailable: true, want: "source"},
		{name: "default destination rejects"},
		{name: "explicit admission", inbound: continuation.Any, want: "target"},
		{name: "source veto", outbound: continuation.None, inbound: continuation.Any},
		{name: "same identity", outbound: continuation.SameIdentity, inbound: continuation.SameIdentity, sameIdentity: true, want: "target"},
		{name: "different identity", outbound: continuation.SameIdentity, inbound: continuation.Any},
		{name: "vendor veto on true failover", inbound: continuation.Any, vendorDenied: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			source := authorityTestProvider("source", "https://api.example.test", "account-a", 100)
			source.Enabled = tc.sourceAvailable
			source.CodexContinuation.Outbound = tc.outbound
			account := "account-b"
			if tc.sameIdentity {
				account = "account-a"
			}
			target := authorityTestProvider("target", "https://api.example.test", account, 1)
			target.CodexContinuation.Inbound = tc.inbound
			scope, err := codexidentity.NewProtocolScope(authorityForProvider(t, source), "codex")
			if err != nil {
				t.Fatal(err)
			}
			service := &continuation.Service{Policy: func(_ context.Context, id string) (continuation.Policy, bool, error) {
				return source.CodexContinuation, id == source.ID, nil
			}}
			session := service.Begin("client", tc.name)
			if err := session.Observe(ctx, codexcontinuity.Evidence{Kind: codexcontinuity.KindThreadID, DigestInput: []byte("thread")}, codexcontinuity.Resolution{Owner: &codexcontinuity.Owner{RouteTargetHint: source.ID, ProtocolScope: scope}}); err != nil {
				t.Fatal(err)
			}
			store := newMockStore()
			store.providers = []model.Provider{target, source}
			req := &model.SelectRequest{APIType: "codex", CodexContinuation: session}
			if tc.vendorDenied {
				req.SwitchMode = model.SwitchModeFailover
				req.ProviderContinuityContext = &model.ProviderContinuityContext{VisibleOriginProviderID: source.ID, VisibleOriginVendor: source.Vendor, StrictestScope: model.ScopeNone}
			}
			selected, err := NewSelector(Config{Store: store}).SelectWithMetadata(ctx, req)
			if tc.want == "" {
				if !errors.Is(err, internal.ErrNoProvider) {
					t.Fatalf("selection = %+v, %v", selected, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer selected.Lease.Release()
			if selected.Provider().ID != tc.want {
				t.Fatalf("selected %s, want %s", selected.Provider().ID, tc.want)
			}
		})
	}
}
