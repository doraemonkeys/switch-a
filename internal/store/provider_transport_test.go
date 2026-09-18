package store

import (
	"context"
	"errors"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/model"
)

func TestProviderTransportBindingsPersistAndUpdateIndependently(t *testing.T) {
	store := newCredentialSessionStore(t)
	ctx := context.Background()
	mustCreateStaticSession(t, store, "http-session", "", "http-key")
	mustCreateStaticSession(t, store, "ws-session", "", "ws-key")
	provider := providerWithSessionRefs("dual", "vendor", map[string]string{"codex": "http-session"})
	provider.APITypes = append(provider.APITypes, model.ProviderAPIType{ProviderID: provider.ID, APIType: "codex", Transport: "websocket", BaseURL: "https://ws.example.test"})
	provider.CredentialSessions = append(provider.CredentialSessions, credentialsession.RouteSnapshot{RouteTargetID: provider.ID, APIType: "codex", Transport: "websocket", Credential: credentialsession.Snapshot{SessionID: "ws-session"}})
	if err := store.CreateProvider(ctx, provider); err != nil {
		t.Fatal(err)
	}
	for transport, key := range map[string]string{"http": "http-key", "websocket": "ws-key"} {
		route, err := store.ResolveCredentialSession(ctx, provider.ID, "codex", transport)
		if err != nil || route.Transport != transport || route.Credential.SecretData != key {
			t.Fatalf("%s: %+v %v", transport, route, err)
		}
	}
	providers, err := store.ListProvidersByAPIType(ctx, "codex")
	if err != nil || len(providers) != 1 || len(providers[0].APITypes) != 2 {
		t.Fatal(providers, err)
	}
	// Disabling HTTP must not remove the WS binding or either reusable session.
	provider.APITypes = provider.APITypes[1:]
	provider.CredentialSessions = provider.CredentialSessions[1:]
	if err := store.UpdateProvider(ctx, provider); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ResolveCredentialSession(ctx, provider.ID, "codex", "http"); !errors.Is(err, credentialsession.ErrNotFound) {
		t.Fatal("HTTP still bound", err)
	}
	if _, err := store.ResolveCredentialSession(ctx, provider.ID, "codex", "websocket"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetCredentialSession(ctx, "http-session"); err != nil {
		t.Fatal("detached key was deleted", err)
	}
	refs, err := store.CredentialSessionRouteReferences(ctx, "ws-session")
	if err != nil || len(refs) != 1 || refs[0].Transport != "websocket" {
		t.Fatal(refs, err)
	}
}
