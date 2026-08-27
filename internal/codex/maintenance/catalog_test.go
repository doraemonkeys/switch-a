package maintenance

import (
	"testing"

	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
)

func TestCatalogSnapshotIsImmutableAndDeduplicatesAuthorities(t *testing.T) {
	subject, err := credentialsession.AccountSubject("account-a")
	if err != nil {
		t.Fatal(err)
	}
	routes := []CatalogRoute{
		{RouteTargetID: "route-a", Vendor: "openai", FinalURL: "https://EXAMPLE.test:443/v1/responses", Subject: subject},
		{RouteTargetID: "route-b", Vendor: "openai", FinalURL: "wss://example.test/socket", Subject: subject},
	}
	snapshot := NewCatalogSnapshot(routes)
	routes[0].FinalURL = "https://mutated.test"
	routes[0].Subject.Value[0] = 'X'
	copyOfRoutes := snapshot.Routes()
	copyOfRoutes[1].Subject.Value[0] = 'Y'

	reachable, err := snapshot.ReachableCookieAuthorities()
	if err != nil {
		t.Fatalf("ReachableCookieAuthorities() error = %v", err)
	}
	if len(reachable) != 1 {
		t.Fatalf("reachable authorities = %d, want one deduplicated Authority", len(reachable))
	}
	if got := reachable[0].Authority().Origin().String(); got != "https://example.test" {
		t.Fatalf("normalized origin = %q", got)
	}
	if got, ok := reachable[0].Authority().Subject().AccountID(); !ok || got != "account-a" {
		t.Fatalf("credential subject = %q, %t", got, ok)
	}
}

func TestCatalogSnapshotDerivesEveryAuthorityDimension(t *testing.T) {
	accountA, _ := credentialsession.AccountSubject("account-a")
	accountB, _ := credentialsession.AccountSubject("account-b")
	snapshot := NewCatalogSnapshot([]CatalogRoute{
		{RouteTargetID: "route-a", Vendor: "openai", FinalURL: "https://a.example", Subject: accountA},
		{RouteTargetID: "route-b", Vendor: "azure", FinalURL: "https://a.example", Subject: accountA},
		{RouteTargetID: "route-c", Vendor: "openai", FinalURL: "https://b.example", Subject: accountA},
		{RouteTargetID: "route-d", Vendor: "openai", FinalURL: "https://a.example", Subject: accountB},
	})
	reachable, err := snapshot.ReachableCookieAuthorities()
	if err != nil {
		t.Fatal(err)
	}
	if len(reachable) != 4 {
		t.Fatalf("reachable authorities = %d, want 4", len(reachable))
	}
	if empty, err := (CatalogSnapshot{}).ReachableCookieAuthorities(); err != nil || len(empty) != 0 {
		t.Fatalf("empty snapshot = %#v, %v", empty, err)
	}
}

func TestCatalogSnapshotRejectsAnyInvalidRouteWithoutReturningPartialSet(t *testing.T) {
	account, _ := credentialsession.AccountSubject("account-a")
	keyed, _ := credentialsession.KeyedDigestSubject("h1", make([]byte, 32))
	tests := []struct {
		name  string
		route CatalogRoute
	}{
		{name: "route target", route: CatalogRoute{Vendor: "openai", FinalURL: "https://example.test", Subject: account}},
		{name: "vendor", route: CatalogRoute{RouteTargetID: "route", FinalURL: "https://example.test", Subject: account}},
		{name: "parse", route: CatalogRoute{RouteTargetID: "route", Vendor: "openai", FinalURL: "://bad", Subject: account}},
		{name: "origin", route: CatalogRoute{RouteTargetID: "route", Vendor: "openai", FinalURL: "https://user@example.test", Subject: account}},
		{name: "pending subject", route: CatalogRoute{RouteTargetID: "route", Vendor: "openai", FinalURL: "https://example.test", Subject: credentialsession.PendingSubject()}},
		{name: "invalid subject", route: CatalogRoute{RouteTargetID: "route", Vendor: "openai", FinalURL: "https://example.test", Subject: credentialsession.Subject{Kind: credentialsession.SubjectKeyedDigest, KeyVersion: "h1", Value: []byte{1}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := NewCatalogSnapshot([]CatalogRoute{
				{RouteTargetID: "valid", Vendor: "openai", FinalURL: "https://valid.example", Subject: keyed},
				test.route,
			})
			if reachable, err := snapshot.ReachableCookieAuthorities(); err == nil || reachable != nil {
				t.Fatalf("ReachableCookieAuthorities() = %#v, %v; want nil error result", reachable, err)
			}
		})
	}
}
