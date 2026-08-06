package capturebridge

import (
	"strings"

	"github.com/doraemonkeys/switch-a/internal/requestcapture"
)

// CredentialMaterial creates attempt-scoped redaction evidence. The injected
// static API key is the only value switch-a owns and therefore the only value
// Debug Capture is allowed to hide.
func CredentialMaterial(injectedAPIKey string) (
	requestcapture.SensitiveHeaderEvidence,
	requestcapture.CredentialEvidence,
) {
	var sensitiveHeaders requestcapture.SensitiveHeaderEvidence
	var evidence requestcapture.CredentialEvidence
	evidence.Add(strings.TrimSpace(injectedAPIKey))
	sensitiveHeaders.Seal()
	evidence.Seal()
	return sensitiveHeaders, evidence
}
