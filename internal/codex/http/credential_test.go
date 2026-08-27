package codexhttp

import (
	"net/http"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/codex/clientcredential"
)

func TestHTTPHeaderAdapterUsesSharedCredentialGrammar(t *testing.T) {
	headers := make(http.Header)
	headers["authorization"] = []string{"Bearer\t value trailing \t"}
	headers["X-Api-Key"] = []string{"value trailing"}

	result := clientcredential.Extract(map[string][]string(headers))
	defer result.Clear()
	if result.State != clientcredential.StateSingle || string(result.Token) != "value trailing" {
		t.Fatalf("shared extractor result = (%q, %q)", result.State, result.Token)
	}
}

func TestHTTPHeaderAdapterPreservesDuplicateSpellings(t *testing.T) {
	headers := http.Header{
		"Authorization": {"Bearer one"},
		"authorization": {"Bearer one"},
	}
	result := clientcredential.Extract(map[string][]string(headers))
	defer result.Clear()
	if result.State != clientcredential.StateInvalid {
		t.Fatalf("duplicate spelling state = %q", result.State)
	}
}
