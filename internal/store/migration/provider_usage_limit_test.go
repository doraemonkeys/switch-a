package migration

import (
	"testing"

	"github.com/doraemonkeys/switch-a/internal/model"
)

func TestMigrateProviderUsageLimitPolicyStorage_MaterializesLegacyChatGPTDefault(t *testing.T) {
	t.Parallel()

	db := setupMigrationTestDB(t)
	if err := db.Exec(`CREATE TABLE providers (
		id TEXT PRIMARY KEY,
		credential_type TEXT NOT NULL,
		usage_limit_policy TEXT
	)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO providers (id, credential_type, usage_limit_policy) VALUES
		('relay-default', 'api_key', ''),
		('relay-explicit', 'api_key', 'suspend'),
		('gpt-default', 'chatgpt', ''),
		('gpt-null-default', 'chatgpt', NULL),
		('gpt-whitespace-default', ' chatgpt ', ' '),
		('gpt-explicit-suspend', 'chatgpt', 'suspend'),
		('gpt-explicit', 'chatgpt', 'switch_provider')`).Error; err != nil {
		t.Fatal(err)
	}

	for attempt := 1; attempt <= 2; attempt++ {
		if err := MigrateProviderUsageLimitPolicyStorage(db); err != nil {
			t.Fatalf("MigrateProviderUsageLimitPolicyStorage attempt %d: %v", attempt, err)
		}
	}

	for _, tc := range []struct{ id, want string }{
		{id: "relay-default", want: ""},
		{id: "relay-explicit", want: string(model.ProviderUsageLimitPolicySuspend)},
		{id: "gpt-default", want: string(model.ProviderUsageLimitPolicySuspend)},
		{id: "gpt-null-default", want: string(model.ProviderUsageLimitPolicySuspend)},
		{id: "gpt-whitespace-default", want: string(model.ProviderUsageLimitPolicySuspend)},
		{id: "gpt-explicit-suspend", want: string(model.ProviderUsageLimitPolicySuspend)},
		{id: "gpt-explicit", want: string(model.ProviderUsageLimitPolicySwitchProvider)},
	} {
		var got string
		if err := db.Raw(`SELECT usage_limit_policy FROM providers WHERE id = ?`, tc.id).Scan(&got).Error; err != nil {
			t.Fatalf("read %s: %v", tc.id, err)
		}
		if got != tc.want {
			t.Fatalf("%s usage_limit_policy = %q, want %q", tc.id, got, tc.want)
		}
	}
}

func TestMigrateProviderUsageLimitPolicyStorage_NewSchemaIsNoOp(t *testing.T) {
	t.Parallel()

	db := setupMigrationTestDB(t)
	if err := db.Exec(`CREATE TABLE providers (
		id TEXT PRIMARY KEY,
		usage_limit_policy TEXT NOT NULL
	)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO providers (id, usage_limit_policy) VALUES ('route-target', '')`).Error; err != nil {
		t.Fatal(err)
	}

	if err := MigrateProviderUsageLimitPolicyStorage(db); err != nil {
		t.Fatalf("MigrateProviderUsageLimitPolicyStorage: %v", err)
	}

	var got string
	if err := db.Raw(`SELECT usage_limit_policy FROM providers WHERE id = 'route-target'`).Scan(&got).Error; err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Fatalf("usage_limit_policy = %q, want unchanged empty route-target default", got)
	}
}
