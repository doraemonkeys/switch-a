package admin

import (
	"context"
	"testing"

	"switch-a/internal/model"
)

func importedTestProvider(id, name, apiType, baseURL string) ExportedProvider {
	return ExportedProvider{
		ID:             id,
		Name:           name,
		APIKey:         "key-" + id,
		APITypes:       []ExportedAPIType{{APIType: apiType, BaseURL: baseURL}},
		AuthMode:       DefaultAuthMode,
		CredentialType: model.ProviderCredentialTypeAPIKey,
		Enabled:        true,
		Weight:         DefaultWeight,
	}
}

func storedTestProvider(id, name, apiType, baseURL string) *model.Provider {
	return &model.Provider{
		ID:             id,
		Name:           name,
		APIKey:         "key-" + id,
		APITypes:       []model.ProviderAPIType{{ProviderID: id, APIType: apiType, BaseURL: baseURL}},
		AuthMode:       DefaultAuthMode,
		CredentialType: model.ProviderCredentialTypeAPIKey,
		Enabled:        true,
		Weight:         DefaultWeight,
	}
}

func TestCalculateImportChanges_CountsAddsUpdatesAndSkipsInvalidEntries(t *testing.T) {
	h, _, _ := testHandler()

	existingProviders := map[string]*model.Provider{
		"p-existing": storedTestProvider("p-existing", "Existing Provider", "claude", "https://old.example"),
	}
	existingGroups := map[string]*model.Group{
		"g-existing": {
			ID:       "g-existing",
			Name:     "Existing Group",
			Strategy: "priority",
			Weight:   DefaultWeight,
			Enabled:  true,
		},
	}
	existingSettings := map[string]string{
		configStickyModeKey: "off",
	}

	req := &ImportConfigRequest{
		ImportScope: fullConfigImportScope(),
		Providers: []ExportedProvider{
			importedTestProvider("p-existing", "Updated Provider", "claude", "https://new.example"),
			importedTestProvider("p-new", "New Provider", "codex", "https://codex.example"),
			{
				Name:     "Missing ID",
				APIKey:   "ignored",
				APITypes: []ExportedAPIType{{APIType: "claude", BaseURL: "https://skip.example"}},
				Enabled:  true,
			},
		},
		Groups: []ExportedGroup{
			{
				ID:       "g-existing",
				Name:     "Updated Group",
				Strategy: "priority",
				Weight:   DefaultWeight,
				Enabled:  true,
			},
			{
				ID:       "g-new",
				Name:     "New Group",
				Strategy: "weight",
				Weight:   2,
				Enabled:  true,
			},
			{
				Name:     "Missing ID",
				Strategy: "priority",
				Enabled:  true,
			},
		},
		Settings: map[string]string{
			configStickyModeKey:        "api_type",
			configGlobalMaxAttemptsKey: "5",
			"ignored_key":              "ignored",
		},
	}

	changes := h.calculateImportChanges(req, existingProviders, existingGroups, existingSettings)

	if changes.Providers.Add != 1 || changes.Providers.Update != 1 {
		t.Fatalf("provider changes = %+v, want add=1 update=1", changes.Providers)
	}
	if changes.Groups.Add != 1 || changes.Groups.Update != 1 {
		t.Fatalf("group changes = %+v, want add=1 update=1", changes.Groups)
	}
	if changes.Settings.Add != 1 || changes.Settings.Update != 1 {
		t.Fatalf("settings changes = %+v, want add=1 update=1", changes.Settings)
	}
}

func TestApplyImportChanges_TracksAddsUpdatesAndMutatesStore(t *testing.T) {
	h, st, _ := testHandler()
	ctx := context.Background()

	existingProviders := map[string]*model.Provider{
		"p-existing": storedTestProvider("p-existing", "Existing Provider", "claude", "https://old.example"),
	}
	existingGroups := map[string]*model.Group{
		"g-existing": {
			ID:       "g-existing",
			Name:     "Existing Group",
			Strategy: "priority",
			Weight:   DefaultWeight,
			Enabled:  true,
		},
	}
	existingSettings := map[string]string{
		configStickyModeKey: "off",
	}

	st.providers["p-existing"] = existingProviders["p-existing"]
	st.groups["g-existing"] = existingGroups["g-existing"]
	st.config[configStickyModeKey] = "off"

	req := &ImportConfigRequest{
		ImportScope: fullConfigImportScope(),
		Providers: []ExportedProvider{
			importedTestProvider("p-existing", "Updated Provider", "claude", "https://new.example"),
			importedTestProvider("p-new", "New Provider", "codex", "https://codex.example"),
		},
		Groups: []ExportedGroup{
			{
				ID:       "g-existing",
				Name:     "Updated Group",
				Strategy: "priority",
				Weight:   DefaultWeight,
				Enabled:  true,
			},
			{
				ID:       "g-new",
				Name:     "New Group",
				Strategy: "weight",
				Weight:   2,
				Enabled:  true,
			},
		},
		Settings: map[string]string{
			configStickyModeKey:        "api_type",
			configGlobalMaxAttemptsKey: "5",
		},
	}

	applied, err := h.applyImportChanges(ctx, req, existingProviders, existingGroups, existingSettings)
	if err != nil {
		t.Fatalf("applyImportChanges() error = %v", err)
	}

	if applied.Providers.Added != 1 || applied.Providers.Updated != 1 {
		t.Fatalf("provider applied = %+v, want added=1 updated=1", applied.Providers)
	}
	if applied.Groups.Added != 1 || applied.Groups.Updated != 1 {
		t.Fatalf("group applied = %+v, want added=1 updated=1", applied.Groups)
	}
	if applied.Settings.Added != 1 || applied.Settings.Updated != 1 {
		t.Fatalf("settings applied = %+v, want added=1 updated=1", applied.Settings)
	}

	if got := st.providers["p-existing"].Name; got != "Updated Provider" {
		t.Fatalf("updated provider name = %q, want Updated Provider", got)
	}
	if _, ok := st.providers["p-new"]; !ok {
		t.Fatal("new provider was not created")
	}
	if got := st.groups["g-existing"].Name; got != "Updated Group" {
		t.Fatalf("updated group name = %q, want Updated Group", got)
	}
	if _, ok := st.groups["g-new"]; !ok {
		t.Fatal("new group was not created")
	}
	if got := st.config[configStickyModeKey]; got != "api_type" {
		t.Fatalf("sticky_mode = %q, want api_type", got)
	}
	if got := st.config[configGlobalMaxAttemptsKey]; got != "5" {
		t.Fatalf("global_max_attempts = %q, want 5", got)
	}
}
