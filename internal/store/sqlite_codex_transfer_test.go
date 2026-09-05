package store

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/doraemonkeys/switch-a/internal/codex/clientidentity"
	"slices"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/clientdisguise"
	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	codexidentity "github.com/doraemonkeys/switch-a/internal/codex/identity"
	codexkeyring "github.com/doraemonkeys/switch-a/internal/codex/keyring"
	"github.com/doraemonkeys/switch-a/internal/model"
)

func portableTestKeyring(t *testing.T, version string, seed byte) *codexkeyring.Keyring {
	t.Helper()
	material := func(value byte) string { return base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{value}, 32)) }
	document := fmt.Sprintf(`{"schema_version":1,"hmac":{"current":%q,"keys":{%q:%q}},"aead":{"current":%q,"keys":{%q:%q}}}`, version, version, material(seed), "a-"+version, "a-"+version, material(seed+1))
	keyring, err := codexkeyring.Parse([]byte(document), bytes.NewReader(bytes.Repeat([]byte{seed + 2}, 256)))
	if err != nil {
		t.Fatal(err)
	}
	return keyring
}
func installPortableTestKeyring(t *testing.T, s *SQLiteStore, version string, seed byte) *codexkeyring.Keyring {
	t.Helper()
	keyring := portableTestKeyring(t, version, seed)
	if err := s.InstallCodexKeyring(context.Background(), keyring); err != nil {
		t.Fatal(err)
	}
	if err := s.FinalizeStaticCredentialSubjects(context.Background(), keyring); err != nil {
		t.Fatal(err)
	}
	return keyring
}
func TestPortableStateRoundTripPreviewRollbackAndBinding(t *testing.T) {
	ctx := context.Background()
	source := setupTestStore(t)
	sourceKeys := installPortableTestKeyring(t, source, "source", 11)
	digester, err := codexidentity.NewDigester(sourceKeys)
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := source.ClientIdentityResolver(digester)
	if err != nil {
		t.Fatal(err)
	}
	client, err := resolver.Resolve(ctx, []byte("old-client-key"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.BindKey(ctx, []byte("replacement-key"), client.ID); err != nil {
		t.Fatal(err)
	}
	active := configImportChatGPTSession(t, "login", "token", "account")
	if _, err := source.CreateCredentialSession(ctx, active); err != nil {
		t.Fatal(err)
	}
	logins, err := source.ClientDisguiseRepository().ListLogins(ctx)
	if err != nil || len(logins) != 1 {
		t.Fatal(logins, err)
	}
	state, err := source.ExportCodexState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	state.Disguise.Mappings = []clientdisguise.Mapping{{MappingKey: clientdisguise.MappingKey{GenerationID: logins[0].GenerationID, ClientIdentityID: client.ID, Namespace: "thread", Original: "old-thread"}, Mapped: "mapped-thread"}}
	serialized, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	var portable CodexState
	if err := json.Unmarshal(serialized, &portable); err != nil {
		t.Fatal(err)
	}
	target := setupTestStore(t)
	targetKeys := installPortableTestKeyring(t, target, "target", 21)
	placeholder := credentialsession.Session{ID: "login", Name: "Restored", Kind: credentialsession.KindChatGPT, Version: 1, SubjectKind: credentialsession.SubjectPending, AuthState: credentialsession.AuthState{Status: credentialsession.AuthStatusReauthRequired, StatusReason: "restore"}}
	bundle := &ConfigImportBundle{CodexState: &portable, CredentialSessions: []credentialsession.Session{placeholder}, RoutingPolicyMode: ConfigImportRoutingPolicyModePreserve}
	if err := target.PreviewConfigImport(ctx, bundle); err != nil {
		t.Fatal("preview", err)
	}
	clients, err := target.db.Table("codex_client_identities").Rows()
	if err != nil {
		t.Fatal(err)
	}
	if clients.Next() {
		t.Fatal("preview committed identity")
	}
	_ = clients.Close()
	if len(targetKeys.Capabilities().HMACVersions) != 1 {
		t.Fatal("preview published keys")
	}
	for range 2 {
		if err := target.ApplyConfigImport(ctx, bundle); err != nil {
			t.Fatal("import", err)
		}
	}
	restored, err := target.GetCredentialSession(ctx, "login")
	if err != nil || !restored.IsReauthenticationPlaceholder() {
		t.Fatal(restored, err)
	}
	targetDigester, err := codexidentity.NewDigester(targetKeys)
	if err != nil {
		t.Fatal(err)
	}
	targetResolver, err := target.ClientIdentityResolver(targetDigester)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := targetResolver.Resolve(ctx, []byte("replacement-key"))
	if err != nil {
		t.Fatal(err)
	}
	if bound.ID != client.ID || !bound.Primary.Equal(client.Primary) {
		t.Fatal("identity affinity changed", bound)
	}
	imported, err := target.ExportCodexState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(imported.Disguise.Logins) != 1 || imported.Disguise.Logins[0].DeviceID != logins[0].DeviceID || len(imported.Disguise.Mappings) != 1 {
		t.Fatal("identity history changed")
	}
	// Persisted imported keys are sufficient to rebuild the live ring at restart.
	restartedKeys := portableTestKeyring(t, "target", 21)
	if err := target.InstallCodexKeyring(ctx, restartedKeys); err != nil {
		t.Fatal(err)
	}
	if len(restartedKeys.Capabilities().HMACVersions) != 2 {
		t.Fatal("portable keys missing after restart")
	}
	portable.ClientIdentity.Clients[0].PrimaryDigest[0] ^= 1
	if err := target.ApplyConfigImport(ctx, bundle); err == nil {
		t.Fatal("immutable identity conflict accepted")
	}
}

func TestPortableStaticCredentialKeepsSourceAuthority(t *testing.T) {
	ctx := context.Background()
	source := setupTestStore(t)
	installPortableTestKeyring(t, source, "source-static", 31)
	session := &credentialsession.Session{ID: "static", Name: "Static", Kind: credentialsession.KindAPIKey, SecretData: "upstream-secret", Version: 1, SubjectKind: credentialsession.SubjectPending, AuthState: credentialsession.AuthState{Status: credentialsession.AuthStatusActive}}
	created, err := source.CreateCredentialSession(ctx, session)
	if err != nil {
		t.Fatal(err)
	}
	state, err := source.ExportCodexState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	target := setupTestStore(t)
	installPortableTestKeyring(t, target, "target-static", 41)
	if err := target.ApplyConfigImport(ctx, &ConfigImportBundle{CodexState: state, CredentialSessions: []credentialsession.Session{*created}, RoutingPolicyMode: ConfigImportRoutingPolicyModePreserve}); err != nil {
		t.Fatal(err)
	}
	actual, err := target.GetCredentialSession(ctx, "static")
	if err != nil {
		t.Fatal(err)
	}
	if !actual.Subject().Equal(created.Subject()) {
		t.Fatal("source static authority was replaced")
	}
}

func TestPortableConflictsRollBackConfigurationAndKeys(t *testing.T) {
	ctx := context.Background()
	source := setupTestStore(t)
	keys := installPortableTestKeyring(t, source, "source-conflict", 51)
	digester, _ := codexidentity.NewDigester(keys)
	resolver, _ := source.ClientIdentityResolver(digester)
	if _, err := resolver.Resolve(ctx, []byte("client")); err != nil {
		t.Fatal(err)
	}
	state, err := source.ExportCodexState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	target := setupTestStore(t)
	targetKeys := installPortableTestKeyring(t, target, "target-conflict", 61)
	state.ClientIdentity.Aliases[0].ClientID = "nonexistent"
	bundle := &ConfigImportBundle{CodexState: state, Groups: []model.Group{{ID: "must-rollback", Name: "Rollback", Strategy: "priority", Weight: 1, Enabled: true}}, RoutingPolicyMode: ConfigImportRoutingPolicyModePreserve}
	if err := target.ApplyConfigImport(ctx, bundle); err == nil {
		t.Fatal("invalid graph imported")
	}
	var count int64
	if err := target.db.Table("codex_client_identities").Count(&count).Error; err != nil || count != 0 {
		t.Fatal("client rows partially imported", count, err)
	}
	if len(targetKeys.Capabilities().HMACVersions) != 1 {
		t.Fatal("failed import published keys")
	}
	if _, err := target.GetGroup(ctx, "must-rollback"); err == nil {
		t.Fatal("failed import committed config")
	}
}

func TestPortableStickySelectionAndCommittedObserver(t *testing.T) {
	ctx := context.Background()
	source := setupTestStore(t)
	keys := installPortableTestKeyring(t, source, "sticky-source", 71)
	digester, _ := codexidentity.NewDigester(keys)
	resolver, _ := source.ClientIdentityResolver(digester)
	firstClient, err := resolver.Resolve(ctx, []byte("first-client"))
	if err != nil {
		t.Fatal(err)
	}
	secondClient, err := resolver.Resolve(ctx, []byte("second-client"))
	if err != nil {
		t.Fatal(err)
	}
	providers := []*model.Provider{}
	for _, id := range []string{"first-provider", "second-provider"} {
		provider := credentialBackedTestProvider(t, source, &model.Provider{ID: id, Name: id, Enabled: true, APITypes: []model.ProviderAPIType{{ProviderID: id, APIType: "codex", BaseURL: "https://api.example"}}})
		if err := source.CreateProvider(ctx, provider); err != nil {
			t.Fatal(err)
		}
		providers = append(providers, provider)
	}
	clients := []clientidentity.Resolution{firstClient, secondClient}
	for i, client := range clients {
		encoded, err := client.Primary.MarshalBinary()
		if err != nil {
			t.Fatal(err)
		}
		entry := model.StickyEntry{Key: model.StickyKey{APIType: "codex", Model: "model", ClientScope: hex.EncodeToString(encoded)}, ProviderID: providers[i].ID, ExpiresAt: source.clock.Now().Add(time.Hour)}
		if err := source.UpsertStickyEntry(ctx, entry); err != nil {
			t.Fatal(err)
		}
	}
	state, err := source.ExportCodexState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	scoped := state.Select([]string{providers[0].ID}, providers[0].CredentialSessionIDs())
	if len(scoped.Sticky) != 1 || len(scoped.ClientIdentity.Clients) != 1 || scoped.ClientIdentity.Clients[0].ID != firstClient.ID {
		t.Fatal("selected sticky client closure lost", scoped)
	}
	target := setupTestStore(t)
	installPortableTestKeyring(t, target, "sticky-target", 81)
	restoredCount := 0
	target.SetCodexStickyRestorer(func(entries []model.StickyEntry) { restoredCount += len(entries) })
	sessions, err := source.ListCredentialSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	selectedSessions := []credentialsession.Session{}
	for _, session := range sessions {
		if slices.Contains(providers[0].CredentialSessionIDs(), session.ID) {
			selectedSessions = append(selectedSessions, session)
		}
	}
	bundle := &ConfigImportBundle{CodexState: scoped, CredentialSessions: selectedSessions, Providers: []model.Provider{*providers[0]}, RoutingPolicyMode: ConfigImportRoutingPolicyModePreserve}
	if err := target.PreviewConfigImport(ctx, bundle); err != nil {
		t.Fatal(err)
	}
	if restoredCount != 0 {
		t.Fatal("preview notified sticky cache")
	}
	if err := target.ApplyConfigImport(ctx, bundle); err != nil {
		t.Fatal(err)
	}
	if restoredCount != 1 {
		t.Fatal("commit did not notify sticky cache")
	}
	entries, err := target.LoadStickyEntries(ctx, target.clock.Now())
	if err != nil || len(entries) != 1 || entries[0].ProviderID != providers[0].ID {
		t.Fatal(entries, err)
	}
	scoped.Sticky[0].ProviderID = "missing"
	if err := target.ApplyConfigImport(ctx, bundle); err == nil {
		t.Fatal("invalid provider imported")
	}
	if restoredCount != 1 {
		t.Fatal("failed transaction notified sticky cache")
	}
}

func TestPortablePinnedRevisionRetainsSourceTrack(t *testing.T) {
	tuple := clientdisguise.Tuple{ClientType: "desktop", Platform: "windows", Arch: "amd64"}
	captured := time.Date(2026, time.September, 5, 0, 0, 0, 0, time.UTC)
	state := &CodexState{Version: CodexStateVersion, Disguise: clientdisguise.Snapshot{
		Bindings: []clientdisguise.ProfileBinding{{CredentialSessionID: "selected", Mode: clientdisguise.ModePinned, Tuple: tuple, RevisionID: "pinned"}},
		Profiles: []clientdisguise.ProfileRevision{{ID: "pinned", SourceID: "reference", Tuple: tuple}, {ID: "head", SourceID: "reference", Tuple: tuple}, {ID: "unrelated", SourceID: "other", Tuple: tuple}},
		Tracks:   []clientdisguise.ProfileTrack{{SourceID: "reference", ClientType: tuple.ClientType, Platform: tuple.Platform, Arch: tuple.Arch, RevisionID: "head", ClientVersion: "2.0.0", CapturedAt: captured}, {SourceID: "other", RevisionID: "unrelated"}},
	}}
	selected := state.Select(nil, []string{"selected"})
	if len(selected.Disguise.Tracks) != 1 || selected.Disguise.Tracks[0].RevisionID != "head" || !selected.Disguise.Tracks[0].CapturedAt.Equal(captured) {
		t.Fatal("pinned revision lost reference learning watermark", selected.Disguise.Tracks)
	}
	if len(selected.Disguise.Profiles) != 2 {
		t.Fatal("selected revision closure included unrelated profiles", selected.Disguise.Profiles)
	}
}
