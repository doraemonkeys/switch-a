package wire

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	disguise "github.com/doraemonkeys/switch-a/internal/codex/clientdisguise"
	"github.com/doraemonkeys/switch-a/internal/codex/clientdisguise/officialversion"
)

func TestBrowserUseInputAcrossRequestCarriers(t *testing.T) {
	const originalUA = "codex-browser-use/0.155.0-alpha.2.6 (Windows 10.0.26200; x86_64) unknown (codex-browser-use; 0.1.0)"
	const originalVersion = "0.155.0-alpha.2.6"
	const sampledUA = "codex-browser-use/0.155.0-alpha.2.7 (Windows 10.0.26200; x86_64) unknown (codex-browser-use; 0.2.0)"
	profiles := make(map[string]disguise.ProfileRevision)
	for _, profile := range disguise.BuiltinProfiles() {
		profiles[profile.ID] = profile
	}
	profiles["reference"] = disguise.ProfileRevision{
		Features: disguise.Features{UserAgent: sampledUA, Originator: "codex-browser-use"},
	}
	for _, tc := range []struct {
		profileID, wantUA, wantVersion, wantOriginator string
	}{
		{"builtin-browser-use-windows-amd64", originalUA, originalVersion, "codex-browser-use"},
		{"builtin-exec-windows-amd64", "codex_exec/0.155.0-alpha.2.6 (Windows 10.0.26200; x86_64) unknown (codex-browser-use; 0.1.0)", originalVersion, "codex_exec"},
		{"builtin-desktop-windows-amd64", "Codex Desktop/0.150.0-alpha.8 (Windows 10.0.26200; x86_64) unknown (Codex Desktop; 26.820.60940)", "0.150.0-alpha.8", "Codex Desktop"},
		{"reference", sampledUA, "0.155.0-alpha.2.7", "codex-browser-use"},
	} {
		for _, release := range []string{"", "0.156.0"} {
			t.Run(tc.profileID+"/"+release, func(t *testing.T) {
				profile, ok := profiles[tc.profileID]
				if !ok {
					t.Fatal("missing profile", tc.profileID)
				}
				wantUA, wantVersion := tc.wantUA, tc.wantVersion
				if release != "" {
					wantUA = strings.Replace(wantUA, "/"+wantVersion, "/"+release, 1)
					wantVersion = release
				}
				s := NewSession(disguise.TargetSnapshot{
					Policy: disguise.Policy{Enabled: true}, Profile: profile,
					OfficialVersion: officialversion.Release{Version: release},
				}, "browser-use-input")
				metadata := map[string]string{
					"user_agent": originalUA, "originator": "codex-browser-use", "client_version": originalVersion,
					"thread_id": "thread", "session_id": "session", "window_id": "thread:1",
				}
				rawMetadata, err := json.Marshal(metadata)
				if err != nil {
					t.Fatal(err)
				}
				wantMetadata := map[string]string{
					"user_agent": wantUA, "originator": tc.wantOriginator, "client_version": wantVersion,
					"thread_id": "thread", "session_id": "session", "window_id": "thread:1",
				}
				original := http.Header{
					"User-Agent": {originalUA}, "Originator": {"codex-browser-use"}, "Version": {originalVersion},
					"X-Codex-Turn-Metadata": {string(rawMetadata)}, "Thread-Id": {"thread"},
					"Session-Id": {"session"}, "X-Codex-Window-Id": {"thread:1"}, "Accept-Encoding": {"br"},
				}
				want := original.Clone()
				want.Set("User-Agent", wantUA)
				want.Set("Originator", tc.wantOriginator)
				want.Set("Version", wantVersion)
				derived, err := s.Headers(context.Background(), original)
				if err != nil {
					t.Fatal(err)
				}
				var gotMetadata map[string]string
				if err := json.Unmarshal([]byte(derived.Get("X-Codex-Turn-Metadata")), &gotMetadata); err != nil {
					t.Fatal(err)
				}
				want.Set("X-Codex-Turn-Metadata", derived.Get("X-Codex-Turn-Metadata"))
				if !reflect.DeepEqual(derived, want) || !reflect.DeepEqual(gotMetadata, wantMetadata) {
					t.Fatal("headers and metadata disagree with the selected profile", derived)
				}
				payload := []byte(`{"type":"response.create","client_metadata":` + string(rawMetadata) + `,"prompt_cache_key":"cache","previous_response_id":"response","input":[` + string(rawMetadata) + `]}`)
				for _, transform := range []func(context.Context, []byte) ([]byte, error){s.RequestJSON, s.ClientFrame} {
					output, err := transform(context.Background(), payload)
					if err != nil {
						t.Fatal(err)
					}
					var got struct {
						Metadata         map[string]string   `json:"client_metadata"`
						Input            []map[string]string `json:"input"`
						CacheKey         string              `json:"prompt_cache_key"`
						PreviousResponse string              `json:"previous_response_id"`
					}
					if err := json.Unmarshal(output, &got); err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(got.Metadata, wantMetadata) || !reflect.DeepEqual(got.Input, []map[string]string{metadata}) || got.CacheKey != "cache" || got.PreviousResponse != "response" {
						t.Fatal("request profile or conversation identity changed incorrectly", string(output))
					}
				}
				if original.Get("User-Agent") != originalUA || original.Get("X-Codex-Turn-Metadata") != string(rawMetadata) {
					t.Fatal("original platform evidence mutated")
				}
				if originalUA != wantUA {
					wantDifference := Difference{Carrier: "header", FieldPath: "User-Agent", Original: originalUA, Derived: wantUA}
					found := false
					for _, difference := range s.Differences() {
						found = found || difference == wantDifference
					}
					if !found {
						t.Fatal("missing original/derived UA evidence", s.Differences())
					}
				}
			})
		}
	}
}
