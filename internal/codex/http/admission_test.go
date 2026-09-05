package codexhttp

import (
	"net/http/httptest"
	"testing"
)

func TestContractSpecificAdmissionDependencies(t *testing.T) {
	var runtime *Runtime
	for _, test := range []struct {
		api, path  string
		body, want bool
	}{
		{"codex", "/codex/responses", true, true},
		{"codex", "/codex/v1/responses/compact", true, true},
		{"codex", "/codex/alpha/search", true, false},
		{"codex", "/codex/models", true, false},
		{"codex", "/codex/responses", false, false},
		{"claude", "/v1/messages", true, false},
	} {
		t.Run(test.path+test.api, func(t *testing.T) {
			if got := runtime.RequiresClientEvidence(test.api, test.body, test.path); got != test.want {
				t.Fatalf("needs evidence=%v", got)
			}
		})
	}
}

func TestJSONRequestContractsPreserveExplicitOpaqueMedia(t *testing.T) {
	for _, test := range []struct {
		path, media string
		want        bool
	}{
		{"/codex/alpha/search", "", true},
		{"/codex/responses", "", true},
		{"/codex/v1/responses/compact", "", true},
		{"/codex/models", "", false},
		{"/codex/extension", "application/json; charset=utf-8", true},
		{"/codex/extension", "application/custom+json", true},
		{"/codex/alpha/search", "application/octet-stream", false},
		{"/codex/alpha/search", "invalid;broken", false},
	} {
		t.Run(test.path+test.media, func(t *testing.T) {
			request := httptest.NewRequest("POST", test.path, nil)
			request.Header.Set("Content-Type", test.media)
			if got := RequestUsesJSON(request); got != test.want {
				t.Fatalf("JSON conversion=%v", got)
			}
		})
	}
}
