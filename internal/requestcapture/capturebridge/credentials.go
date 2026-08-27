package capturebridge

import (
	"net/http"
	"strings"

	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
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

// InjectedCredentialFromSnapshot resolves the exact secret present on an
// already-sanitized attempt. ChatGPT refresh can change the access token without
// mutating the immutable candidate snapshot, so its value must come from the
// final applied headers rather than from persisted selection evidence.
func InjectedCredentialFromSnapshot(snapshot credentialsession.Snapshot, appliedHeaders http.Header) string {
	switch snapshot.Kind {
	case credentialsession.KindAPIKey:
		return strings.TrimSpace(snapshot.SecretData)
	case credentialsession.KindChatGPT:
		scheme, token, ok := strings.Cut(strings.TrimSpace(appliedHeaders.Get("Authorization")), " ")
		if !ok || !strings.EqualFold(scheme, "Bearer") {
			return ""
		}
		return strings.TrimSpace(token)
	default:
		return ""
	}
}
