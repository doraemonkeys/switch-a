package clientdisguiseapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/clientdisguise"
	"github.com/doraemonkeys/switch-a/internal/codex/clientdisguise/officialversion"
	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/model"
)

func TestMaterializedCredentialCanSaveProfileBeforeFirstRequest(t *testing.T) {
	handler, persistence, clients := realAdministration(t)
	ctx := context.Background()
	session := &credentialsession.Session{
		ID: "login", Name: "Temporary GPT", Kind: credentialsession.KindAPIKey,
		SecretData: "test-key", SubjectKind: credentialsession.SubjectPending,
	}
	provider := &model.Provider{
		ID: "provider", Name: "Temporary GPT", Vendor: "openai",
		APITypes: []model.ProviderAPIType{{APIType: "codex", BaseURL: "https://example.com"}},
		CredentialSessions: []credentialsession.RouteSnapshot{{
			APIType: "codex", Credential: credentialsession.Snapshot{SessionID: session.ID},
		}},
	}
	if err := persistence.CreateProviderWithCredentialSessions(ctx, provider, []*credentialsession.Session{session}); err != nil {
		t.Fatal(err)
	}
	client, err := clients.Resolve(ctx, []byte("reference-key"))
	if err != nil {
		t.Fatal(err)
	}
	repo := persistence.ClientDisguiseRepository()
	const referenceID = "reference"
	if err := repo.SaveReference(ctx, clientdisguise.ReferenceSource{ID: referenceID, Name: "Desktop", ClientIdentityID: client.ID}); err != nil {
		t.Fatal(err)
	}
	profile := clientdisguise.BuiltinProfiles()[0]
	learned, err := repo.LearnSample(ctx, clientdisguise.Sample{
		ID: "sample", SourceID: referenceID, CapturedAt: time.Now().UTC(),
		Tuple: profile.Tuple, ClientVersion: "0.153.4", Features: clientdisguise.Features{ClientVersion: "0.153.4"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, revisionID := range []string{profile.ID, learned.Revision.ID} {
		for _, versionSource := range []string{"", officialversion.Source} {
			t.Run(fmt.Sprintf("%s/%s", revisionID, versionSource), func(t *testing.T) {
				binding := clientdisguise.ProfileBinding{
					RevisionID: revisionID, Mode: clientdisguise.ModeAuto,
					VersionSource: versionSource, ReferenceSourceID: referenceID,
				}
				body, err := json.Marshal(binding)
				if err != nil {
					t.Fatal(err)
				}
				response := invoke(handler.SaveBinding, string(body))
				if response.Code != http.StatusOK {
					t.Fatalf("save profile before any upstream request: %d %s", response.Code, response.Body.String())
				}
				var saved clientdisguise.ProfileBinding
				if err := json.Unmarshal(response.Body.Bytes(), &saved); err != nil {
					t.Fatal(err)
				}
				if saved.VersionSource != versionSource || saved.Tuple != profile.Tuple || saved.ReferenceSourceID != referenceID {
					t.Fatalf("saved binding = %+v", saved)
				}
				overview, err := handler.overview(ctx)
				if err != nil || len(overview.Logins) != 1 || overview.Logins[0].Binding == nil || overview.Logins[0].Binding.VersionSource != versionSource {
					t.Fatalf("saved selection did not survive reload: %+v, %v", overview.Logins, err)
				}
			})
		}
	}
}
