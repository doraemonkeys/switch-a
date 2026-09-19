package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/codex/continuation"
	"github.com/doraemonkeys/switch-a/internal/codex/identity"
	"github.com/doraemonkeys/switch-a/internal/model"
)

func TestCodexContinuationDefaultsForExistingAndNewProviders(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "upgrade.db")
	s, err := NewSQLiteStore(path, internal.RealClock{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CreateProvider(ctx, &model.Provider{ID: "existing", Name: "Existing"}); err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"codex_continuation_outbound", "codex_continuation_inbound"} {
		if err := s.db.Migrator().DropColumn(&model.Provider{}, column); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = NewSQLiteStore(path, internal.RealClock{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.CreateProvider(ctx, &model.Provider{ID: "new", Name: "New"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"existing", "new"} {
		provider, err := s.GetProvider(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if provider.CodexContinuation != (continuation.Policy{Outbound: continuation.Any, Inbound: continuation.None}) {
			t.Fatalf("%s policy = %+v", id, provider.CodexContinuation)
		}
	}
	provider, err := s.GetProvider(ctx, "existing")
	if err != nil {
		t.Fatal(err)
	}
	provider.CodexContinuation = continuation.Policy{Outbound: continuation.None, Inbound: continuation.SameIdentity}
	if err := s.UpdateProvider(ctx, provider); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.GetProvider(ctx, "existing")
	if err != nil || loaded.CodexContinuation != provider.CodexContinuation {
		t.Fatalf("round trip = %+v, %v", loaded, err)
	}
	provider.CodexContinuation.Inbound = "invalid"
	if err := s.UpdateProvider(ctx, provider); err == nil {
		t.Fatal("invalid update accepted")
	}
	provider.ID = "invalid"
	if err := s.CreateProvider(ctx, provider); err == nil {
		t.Fatal("invalid creation accepted")
	}
}

func TestCodexContinuationPortableServingRoutes(t *testing.T) {
	ctx := context.Background()
	source := setupTestStore(t)
	keys := installPortableTestKeyring(t, source, "routes", 37)
	digester, err := codexidentity.NewDigester(keys)
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := source.ClientIdentityResolver(digester)
	if err != nil {
		t.Fatal(err)
	}
	client, err := resolver.Resolve(ctx, []byte("client"))
	if err != nil {
		t.Fatal(err)
	}
	route := continuation.Binding{ClientID: client.ID, Kind: "thread_id", Digest: "thread-digest", ProviderID: "serving", ProtocolScope: "scope", Outbound: continuation.None, UpdatedAt: time.Now().UTC()}
	other := route
	other.ProviderID = "other"
	other.Digest = "other-thread"
	if err := source.CodexContinuationRepository().Save(ctx, []continuation.Binding{route, other}); err != nil {
		t.Fatal(err)
	}
	state, err := source.ExportCodexState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	selected := state.Select([]string{"serving"}, nil)
	if len(selected.ConversationRoutes) != 1 || selected.ConversationRoutes[0].ProviderID != "serving" || len(selected.ClientIdentity.Clients) != 1 || len(selected.ClientIdentity.Aliases) == 0 {
		t.Fatalf("scoped state = %+v", selected)
	}
	target := setupTestStore(t)
	if err := importCodexState(ctx, target.db, selected); err != nil {
		t.Fatal(err)
	}
	loaded, ok, err := target.CodexContinuationRepository().Lookup(ctx, route)
	if err != nil || !ok || loaded.ProviderID != route.ProviderID || loaded.Outbound != continuation.None {
		t.Fatalf("imported binding = %+v, %v", loaded, err)
	}
}
