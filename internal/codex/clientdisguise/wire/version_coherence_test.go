package wire

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	disguise "github.com/doraemonkeys/switch-a/internal/codex/clientdisguise"
)

func TestUserAgentAndVersionAreAppliedTogether(t *testing.T) {
	const originalUA = "codex-tui/0.149.0 (Linux 6.8; x86_64) terminal"
	const newUA = "codex-tui/0.150.0-alpha.8 (Linux 6.8; x86_64) terminal"
	var builtin disguise.ProfileRevision
	for _, profile := range disguise.BuiltinProfiles() {
		if profile.Tuple == (disguise.Tuple{ClientType: "tui", Platform: "linux", Arch: "amd64"}) {
			builtin = profile
		}
	}
	for _, test := range []struct {
		name        string
		profile     disguise.ProfileRevision
		wantUA      string
		wantVersion string
	}{
		{"incomplete builtin", builtin, originalUA, "0.149.0"},
		{"incomplete header overrides", disguise.ProfileRevision{ClientVersion: "0.150.0-alpha.8", Features: disguise.Features{Headers: map[string]string{"Version": "0.150.0-alpha.8", "X-Client-Version": "0.150.0-alpha.8", "User-Agent": ""}}}, originalUA, "0.149.0"},
		{"complete reference", disguise.ProfileRevision{ClientVersion: "0.150.0-alpha.8", Features: disguise.Features{UserAgent: newUA}}, newUA, "0.150.0-alpha.8"},
		{"stored mixed profile", disguise.ProfileRevision{ClientVersion: "0.150.0-alpha.8", Features: disguise.Features{UserAgent: originalUA, Headers: map[string]string{"Version": "0.150.0-alpha.8"}}}, originalUA, "0.149.0"},
		{"UA supplied as header", disguise.ProfileRevision{ClientVersion: "0.150.0-alpha.8", Features: disguise.Features{Headers: map[string]string{"user-agent": newUA}}}, newUA, "0.150.0-alpha.8"},
		{"typed UA wins over header alias", disguise.ProfileRevision{ClientVersion: "0.150.0-alpha.8", Features: disguise.Features{UserAgent: newUA, Headers: map[string]string{"user-agent": originalUA, "Version": "0.149.0"}}}, newUA, "0.150.0-alpha.8"},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := NewSession(disguise.TargetSnapshot{Policy: disguise.Policy{Enabled: true}, Profile: test.profile}, "version-coherence")
			original := http.Header{"User-Agent": {originalUA}, "Version": {"0.149.0"}, "X-Client-Version": {"0.149.0"}, "X-Codex-Client-Version": {"0.149.0"}, "Accept-Encoding": {"gzip"}}
			derived, err := s.Headers(context.Background(), original)
			if err != nil {
				t.Fatal(err)
			}
			if derived.Get("User-Agent") != test.wantUA {
				t.Fatal("wrong UA", derived.Get("User-Agent"))
			}
			for _, name := range []string{"Version", "X-Client-Version", "X-Codex-Client-Version"} {
				if derived.Get(name) != test.wantVersion {
					t.Fatalf("%s = %s, want %s", name, derived.Get(name), test.wantVersion)
				}
			}
			if original.Get("Version") != "0.149.0" || derived.Get("Accept-Encoding") != "gzip" {
				t.Fatal("unrelated behavior changed")
			}
			payload, _ := json.Marshal(map[string]any{"client_metadata": map[string]string{"user_agent": originalUA, "client_version": "0.149.0", "version": "0.149.0"}, "input": []any{map[string]string{"user_agent": originalUA, "client_version": "business-value"}}})
			for _, websocket := range []bool{false, true} {
				input := payload
				transform := s.RequestJSON
				if websocket {
					input = append([]byte(`{"type":"response.create",`), payload[1:]...)
					transform = s.ClientFrame
				}
				output, err := transform(context.Background(), input)
				if err != nil {
					t.Fatal(err)
				}
				var got struct {
					Metadata map[string]string   `json:"client_metadata"`
					Input    []map[string]string `json:"input"`
				}
				if err := json.Unmarshal(output, &got); err != nil {
					t.Fatal(err)
				}
				if got.Metadata["user_agent"] != test.wantUA || got.Metadata["client_version"] != test.wantVersion || got.Metadata["version"] != test.wantVersion {
					t.Fatal("body fingerprint disagrees with headers", got.Metadata)
				}
				if got.Input[0]["user_agent"] != originalUA || got.Input[0]["client_version"] != "business-value" {
					t.Fatal("conversation content rewritten")
				}
			}
		})
	}
}

func TestIncompleteProfileDoesNotInjectAVersionHeader(t *testing.T) {
	s := NewSession(disguise.TargetSnapshot{Policy: disguise.Policy{Enabled: true}, Profile: disguise.ProfileRevision{ClientVersion: "2.0.0", Features: disguise.Features{Headers: map[string]string{"Version": "2.0.0"}}}}, "partial")
	got, err := s.Headers(context.Background(), http.Header{"User-Agent": {"codex-tui/1.0.0"}})
	if err != nil || got.Get("Version") != "" || got.Get("User-Agent") != "codex-tui/1.0.0" {
		t.Fatal("incomplete profile invented a version", got, err)
	}
}
