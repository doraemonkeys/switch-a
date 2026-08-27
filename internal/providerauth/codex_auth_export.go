package providerauth

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
)

const codexAuthModeChatGPT = "chatgpt"

var (
	ErrCodexAuthExportProviderRequired      = errors.New("credential session is required")
	ErrCodexAuthExportRequiresChatGPT       = errors.New("codex auth export requires a chatgpt credential session")
	ErrCodexAuthExportRequiresPaused        = errors.New("codex auth export requires every referencing route target to be paused")
	ErrCodexAuthExportCredentialUnavailable = errors.New("chatgpt credential is unavailable for codex auth export")
)

// CodexAuthTokens is the token block consumed by Codex auth.json.
type CodexAuthTokens struct {
	IDToken      string `json:"id_token"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	AccountID    string `json:"account_id"`
}

// CodexAuthDocument is the portable Codex auth.json representation of one GPT login.
type CodexAuthDocument struct {
	AuthMode     string          `json:"auth_mode"`
	OpenAIAPIKey *string         `json:"OPENAI_API_KEY"`
	Tokens       CodexAuthTokens `json:"tokens"`
	LastRefresh  *time.Time      `json:"last_refresh,omitempty"`
}

// BuildCodexAuthDocument creates a detached credential snapshot without refreshing,
// consuming, unbinding, or otherwise mutating the provider. Requiring a manual pause
// makes the shared refresh-token ownership decision explicit before the secret leaves
// switch-a.
func BuildCodexAuthDocument(snapshot *credentialsession.Snapshot, hasEnabledRoute bool) (CodexAuthDocument, error) {
	if snapshot == nil {
		return CodexAuthDocument{}, ErrCodexAuthExportProviderRequired
	}
	if snapshot.Kind != credentialsession.KindChatGPT {
		return CodexAuthDocument{}, ErrCodexAuthExportRequiresChatGPT
	}
	if hasEnabledRoute {
		return CodexAuthDocument{}, ErrCodexAuthExportRequiresPaused
	}
	if snapshot.AuthState.Status != credentialsession.AuthStatusActive {
		return CodexAuthDocument{}, ErrCodexAuthExportCredentialUnavailable
	}

	credential, err := decodeChatGPTCredentialSession(snapshot)
	if err != nil {
		return CodexAuthDocument{}, fmt.Errorf("%w: decode credential: %w", ErrCodexAuthExportCredentialUnavailable, err)
	}
	if credential == nil || !credential.Ready() {
		return CodexAuthDocument{}, ErrCodexAuthExportCredentialUnavailable
	}

	var lastRefresh *time.Time
	if !credential.LastRefresh.IsZero() {
		value := credential.LastRefresh.UTC()
		lastRefresh = &value
	}

	return CodexAuthDocument{
		AuthMode:     codexAuthModeChatGPT,
		OpenAIAPIKey: nil,
		Tokens: CodexAuthTokens{
			IDToken:      strings.TrimSpace(credential.IDToken),
			AccessToken:  strings.TrimSpace(credential.AccessToken),
			RefreshToken: strings.TrimSpace(credential.RefreshToken),
			AccountID:    strings.TrimSpace(credential.AccountID),
		},
		LastRefresh: lastRefresh,
	}, nil
}
