package store

import (
	"context"
	"strings"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/model/providerroute"
)

func TestCodexMaintenanceCatalogJoinsTheExactTransportBinding(t *testing.T) {
	storage := newCredentialSessionStore(t)
	ctx := context.Background()
	createMaintenanceSession(t, storage, "http-session", "http-account")
	createMaintenanceSession(t, storage, "ws-session", "ws-account")
	provider := maintenanceProvider("dual", "http-session", "https://http.example/v1")
	provider.APITypes = append(provider.APITypes, model.ProviderAPIType{
		APIType: "codex", Transport: providerroute.WebSocket, BaseURL: "wss://ws.example/responses",
	})
	provider.CredentialSessions = append(provider.CredentialSessions, credentialsession.RouteSnapshot{
		APIType: "codex", Transport: providerroute.WebSocket,
		Credential: credentialsession.Snapshot{SessionID: "ws-session"},
	})
	if err := storage.CreateProvider(ctx, &provider); err != nil {
		t.Fatal(err)
	}

	snapshot, err := storage.LoadCodexMaintenanceCatalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Routes()) != 2 {
		t.Fatalf("transport join invented routes: got %d, want 2", len(snapshot.Routes()))
	}
	reachable, err := snapshot.ReachableCookieAuthorities()
	if err != nil || len(reachable) != 2 {
		t.Fatalf("reachable authorities: count=%d error=%v", len(reachable), err)
	}
	want := map[string]string{"http-account": "https://http.example", "ws-account": "https://ws.example"}
	for _, item := range reachable {
		account, ok := item.Authority().Subject().AccountID()
		if !ok || item.Authority().Origin().String() != want[account] {
			t.Fatalf("transport credentials crossed origins: %s", item.Authority().Origin().String())
		}
		delete(want, account)
	}
	if len(want) != 0 {
		t.Fatal("missing configured authority")
	}

	// The surviving WS binding must not hide a missing HTTP binding. Maintenance
	// needs a complete catalog before deciding that persisted cookies are orphaned.
	if err := storage.db.Where("route_target_id = ? AND transport = ?", provider.ID, providerroute.HTTP).
		Delete(&credentialsession.RouteBinding{}).Error; err != nil {
		t.Fatal(err)
	}
	snapshot, err = storage.LoadCodexMaintenanceCatalog(ctx)
	if err == nil || !strings.Contains(err.Error(), "codex/http") || len(snapshot.Routes()) != 0 {
		t.Fatalf("incomplete HTTP route: count=%d error=%v", len(snapshot.Routes()), err)
	}
}
