package wsdisguise

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/codex/clientdisguise"
	"github.com/doraemonkeys/switch-a/internal/model"
)

func TestConnectionUsesOriginalRequestRoleForHandshakeAndTurns(t *testing.T) {
	const desktop = "Codex Desktop/0.156.0 (Windows 10.0.26200; x86_64) unknown (Codex Desktop; 26.9)"
	const browser = "codex-browser-use/0.155.0 (Windows 10; x86_64) unknown (codex-browser-use; 0.1.0)"
	for _, tc := range []struct{ input, wantUA, wantOriginator string }{
		{browser, "codex-browser-use/0.156.0 (Windows 10.0.26200; x86_64) unknown (codex-browser-use; 0.1.0)", "codex-browser-use"},
		{"codex-tui/0.155.0 (Windows 10; x86_64) terminal", desktop, "Codex Desktop"},
	} {
		t.Run(tc.wantOriginator, func(t *testing.T) {
			ctx := context.Background()
			provider := route()
			profile := clientdisguise.ProfileRevision{Tuple: clientdisguise.Tuple{ClientType: "desktop", Platform: "windows", Arch: "amd64"}, Features: clientdisguise.Features{UserAgent: desktop, Originator: "Codex Desktop", Headers: map[string]string{"Version": "0.156.0"}}}
			repo := repository{target: clientdisguise.TargetSnapshot{Policy: clientdisguise.Policy{Enabled: true}, Profile: profile}}
			headers := http.Header{"User-Agent": {tc.input}, "Originator": {"Codex Desktop"}, "Version": {"0.155.0"}}
			session, err := New(ctx, repo, []model.Provider{provider}, headers, "role-connection", nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := session.Operation().Commit(ctx, &provider); err != nil {
				t.Fatal(err)
			}
			if err := session.Select(&provider); err != nil {
				t.Fatal(err)
			}
			frozen := session.Current()
			handshake, err := frozen.Headers(ctx, headers)
			if err != nil || handshake.Get("User-Agent") != tc.wantUA || handshake.Get("Originator") != tc.wantOriginator || handshake.Get("Version") != "0.156.0" {
				t.Fatal("handshake lost request role", handshake, err)
			}
			for range 2 {
				frame, err := frozen.ClientFrame(ctx, []byte(`{"type":"response.create","client_metadata":{"client_version":"0.155.0","originator":"Codex Desktop"},"previous_response_id":"response","prompt_cache_key":"cache"}`))
				if err != nil {
					t.Fatal(err)
				}
				var result struct {
					Metadata map[string]string `json:"client_metadata"`
					Previous string            `json:"previous_response_id"`
					Cache    string            `json:"prompt_cache_key"`
				}
				if err := json.Unmarshal(frame, &result); err != nil {
					t.Fatal(err)
				}
				if result.Metadata["originator"] != tc.wantOriginator || result.Metadata["client_version"] != "0.156.0" || result.Previous != "response" || result.Cache != "cache" {
					t.Fatal("turn disagrees with frozen handshake or changed continuity", string(frame))
				}
				repo.target.Profile.Features.Headers["Version"] = "9.0.0"
				headers.Set("Originator", "codex-tui")
				if err := session.Select(&provider); err != nil || session.Current() != frozen {
					t.Fatal("reselection replaced the connection profile", err)
				}
			}
		})
	}
}
