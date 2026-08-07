package capturebridge

import (
	"strings"

	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/requestcapture"
)

// CredentialMaterial creates attempt-scoped redaction evidence. The injected
// provider credential is the only secret switch-a owns on the upstream wire,
// so unrelated user and provider diagnostics remain transparent.
func CredentialMaterial(injectedCredential string) (
	requestcapture.SensitiveHeaderEvidence,
	requestcapture.CredentialEvidence,
) {
	var sensitiveHeaders requestcapture.SensitiveHeaderEvidence
	var evidence requestcapture.CredentialEvidence
	evidence.Add(strings.TrimSpace(injectedCredential))
	sensitiveHeaders.Seal()
	evidence.Seal()
	return sensitiveHeaders, evidence
}

// InjectedCredentialValue resolves the exact secret switch-a places on the
// upstream request. Capture policy follows credential ownership, not header or
// token shape: static providers contribute their API key, while login-backed
// providers contribute only the OAuth access token used for bearer auth.
func InjectedCredentialValue(provider *model.Provider, apiType string) string {
	if provider == nil {
		return ""
	}
	switch model.NormalizeProviderCredentialType(provider.CredentialType) {
	case model.ProviderCredentialTypeAPIKey:
		return strings.TrimSpace(provider.APIKeyForAPIType(apiType))
	case model.ProviderCredentialTypeChatGPT:
		if provider.Credential == nil {
			return ""
		}
		secret, err := model.DecodeChatGPTProviderSecret(provider.Credential.SecretData)
		if err != nil || secret == nil {
			return ""
		}
		return strings.TrimSpace(secret.AccessToken)
	default:
		return ""
	}
}
