package clientaccess

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"

	"github.com/doraemonkeys/switch-a/internal/codex/clientcredential"
)

const (
	keyPreviewPrefixRunes = 6
	keyPreviewSuffixRunes = 4
	keyPreviewHiddenRunes = 4
)

// UsageIdentity follows the credential value rather than registry membership or
// Codex client identity, both of which can change while a key stays the same.
type UsageIdentity struct {
	Fingerprint string
	MaskedKey   string
}

func IdentifyKey(value []byte) UsageIdentity {
	digest := sha256.Sum256(value)
	runes := []rune(string(value))
	masked := "••••"
	if len(runes) >= keyPreviewPrefixRunes+keyPreviewSuffixRunes+keyPreviewHiddenRunes {
		masked = string(runes[:keyPreviewPrefixRunes]) + "…" + string(runes[len(runes)-keyPreviewSuffixRunes:])
	}
	return UsageIdentity{Fingerprint: hex.EncodeToString(digest[:]), MaskedKey: masked}
}

// Observation is independent of admission: permissive access still needs exact
// per-key accounting, while ambiguous credentials must never pick an owner.
func ObserveUsageIdentity(r *http.Request, apiType string) UsageIdentity {
	credential := extractCredential(r, apiType)
	defer credential.Clear()
	if credential.State != clientcredential.StateSingle {
		return UsageIdentity{}
	}
	return IdentifyKey(credential.Token)
}
