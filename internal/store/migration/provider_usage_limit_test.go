package migration

import (
	"testing"

	"github.com/doraemonkeys/switch-a/internal/model"
)

func TestMigrateProviderUsageLimitPolicyStorage_BackfillsDerivedDefaults(t *testing.T) {
	t.Parallel()

	db := setupMigrationTestDB(t)
	if err := db.Exec(`CREATE TABLE providers (
		id TEXT PRIMARY KEY,
		credential_type TEXT NOT NULL,
		usage_limit_policy TEXT NOT NULL
	)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO providers (id, credential_type, usage_limit_policy) VALUES
		('relay-default', 'api_key', 'switch_provider'),
		('gpt-default', 'chatgpt', 'suspend'),
		('gpt-explicit', 'chatgpt', 'switch_provider')`).Error; err != nil {
		t.Fatal(err)
	}

	if err := MigrateProviderUsageLimitPolicyStorage(db); err != nil {
		t.Fatalf("MigrateProviderUsageLimitPolicyStorage: %v", err)
	}

	for _, tc := range []struct{ id, want string }{
		{id: "relay-default", want: ""},
		{id: "gpt-default", want: ""},
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
