package clientdisguiseapi

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/codex/clientdisguise"
	"github.com/doraemonkeys/switch-a/internal/codex/clientidentity"
	codexidentity "github.com/doraemonkeys/switch-a/internal/codex/identity"
	codexkeyring "github.com/doraemonkeys/switch-a/internal/codex/keyring"
	"github.com/doraemonkeys/switch-a/internal/store"
	"github.com/google/uuid"
	"path/filepath"
	"testing"
)

func realAdministration(t *testing.T) (*Handler, *store.SQLiteStore, *clientidentity.Resolver) {
	t.Helper()
	ctx := context.Background()
	persistence, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "admin.db"), internal.RealClock{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = persistence.Close() })
	document, err := codexkeyring.GenerateDocument(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keyring, err := codexkeyring.Parse(document, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := persistence.InstallCodexKeyring(ctx, keyring); err != nil {
		t.Fatal(err)
	}
	digester, err := codexidentity.NewDigester(keyring)
	if err != nil {
		t.Fatal(err)
	}
	clients, err := persistence.ClientIdentityResolver(&digester)
	if err != nil {
		t.Fatal(err)
	}
	return NewHandler(Config{Repository: persistence.ClientDisguiseRepository(), Catalog: persistence, Clients: clients}), persistence, clients
}
func TestApplicationSampleOptionalIDPersistsAndDeduplicates(t *testing.T) {
	handler, persistence, clients := realAdministration(t)
	ctx := context.Background()
	client, err := clients.Resolve(ctx, []byte("reference-client"))
	if err != nil {
		t.Fatal(err)
	}
	response := invoke(handler.SaveReference, fmt.Sprintf(`{"name":"Reference","client_identity_id":%q}`, client.ID))
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	body := `{"source_id":"login","captured_at":"2026-09-05T00:00:00Z","tuple":{"client_type":"desktop","platform":"windows","arch":"amd64"},"client_version":"1.0.0","features":{"user_agent":"example/1.0.0"}}`
	response = invoke(handler.ImportSample, body)
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	var first clientdisguise.LearnResult
	if err := json.Unmarshal(response.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if !first.Created || first.Revision.ID == "" {
		t.Fatal(first)
	}
	snapshot, err := persistence.ClientDisguiseRepository().Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Samples) != 1 {
		t.Fatal(snapshot.Samples)
	}
	if _, err := uuid.Parse(snapshot.Samples[0].ID); err != nil {
		t.Fatal("server sample ID is not UUID", err)
	}
	response = invoke(handler.ImportSample, body)
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	var repeated clientdisguise.LearnResult
	if err := json.Unmarshal(response.Body.Bytes(), &repeated); err != nil {
		t.Fatal(err)
	}
	if repeated.Created || repeated.Revision.ID != first.Revision.ID {
		t.Fatal(repeated)
	}
}
func TestReferenceClientRelationshipSurvivesBackupRoundTrip(t *testing.T) {
	handler, persistence, clients := realAdministration(t)
	ctx := context.Background()
	invalid := `{"name":"Reference","client_identity_id":"missing-client"}`
	if response := invoke(handler.SaveReference, invalid); response.Code != 404 {
		t.Fatal(response.Code, response.Body.String())
	}
	references, err := persistence.ClientDisguiseRepository().ListReferences(ctx)
	if err != nil || len(references) != 0 {
		t.Fatal(references, err)
	}
	client, err := clients.Resolve(ctx, []byte("reference-client"))
	if err != nil {
		t.Fatal(err)
	}
	response := invoke(handler.SaveReference, fmt.Sprintf(`{"name":"Reference","client_identity_id":%q}`, client.ID))
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	if response := invoke(handler.SaveReference, invalid); response.Code != 404 {
		t.Fatal(response.Code)
	}
	references, err = persistence.ClientDisguiseRepository().ListReferences(ctx)
	if err != nil || len(references) != 1 || references[0].ClientIdentityID != client.ID {
		t.Fatal(references, err)
	}
	snapshot, err := persistence.ExportCodexState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, restored, _ := realAdministration(t)
	if err := restored.ApplyConfigImport(ctx, &store.ConfigImportBundle{CodexState: snapshot, RoutingPolicyMode: store.ConfigImportRoutingPolicyModePreserve}); err != nil {
		t.Fatal("accepted reference failed backup restore", err)
	}
	imported, err := restored.ClientDisguiseRepository().ListReferences(ctx)
	if err != nil || len(imported) != 1 || imported[0].ClientIdentityID != client.ID {
		t.Fatal(imported, err)
	}
}
