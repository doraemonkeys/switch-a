package admin

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/store"
)

func TestConfigImportSnapshotIncludesUnreferencedCredentialSessions(t *testing.T) {
	handler, st, _ := testHandler()
	exported := importedTestSession("orphan-session", "orphan-secret")
	session := &credentialsession.Session{
		ID: exported.ID, Vendor: exported.Vendor, Kind: exported.Kind,
		SecretData: exported.SecretData, Version: exported.Version, AuthState: exported.AuthState,
	}
	if err := session.SetSubject(exported.Subject); err != nil {
		t.Fatal(err)
	}
	st.credentialSessions[session.ID] = session

	snapshot, err := handler.loadConfigImportSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := snapshot.credentialSessions[session.ID]; !exists {
		t.Fatal("unreferenced credential session was absent from import snapshot")
	}
	staged := stageConfigImport(
		&ImportConfigRequest{ImportScope: fullConfigImportScope(), CredentialSessions: []ExportedCredentialSession{exported}},
		snapshot.providers,
		snapshot.credentialSessions,
		snapshot.groups,
		snapshot.routingPolicies,
		snapshot.settings,
		snapshot.rules,
	)
	if staged.changes.CredentialSessions != (ChangeCount{Unchanged: 1}) {
		t.Fatalf("credential session changes = %+v, want one unchanged", staged.changes.CredentialSessions)
	}
}

func TestResolveImportConfigRequest_SelectionKeepsProviderImportsExactAndNormalizesScopeIDs(t *testing.T) {
	groupA := "group-a"
	groupB := "group-b"
	req := &ImportConfigRequest{
		CredentialSessions: []ExportedCredentialSession{
			importedTestSession("provider-z-session", "key-z"), importedTestSession("provider-a-session", "key-a"),
			importedTestSession("provider-b-session", "key-b"), importedTestSession("provider-c-session", "key-c"),
		},
		ImportScope: selectionConfigImportScope(
			[]string{" " + groupB + " ", groupB},
			[]string{" provider-a ", "provider-a"},
		),
		Groups: []ExportedGroup{
			{ID: "group-z", Name: "Ignored Group", Strategy: DefaultStrategy, Weight: DefaultWeight, Enabled: true},
			{ID: groupB, Name: "Selected Group", Strategy: DefaultStrategy, Weight: DefaultWeight, Enabled: true},
			{ID: groupA, Name: "Auto Included Group", Strategy: DefaultStrategy, Weight: DefaultWeight, Enabled: true},
		},
		Providers: []ExportedProvider{
			scopeTestProvider("provider-z", "Ignored Provider", "claude", "https://ignored.example", nil),
			scopeTestProvider("provider-a", "Direct Provider", "codex", "https://direct.example", &groupA),
			scopeTestProvider("provider-b", "Group Provider", "claude", "https://group.example", &groupB),
			scopeTestProvider("provider-c", "Sibling Provider", "claude", "https://sibling.example", &groupA),
		},
		RoutingPolicies: []ExportedRoutingPolicy{
			{APIType: "claude", Enabled: true},
		},
		Settings: map[string]string{
			configStickyModeKey: "api_type",
		},
	}

	resolved, warnings := resolveImportConfigRequest(req)

	if !resolved.CanStage {
		t.Fatal("CanStage = false, want true")
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none", warnings)
	}
	if !reflect.DeepEqual(resolved.Scope.GroupIDs, []string{groupB}) {
		t.Fatalf("Scope.GroupIDs = %v, want [%q]", resolved.Scope.GroupIDs, groupB)
	}
	if !reflect.DeepEqual(resolved.Scope.ProviderIDs, []string{"provider-a"}) {
		t.Fatalf("Scope.ProviderIDs = %v, want [provider-a]", resolved.Scope.ProviderIDs)
	}
	if got := exportedGroupIDs(resolved.Groups); !reflect.DeepEqual(got, []string{groupB, groupA}) {
		t.Fatalf("resolved group IDs = %v, want [%q %q]", got, groupB, groupA)
	}
	if got := exportedProviderIDs(resolved.Providers); !reflect.DeepEqual(got, []string{"provider-a", "provider-b"}) {
		t.Fatalf("resolved provider IDs = %v, want [provider-a provider-b]", got)
	}
	if !reflect.DeepEqual(resolved.RuleProviderIDs, []string{"provider-a", "provider-b"}) {
		t.Fatalf("rule provider partition = %v, want [provider-a provider-b]", resolved.RuleProviderIDs)
	}
	if len(resolved.RoutingPolicies) != 0 {
		t.Fatalf("resolved routing policies = %v, want none", resolved.RoutingPolicies)
	}
	if len(resolved.Settings) != 0 {
		t.Fatalf("resolved settings = %v, want none", resolved.Settings)
	}
}

func TestResolveImportConfigRequest_SelectionWarnsWhenSelectedProviderGroupIsMissingFromFile(t *testing.T) {
	missingGroupID := "group-missing"
	req := &ImportConfigRequest{
		ImportScope: selectionConfigImportScope(nil, []string{"provider-a"}),
		CredentialSessions: []ExportedCredentialSession{
			importedTestSession("provider-a-session", "key-a"),
		},
		Providers: []ExportedProvider{
			scopeTestProvider("provider-a", "Selected Provider", "codex", "https://selected.example", &missingGroupID),
		},
	}

	resolved, warnings := resolveImportConfigRequest(req)

	if !resolved.CanStage {
		t.Fatal("CanStage = false, want true")
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %v, want 1 warning", warnings)
	}
	if warnings[0] != `Selected provider "provider-a" references group "group-missing", but that group is missing from the import file` {
		t.Fatalf("warning = %q, want missing-group warning", warnings[0])
	}
	if got := exportedProviderIDs(resolved.Providers); !reflect.DeepEqual(got, []string{"provider-a"}) {
		t.Fatalf("resolved provider IDs = %v, want [provider-a]", got)
	}
	if len(resolved.Groups) != 0 {
		t.Fatalf("resolved groups = %v, want none", resolved.Groups)
	}
}

func TestResolveImportConfigRequest_RejectsUnsupportedMode(t *testing.T) {
	req := &ImportConfigRequest{
		ImportScope: &ConfigImportScope{
			Mode: ConfigImportMode("partial"),
		},
	}

	resolved, warnings := resolveImportConfigRequest(req)

	if resolved.CanStage {
		t.Fatal("CanStage = true, want false")
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %v, want 1 warning", warnings)
	}
	if warnings[0] != `Import scope mode must be one of "full", "settings_only", "selection"` {
		t.Fatalf("warning = %q, want unsupported-mode message", warnings[0])
	}
}

func TestResolveImportConfigRequest_SettingsOnlyWarnsOnScopedIDs(t *testing.T) {
	req := &ImportConfigRequest{
		ImportScope: &ConfigImportScope{
			Mode: ConfigImportModeSettingsOnly,
			Selection: &ConfigImportSelection{
				GroupIDs:    []string{"group-a"},
				ProviderIDs: []string{"provider-a"},
			},
		},
		Providers: []ExportedProvider{
			scopeTestProvider("provider-a", "Ignored Provider", "codex", "https://ignored.example", nil),
		},
		Groups: []ExportedGroup{
			{ID: "group-a", Name: "Ignored Group", Strategy: DefaultStrategy, Weight: DefaultWeight, Enabled: true},
		},
		Settings: map[string]string{
			configStickyModeKey: "api_type",
		},
	}

	resolved, warnings := resolveImportConfigRequest(req)

	if resolved.CanStage {
		t.Fatal("CanStage = true, want false")
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %v, want 1 warning", warnings)
	}
	if warnings[0] != `Import scope mode "settings_only" does not allow selection` {
		t.Fatalf("warning = %q, want scoped-id rejection", warnings[0])
	}
	if len(resolved.Providers) != 0 {
		t.Fatalf("resolved providers = %v, want none", resolved.Providers)
	}
	if len(resolved.Groups) != 0 {
		t.Fatalf("resolved groups = %v, want none", resolved.Groups)
	}
	if len(resolved.Settings) != 0 {
		t.Fatalf("resolved settings = %v, want none when scope contract is invalid", resolved.Settings)
	}
}

func TestStageConfigImport_SetsExplicitRoutingPolicyMode(t *testing.T) {
	groupID := "group-a"
	targetProviderID := "provider-a"
	existingProviders := map[string]*model.Provider{
		targetProviderID: storedTestProvider(targetProviderID, "Target Provider", "claude", "https://claude.example"),
	}
	existingRoutingPolicies := map[model.RoutingPolicyNaturalKey]*model.RoutingPolicy{
		model.NewRoutingPolicyNaturalKey("claude", model.RoutingPolicyModelMatchTypeNone, ""): {
			APIType:          "claude",
			Enabled:          true,
			TargetProviderID: &targetProviderID,
		},
	}

	fullStaged := stageConfigImport(
		&ImportConfigRequest{
			ImportScope: fullConfigImportScope(),
			RoutingPolicies: []ExportedRoutingPolicy{
				{APIType: "claude", Enabled: true, TargetProviderID: &targetProviderID},
			},
		},
		existingProviders,
		nil,
		nil,
		nil,
		nil,
		nil,
	)
	if fullStaged.bundle.RoutingPolicyMode != store.ConfigImportRoutingPolicyModeReplace {
		t.Fatalf("full routing policy mode = %q, want %q", fullStaged.bundle.RoutingPolicyMode, store.ConfigImportRoutingPolicyModeReplace)
	}
	if len(fullStaged.bundle.RoutingPolicies) != 1 {
		t.Fatalf("full routing policies = %v, want 1 imported policy", fullStaged.bundle.RoutingPolicies)
	}

	selectionStaged := stageConfigImport(
		&ImportConfigRequest{
			ImportScope: selectionConfigImportScope([]string{groupID}, nil),
			CredentialSessions: []ExportedCredentialSession{
				importedTestSession("provider-b-session", "key-b"),
			},
			Groups: []ExportedGroup{
				{ID: groupID, Name: "Group A", Strategy: DefaultStrategy, Weight: DefaultWeight, Enabled: true},
			},
			Providers: []ExportedProvider{
				scopeTestProvider("provider-b", "Provider B", "codex", "https://codex.example", &groupID),
			},
		},
		existingProviders,
		nil,
		nil,
		existingRoutingPolicies,
		nil,
		nil,
	)
	if selectionStaged.bundle.RoutingPolicyMode != store.ConfigImportRoutingPolicyModePreserve {
		t.Fatalf("selection routing policy mode = %q, want %q", selectionStaged.bundle.RoutingPolicyMode, store.ConfigImportRoutingPolicyModePreserve)
	}
	if len(selectionStaged.bundle.RoutingPolicies) != 0 {
		t.Fatalf("selection routing policies = %v, want none in preserve mode", selectionStaged.bundle.RoutingPolicies)
	}
	if selectionStaged.changes.RoutingPolicies != (ChangeCount{}) {
		t.Fatalf("selection routing policy changes = %+v, want zero", selectionStaged.changes.RoutingPolicies)
	}
	if len(selectionStaged.warnings) != 0 {
		t.Fatalf("selection warnings = %v, want none", selectionStaged.warnings)
	}
}

func scopeTestProvider(
	id string,
	name string,
	apiType string,
	baseURL string,
	groupID *string,
) ExportedProvider {
	return ExportedProvider{
		ID:       strings.TrimSpace(id),
		Name:     name,
		APITypes: []ExportedAPIType{{APIType: apiType, BaseURL: baseURL, CredentialSessionID: strings.TrimSpace(id) + "-session"}},
		AuthMode: DefaultAuthMode,
		GroupID:  groupID,
		Weight:   DefaultWeight,
		Enabled:  true,
	}
}

func exportedGroupIDs(groups []ExportedGroup) []string {
	ids := make([]string, 0, len(groups))
	for _, group := range groups {
		ids = append(ids, group.ID)
	}
	return ids
}

func exportedProviderIDs(providers []ExportedProvider) []string {
	ids := make([]string, 0, len(providers))
	for _, provider := range providers {
		ids = append(ids, provider.ID)
	}
	return ids
}
