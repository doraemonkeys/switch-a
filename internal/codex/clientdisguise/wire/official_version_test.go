package wire

import (
	"context"
	"net/http"
	"strings"
	"testing"

	disguise "github.com/doraemonkeys/switch-a/internal/codex/clientdisguise"
	"github.com/doraemonkeys/switch-a/internal/codex/clientdisguise/officialversion"
)

func TestOfficialVersionConsistentAcrossRequestCarriers(t *testing.T) {
	ctx := context.Background()
	for _, sampleUA := range []string{"", "codex-tui/0.149.0 (Linux 6.8; x86_64) xterm (codex-tui; 0.149.0)"} {
		t.Run(sampleUA, func(t *testing.T) {
			target := disguise.TargetSnapshot{
				Policy:          disguise.Policy{Enabled: true},
				Profile:         disguise.ProfileRevision{ClientVersion: "0.149.0", Features: disguise.Features{UserAgent: sampleUA, Headers: map[string]string{"Version": "0.149.0"}}},
				OfficialVersion: officialversion.Release{Version: "0.151.0"},
			}
			s := NewSession(target, "release-test")
			incomingUA := "codex-tui/0.148.0 (Linux 6.9; arm64) screen (codex-tui; 0.148.0)"
			selectedUA := sampleUA
			if selectedUA == "" {
				selectedUA = incomingUA
			}
			wantUA := disguise.WithUserAgentVersion(selectedUA, "0.151.0")
			metadata := `{"client_version":"0.148.0","user_agent":"` + incomingUA + `","thread_id":"thread","session_id":"session","os_version":"6.9","desktop_build":"26.8"}`
			headers := http.Header{"User-Agent": {incomingUA}, "Version": {"0.148.0"}, "X-Codex-Turn-Metadata": {metadata}, "Thread-Id": {"thread"}, "Accept-Encoding": {"br"}}
			got, err := s.Headers(ctx, headers)
			if err != nil || got.Get("User-Agent") != wantUA || got.Get("Version") != "0.151.0" || got.Get("Thread-Id") != "thread" || got.Get("Accept-Encoding") != "br" {
				t.Fatal(got, err)
			}
			if !strings.Contains(got.Get("X-Codex-Turn-Metadata"), wantUA) {
				t.Fatal(got)
			}
			body := []byte(`{"type":"response.create","client_metadata":` + metadata + `,"prompt_cache_key":"review:thread","input":[{"text":"0.148.0"}]}`)
			for _, transform := range []func(context.Context, []byte) ([]byte, error){s.RequestJSON, s.ClientFrame} {
				got, err := transform(ctx, body)
				if err != nil || !strings.Contains(string(got), `"client_version":"0.151.0"`) || !strings.Contains(string(got), wantUA) || !strings.Contains(string(got), `"prompt_cache_key":"review:thread"`) || !strings.Contains(string(got), `"text":"0.148.0"`) {
					t.Fatal(string(got), err)
				}
			}
			restored, err := s.RestoreHeaders(ctx, http.Header{"Version": {"upstream-version"}, "User-Agent": {"upstream-agent"}})
			if err != nil || restored.Get("Version") != "upstream-version" || restored.Get("User-Agent") != "upstream-agent" {
				t.Fatal(restored, err)
			}
			if target.Profile.Features.Headers["Version"] != "0.149.0" {
				t.Fatal("profile mutated")
			}
		})
	}
}
