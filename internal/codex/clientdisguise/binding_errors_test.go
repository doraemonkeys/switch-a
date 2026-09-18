package clientdisguise

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestBindingMissingRecordIdentifiesRelationship(t *testing.T) {
	repo := testRepository(t)
	ctx := context.Background()
	if _, err := repo.SyncLoginAccount(ctx, "login", account("account")); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		binding ProfileBinding
		message string
	}{
		{
			name:    "login",
			binding: ProfileBinding{CredentialSessionID: "missing-login", Mode: ModePinned, RevisionID: BuiltinProfiles()[0].ID},
			message: `login identity for credential session "missing-login"`,
		},
		{
			name:    "revision",
			binding: ProfileBinding{CredentialSessionID: "login", Mode: ModePinned, RevisionID: "missing-revision"},
			message: `profile revision "missing-revision"`,
		},
		{
			name:    "reference",
			binding: ProfileBinding{CredentialSessionID: "login", Mode: ModeAuto, RevisionID: BuiltinProfiles()[0].ID, ReferenceSourceID: "missing-reference"},
			message: `reference source "missing-reference"`,
		},
		{
			name:    "transport",
			binding: ProfileBinding{CredentialSessionID: "login", Mode: ModePinned, RevisionID: BuiltinProfiles()[0].ID, TransportSampleID: "missing-transport"},
			message: `transport sample "missing-transport"`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := repo.SetBinding(ctx, tc.binding)
			if !errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("missing relationship diagnostic = %v, want %s", err, tc.message)
			}
		})
	}
}
