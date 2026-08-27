package proxy

import (
	"crypto/sha256"
	"encoding/json"
	"net/url"

	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/codex/identity"
	"github.com/doraemonkeys/switch-a/internal/model"
)

type testAuthCandidate = codexidentity.CandidateSnapshot
type testAppliedIdentity = codexidentity.AppliedIdentity
type testCredentialSnapshot = credentialsession.Snapshot
type testUpstreamURL = url.URL

type testProviderValue interface {
	model.Provider | *model.Provider
}

func withTestStaticCredential[T testProviderValue](provider T, apiType, secret string) T {
	return withTestCredential(provider, apiType, secret, credentialsession.KindAPIKey)
}

func withTestChatGPTCredential[T testProviderValue](provider T, apiType, secret string) T {
	return withTestCredential(provider, apiType, secret, credentialsession.KindChatGPT)
}

func withTestCredential[T testProviderValue](provider T, apiType, secret string, kind credentialsession.Kind) T {
	var target *model.Provider
	switch value := any(provider).(type) {
	case model.Provider:
		target = &value
	case *model.Provider:
		target = value
	}
	apiTypes := testCredentialAPITypes(*target, apiType)
	target.CredentialSessions = make([]credentialsession.RouteSnapshot, 0, len(apiTypes))
	for _, candidateAPIType := range apiTypes {
		subject, authState := testCredentialIdentity(secret, candidateAPIType, kind)
		target.CredentialSessions = append(target.CredentialSessions, credentialsession.RouteSnapshot{
			RouteTargetID: target.ID,
			APIType:       candidateAPIType,
			Credential: credentialsession.Snapshot{
				SessionID:  target.ID + "-" + candidateAPIType,
				Vendor:     "proxy-test",
				Kind:       kind,
				SecretData: secret,
				Version:    1,
				Subject:    subject,
				AuthState:  authState,
			},
		})
	}
	if value, ok := any(provider).(model.Provider); ok {
		value = *target
		return any(value).(T)
	}
	return provider
}

func testCredentialIdentity(secret, apiType string, kind credentialsession.Kind) (credentialsession.Subject, credentialsession.AuthState) {
	state := credentialsession.AuthState{Status: credentialsession.AuthStatusActive}
	if kind == credentialsession.KindChatGPT {
		var identity model.ChatGPTProviderCredential
		if err := json.Unmarshal([]byte(secret), &identity); err != nil {
			panic(err)
		}
		subject, err := credentialsession.AccountSubject(identity.AccountID)
		if err != nil {
			panic(err)
		}
		state.AccountID = identity.AccountID
		state.Email = identity.Email
		state.PlanType = identity.PlanType
		if !identity.ExpiresAt.IsZero() {
			expiresAt := identity.ExpiresAt
			state.ExpiresAt = &expiresAt
		}
		if !identity.LastRefresh.IsZero() {
			lastRefresh := identity.LastRefresh
			state.LastRefreshAt = &lastRefresh
		}
		return subject, state
	}
	digest := sha256.Sum256([]byte("proxy-test:" + secret + ":" + apiType))
	subject, err := credentialsession.KeyedDigestSubject("proxy-test-v1", digest[:])
	if err != nil {
		panic(err)
	}
	return subject, state
}

func testCredentialAPITypes(provider model.Provider, explicit string) []string {
	if explicit != "" {
		return []string{explicit}
	}
	seen := make(map[string]struct{})
	var result []string
	for _, api := range provider.APITypes {
		if api.APIType == "" {
			continue
		}
		if _, exists := seen[api.APIType]; exists {
			continue
		}
		seen[api.APIType] = struct{}{}
		result = append(result, api.APIType)
	}
	if len(result) > 0 {
		return result
	}
	return []string{APITypeClaude, APITypeDeepSeekClaude, APITypeCodex, APITypeGemini, APITypeGrok, APITypeDeepSeekOpenAI}
}
