package clientaccess

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/doraemonkeys/switch-a/internal/apicontract"
	"github.com/doraemonkeys/switch-a/internal/codex/clientcredential"
)

const (
	ReasonPermissive    = "permissive"
	ReasonMatched       = "matched"
	ReasonUnlisted      = "unlisted"
	CredentialUnchecked = "unchecked"
	headerGoogleAPIKey  = "X-Goog-Api-Key"
	queryGoogleAPIKey   = "key"
)

type Decision struct {
	Allowed         bool
	Mode            Mode
	CredentialState string
	Reason          string
	KeyID           string
}

func (s *Service) Admit(ctx context.Context, r *http.Request, apiType string) (Decision, error) {
	snapshot, err := s.Snapshot(ctx)
	if err != nil {
		return Decision{}, err
	}
	decision := Decision{Mode: snapshot.Mode}
	// Permissive mode preserves the existing gateway contract even for
	// credentials that are absent, unusual, or irrelevant to client identity.
	if snapshot.Mode == ModePermissive {
		decision.Allowed = true
		decision.CredentialState = CredentialUnchecked
		decision.Reason = ReasonPermissive
		return decision, nil
	}
	credential := extractCredential(r, apiType)
	defer credential.Clear()
	decision.CredentialState = string(credential.State)
	if credential.State != clientcredential.StateSingle {
		decision.Reason = string(credential.State)
		return decision, nil
	}
	for _, key := range snapshot.Keys {
		if subtle.ConstantTimeCompare(credential.Token, []byte(key.Key)) == 1 {
			decision.Allowed = true
			decision.KeyID = key.ID
			decision.Reason = ReasonMatched
			return decision, nil
		}
	}
	decision.Reason = ReasonUnlisted
	return decision, nil
}

func extractCredential(r *http.Request, apiType string) clientcredential.Result {
	if r == nil {
		return clientcredential.Result{State: clientcredential.StateAbsent}
	}
	result := clientcredential.Extract(r.Header)
	if apiType != string(apicontract.APITypeGemini) {
		return result
	}
	// Google-native locations belong to Gemini contracts. Reading them here
	// must neither widen Codex identity semantics nor rewrite forwarding input.
	locations := [][]string{matchingHeaderValues(r.Header, headerGoogleAPIKey)}
	if r.URL != nil {
		locations = append(locations, r.URL.Query()[queryGoogleAPIKey])
	}
	for _, values := range locations {
		if len(values) == 0 {
			continue
		}
		if len(values) != 1 || validateKey(values[0]) != nil {
			result.Clear()
			return clientcredential.Result{State: clientcredential.StateInvalid}
		}
		if result.State == clientcredential.StateInvalid || result.State == clientcredential.StateAmbiguous {
			return result
		}
		if result.State == clientcredential.StateAbsent {
			result = clientcredential.Result{State: clientcredential.StateSingle, Token: []byte(values[0])}
			continue
		}
		if subtle.ConstantTimeCompare(result.Token, []byte(values[0])) != 1 {
			result.Clear()
			return clientcredential.Result{State: clientcredential.StateAmbiguous}
		}
	}
	return result
}

func matchingHeaderValues(headers http.Header, name string) []string {
	var values []string
	for key, candidates := range headers {
		if strings.EqualFold(key, name) {
			values = append(values, candidates...)
		}
	}
	return values
}
