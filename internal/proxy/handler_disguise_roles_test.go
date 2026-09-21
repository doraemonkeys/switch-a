package proxy

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/codex/clientdisguise"
)

func TestHTTPDisguiseSelectsRequestRoleBeforeTransformingAnyCarrier(t *testing.T) {
	const desktop = "Codex Desktop/0.156.0 (Windows 10.0.26200; x86_64) unknown (Codex Desktop; 26.9)"
	const browser = "codex-browser-use/0.155.0 (Linux 6.8; aarch64) unknown (codex-browser-use; 0.1.0)"
	const browserTarget = "codex-browser-use/0.156.0 (Windows 10.0.26200; x86_64) unknown (codex-browser-use; 0.1.0)"
	for _, tc := range []struct{ name, incoming, originator, wantUA, wantOriginator string }{
		{"primary", "codex-tui/0.155.0 (Linux 6.8; aarch64) terminal", "codex-tui", desktop, "Codex Desktop"},
		{"browser with a different thread originator", browser, "Codex Desktop", browserTarget, "codex-browser-use"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			received := make(chan struct{}, 1)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("User-Agent") != tc.wantUA || r.Header.Get("Originator") != tc.wantOriginator || r.Header.Get("Version") != "0.156.0" {
					t.Error("header role/version mismatch", r.Header)
				}
				var body struct {
					Metadata map[string]string   `json:"client_metadata"`
					Input    []map[string]string `json:"input"`
					CacheKey string              `json:"prompt_cache_key"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body.Metadata["originator"] != tc.wantOriginator || body.Metadata["client_version"] != "0.156.0" || body.Metadata["os_version"] != "10.0.26200" || body.CacheKey != "cache" || len(body.Input) != 1 || body.Input[0]["originator"] != "business" {
					t.Error("body role or conversation content mismatch", body)
				}
				if r.Header.Get("Thread-Id") != "thread" || r.Header.Get("Session-Id") != "session" || r.Header.Get("Accept-Encoding") != "br" || r.Header.Get("Cookie") != "" {
					t.Error("unrelated request behavior changed", r.Header)
				}
				received <- struct{}{}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"ok":true}`)
			}))
			defer upstream.Close()
			profile := clientdisguise.ProfileRevision{ID: "target-desktop", Tuple: clientdisguise.Tuple{ClientType: "desktop", Platform: "windows", Arch: "amd64"}, ClientVersion: "0.156.0", Features: clientdisguise.Features{UserAgent: desktop, Originator: "Codex Desktop", DesktopBuild: "26.9"}}
			handler, _ := disguiseHandler(t, upstream.URL, &httpDisguiseRepository{profile: &profile}, nil)
			// No UA in the metadata: carrier order and omission cannot undo the
			// decision already made from the original HTTP request.
			body := `{"client_metadata":{"client_version":"0.155.0","originator":"` + tc.originator + `","os_version":"6.8"},"input":[{"originator":"business"}],"prompt_cache_key":"cache"}`
			request := httptest.NewRequest("POST", "/codex/alpha/search", strings.NewReader(body))
			request.Header = http.Header{"Authorization": {proxyCodexTestAuthorization}, "Content-Type": {"application/json"}, "User-Agent": {tc.incoming}, "Originator": {tc.originator}, "Version": {"0.155.0"}, "Thread-Id": {"thread"}, "Session-Id": {"session"}, "Accept-Encoding": {"br"}}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			select {
			case <-received:
			default:
				t.Fatal("request did not reach upstream")
			}
			if request.Header.Get("User-Agent") != tc.incoming || request.Header.Get("Originator") != tc.originator {
				t.Fatal("original evidence was mutated")
			}
		})
	}
}
