package admin

import (
	"context"
	"fmt"
	"strings"

	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/model"
)

const providerCredentialMaterializationOperation = "provider_credential_materialization"

type NewProviderCredentialSessionInput struct {
	ID         string                 `json:"id"`
	Name       string                 `json:"name"`
	Kind       credentialsession.Kind `json:"kind"`
	SecretData string                 `json:"secret_data"`
}

type providerCredentialSessionWriter interface {
	CreateProviderWithCredentialSessions(context.Context, *model.Provider, []*credentialsession.Session) error
	UpdateProviderWithCredentialSessions(context.Context, *model.Provider, []*credentialsession.Session) error
}

func validateNewProviderCredentialSessions(apiTypes []APITypeInput, inputs []NewProviderCredentialSessionInput) string {
	if len(inputs) == 0 {
		return ""
	}
	referenced := make(map[string]struct{}, len(apiTypes))
	for _, apiType := range apiTypes {
		referenced[apiType.CredentialSessionID] = struct{}{}
	}
	seen := make(map[string]struct{}, len(inputs))
	for _, input := range inputs {
		id := strings.TrimSpace(input.ID)
		name := strings.TrimSpace(input.Name)
		if id == "" {
			return "new credential session id is required"
		}
		if _, duplicate := seen[id]; duplicate {
			return "duplicate new credential session: " + id
		}
		seen[id] = struct{}{}
		if _, used := referenced[id]; !used {
			return "new credential session is not referenced by an api_type: " + id
		}
		if input.Kind != credentialsession.KindAPIKey {
			return "provider writes can only materialize api_key credential sessions"
		}
		if name == "" || len([]rune(name)) > credentialsession.MaxNameLength {
			return fmt.Sprintf("credential session name is required and must not exceed %d characters", credentialsession.MaxNameLength)
		}
		if strings.TrimSpace(input.SecretData) == "" {
			return "secret_data is required for new credential session: " + id
		}
	}
	return ""
}

func buildNewProviderCredentialSessions(inputs []NewProviderCredentialSessionInput) ([]*credentialsession.Session, error) {
	sessions := make([]*credentialsession.Session, 0, len(inputs))
	for _, input := range inputs {
		session := &credentialsession.Session{
			ID:         strings.TrimSpace(input.ID),
			Name:       strings.TrimSpace(input.Name),
			Kind:       input.Kind,
			SecretData: input.SecretData,
			Version:    1,
			AuthState: credentialsession.AuthState{
				Status: credentialsession.AuthStatusActive,
			},
		}
		if err := session.SetSubject(credentialsession.PendingSubject()); err != nil {
			return nil, err
		}
		if err := session.Validate(); err != nil {
			return nil, err
		}
		sessions = append(sessions, session)
	}
	return sessions, nil
}

func providerCredentialSessionIDs(sessions []*credentialsession.Session) []string {
	ids := make([]string, 0, len(sessions))
	for _, session := range sessions {
		ids = append(ids, session.ID)
	}
	return ids
}
