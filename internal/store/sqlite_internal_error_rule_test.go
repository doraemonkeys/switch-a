package store

import (
	"context"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/errorrule"
	errorrulesqlite "github.com/doraemonkeys/switch-a/internal/errorrule/sqlite"
	"github.com/doraemonkeys/switch-a/internal/model"
)

func configImportTestProvider(id string) model.Provider {
	return model.Provider{
		ID: id, Name: "Provider " + id, AuthMode: "bearer", Enabled: true,
		APITypes: []model.ProviderAPIType{{
			ProviderID: id, APIType: "codex", BaseURL: "https://example.com",
		}},
		CredentialSessions: []credentialsession.RouteSnapshot{{
			RouteTargetID: id, APIType: "codex", Credential: credentialsession.Snapshot{SessionID: id + "-session"},
		}},
	}
}

func configImportTestSession(id string) credentialsession.Session {
	return testStaticCredentialSession(id+"-session", "", "secret")
}

func configImportTestRule(id, providerID string) errorrulesqlite.ImportedRule {
	target, _ := errorrule.NewProviderTarget(errorrule.ProviderID(providerID))
	return errorrulesqlite.ImportedRule{
		ID: errorrule.RuleID(id),
		RuleSpec: errorrule.RuleSpec{
			Name: "Capacity", Enabled: true, Target: target, Keywords: []string{"overloaded"},
			MatchMode: errorrule.MatchAny, Action: errorrule.NewPassthroughAction(),
		},
	}
}

func TestApplyConfigImportAndProviderDeleteShareRuleTransaction(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()
	expected := errorrule.Revision(0)
	bundle := &ConfigImportBundle{
		CredentialSessions: []credentialsession.Session{configImportTestSession("provider-a")},
		Providers:          []model.Provider{configImportTestProvider("provider-a")},
		RoutingPolicyMode:  ConfigImportRoutingPolicyModePreserve,
		RuleImport: errorrulesqlite.ImportRequest{
			Mode:  errorrulesqlite.ImportModeFull,
			Rules: []errorrulesqlite.ImportedRule{configImportTestRule("11111111-1111-4111-8111-111111111111", "provider-a")},
		},
		ExpectedRuleRevision: &expected,
	}
	if err := store.ApplyConfigImport(ctx, bundle); err != nil {
		t.Fatal(err)
	}
	repository := store.InternalErrorRuleRepository()
	if revision, rules := repository.ListRules(); revision != 1 || len(rules) != 1 {
		t.Fatalf("after import revision=%d rules=%#v", revision, rules)
	}

	if err := store.DeleteProvider(ctx, "provider-a"); err != nil {
		t.Fatal(err)
	}
	if revision, rules := repository.ListRules(); revision != 2 || len(rules) != 0 {
		t.Fatalf("after provider delete revision=%d rules=%#v", revision, rules)
	}
	var statsRows int64
	if err := store.db.Table("internal_error_rule_stats").Count(&statsRows).Error; err != nil || statsRows != 0 {
		t.Fatalf("stats rows=%d err=%v", statsRows, err)
	}
}

func TestApplyConfigImportInvalidRuleRollsBackOtherConfig(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()
	if err := store.SetConfig(ctx, "probe-key", "before"); err != nil {
		t.Fatal(err)
	}
	expected := errorrule.Revision(0)
	err := store.ApplyConfigImport(ctx, &ConfigImportBundle{
		CredentialSessions: []credentialsession.Session{configImportTestSession("provider-a")},
		Providers:          []model.Provider{configImportTestProvider("provider-a")},
		RoutingPolicyMode:  ConfigImportRoutingPolicyModePreserve,
		Settings:           map[string]string{"probe-key": "after"},
		RuleImport: errorrulesqlite.ImportRequest{
			Mode:  errorrulesqlite.ImportModeFull,
			Rules: []errorrulesqlite.ImportedRule{configImportTestRule("11111111-1111-4111-8111-111111111111", "missing-provider")},
		},
		ExpectedRuleRevision: &expected,
	})
	if err == nil {
		t.Fatal("invalid rule import error = nil")
	}
	if value, getErr := store.GetConfig(ctx, "probe-key"); getErr != nil || value != "before" {
		t.Fatalf("config after rollback=%q err=%v", value, getErr)
	}
	if _, getErr := store.GetProvider(ctx, "provider-a"); getErr == nil {
		t.Fatal("provider committed despite rule validation failure")
	}
	if revision := store.InternalErrorRuleRepository().CurrentRuleSet().Revision(); revision != 0 {
		t.Fatalf("revision after rollback=%d", revision)
	}
}

func TestApplyConfigImportSettingsOnlyPreservesRuleDomain(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()
	expected := errorrule.Revision(0)
	if err := store.ApplyConfigImport(ctx, &ConfigImportBundle{
		CredentialSessions: []credentialsession.Session{configImportTestSession("provider-a")},
		Providers:          []model.Provider{configImportTestProvider("provider-a")},
		RoutingPolicyMode:  ConfigImportRoutingPolicyModePreserve,
		RuleImport: errorrulesqlite.ImportRequest{
			Mode:  errorrulesqlite.ImportModeFull,
			Rules: []errorrulesqlite.ImportedRule{configImportTestRule("11111111-1111-4111-8111-111111111111", "provider-a")},
		},
		ExpectedRuleRevision: &expected,
	}); err != nil {
		t.Fatal(err)
	}
	repository := store.InternalErrorRuleRepository()
	published := repository.CurrentRuleSet()
	if err := store.ApplyConfigImport(ctx, &ConfigImportBundle{
		RoutingPolicyMode: ConfigImportRoutingPolicyModePreserve,
		Settings:          map[string]string{"probe-key": "settings-only"},
		RuleImport:        errorrulesqlite.ImportRequest{Mode: errorrulesqlite.ImportModePreserve},
	}); err != nil {
		t.Fatal(err)
	}
	if repository.CurrentRuleSet() != published || published.Revision() != 1 {
		t.Fatal("settings-only import changed the rule snapshot")
	}
	if value, err := store.GetConfig(ctx, "probe-key"); err != nil || value != "settings-only" {
		t.Fatalf("settings-only value=%q err=%v", value, err)
	}

	if err := store.ApplyConfigImport(ctx, &ConfigImportBundle{
		RoutingPolicyMode:    ConfigImportRoutingPolicyModePreserve,
		RuleImport:           errorrulesqlite.ImportRequest{Mode: errorrulesqlite.ImportModePreserve},
		ExpectedRuleRevision: &expected,
	}); err == nil {
		t.Fatal("settings-only import accepted a rule precondition")
	}
}
