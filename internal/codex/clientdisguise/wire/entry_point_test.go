package wire

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	disguise "github.com/doraemonkeys/switch-a/internal/codex/clientdisguise"
	"github.com/doraemonkeys/switch-a/internal/codex/clientdisguise/officialversion"
)

func TestEntryPointSelectionIsConsistentAcrossRequestCarriers(t *testing.T) {
	const originalUA = "codex-tui/0.149.0 (Windows 10.0.26200; x86_64) Terminal/1.2 (codex-tui; 0.149.0)"
	ctx := context.Background()
	for _, profile := range disguise.BuiltinProfiles() {
		if profile.Tuple.Platform != "windows" || profile.Tuple.Arch != "amd64" || profile.Features.UserAgent != "" {
			continue
		}
		for _, release := range []string{"", "0.151.0"} {
			t.Run(profile.ID+"/"+release, func(t *testing.T) {
				wantVersion := release
				if wantVersion == "" {
					wantVersion = profile.ClientVersion
				}
				originator := profile.Features.Originator
				wantUA := originator + "/" + wantVersion + " (Windows 10.0.26200; x86_64) Terminal/1.2 (" + originator + "; " + wantVersion + ")"
				s := newPrimarySession(disguise.TargetSnapshot{
					Policy: disguise.Policy{Enabled: true}, Profile: profile,
					OfficialVersion: officialversion.Release{Version: release},
				}, "entry-point-selection")
				metadata := map[string]string{
					"user_agent": originalUA, "originator": "codex-tui", "client_version": "0.149.0",
					"thread_id": "thread", "session_id": "session", "os_version": "10.0.26200",
				}
				rawMetadata, err := json.Marshal(metadata)
				if err != nil {
					t.Fatal(err)
				}
				original := http.Header{
					"User-Agent": {originalUA}, "Originator": {"codex-tui"}, "Version": {"0.149.0"},
					"X-Codex-Turn-Metadata": {string(rawMetadata)}, "Thread-Id": {"thread"},
					"Session-Id": {"session"}, "X-Codex-Window-Id": {"thread:1"}, "Accept-Encoding": {"br"},
				}
				untouched := original.Clone()
				derived, err := s.Headers(ctx, original)
				if err != nil || derived.Get("User-Agent") != wantUA || derived.Get("Originator") != originator || derived.Get("Version") != wantVersion {
					t.Fatal("incorrect header identity", derived, err)
				}
				for _, name := range []string{"Thread-Id", "Session-Id", "X-Codex-Window-Id", "Accept-Encoding"} {
					if derived.Get(name) != original.Get(name) {
						t.Fatalf("unrelated header %s changed", name)
					}
				}
				checkMetadata := func(raw []byte) {
					t.Helper()
					var got map[string]string
					if err := json.Unmarshal(raw, &got); err != nil {
						t.Fatal(err)
					}
					want := map[string]string{
						"user_agent": wantUA, "originator": originator, "client_version": wantVersion,
						"thread_id": "thread", "session_id": "session", "os_version": "10.0.26200",
					}
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("metadata = %#v, want %#v", got, want)
					}
				}
				checkMetadata([]byte(derived.Get("X-Codex-Turn-Metadata")))
				body := []byte(`{"type":"response.create","client_metadata":` + string(rawMetadata) + `,"prompt_cache_key":"thread:cache","previous_response_id":"response","input":[` + string(rawMetadata) + `]}`)
				for _, transform := range []func(context.Context, []byte) ([]byte, error){s.RequestJSON, s.ClientFrame} {
					output, err := transform(ctx, body)
					if err != nil {
						t.Fatal(err)
					}
					var got struct {
						Metadata         json.RawMessage   `json:"client_metadata"`
						Input            []json.RawMessage `json:"input"`
						CacheKey         string            `json:"prompt_cache_key"`
						PreviousResponse string            `json:"previous_response_id"`
					}
					if err := json.Unmarshal(output, &got); err != nil {
						t.Fatal(err)
					}
					checkMetadata(got.Metadata)
					if len(got.Input) != 1 || !bytes.Equal(got.Input[0], rawMetadata) || got.CacheKey != "thread:cache" || got.PreviousResponse != "response" {
						t.Fatal("conversation content or continuity changed", string(output))
					}
				}
				if !reflect.DeepEqual(original, untouched) {
					t.Fatal("original request mutated")
				}
				replay, err := s.Headers(ctx, original)
				if err != nil || !reflect.DeepEqual(derived, replay) {
					t.Fatal("replay changed projection", replay, err)
				}
				if originalUA != wantUA {
					found := false
					for _, difference := range s.Differences() {
						if difference.Carrier == "header" && difference.FieldPath == "User-Agent" && difference.Original == originalUA && difference.Derived == wantUA {
							found = true
						}
					}
					if !found {
						t.Fatal("missing original/derived UA diagnostic")
					}
				}
			})
		}
	}
}

func TestOriginatorHeaderAliasesAgreeWithProtocolMetadata(t *testing.T) {
	for _, typed := range []string{"", "codex_exec"} {
		features := disguise.Features{Originator: typed, Headers: map[string]string{"originator": "codex_cli_rs", "Originator": "codex-tui"}}
		wantOriginator := "codex-tui"
		if typed != "" {
			wantOriginator = typed
		}
		s := newPrimarySession(disguise.TargetSnapshot{Policy: disguise.Policy{Enabled: true}, Profile: disguise.ProfileRevision{Features: features}}, "alias-selection")
		headers, err := s.Headers(context.Background(), http.Header{"User-Agent": {"codex_cli_rs/0.149.0 (Linux; x86_64)"}})
		if err != nil || headers.Get("Originator") != wantOriginator || !strings.HasPrefix(headers.Get("User-Agent"), wantOriginator+"/") {
			t.Fatal(headers, err)
		}
		body, err := s.RequestJSON(context.Background(), []byte(`{"client_metadata":{"originator":"original","user_agent":"codex_cli_rs/0.149.0 (Linux; x86_64)"}}`))
		if err != nil || !strings.Contains(string(body), `"originator":"`+wantOriginator+`"`) || !strings.Contains(string(body), `"user_agent":"`+wantOriginator+`/`) {
			t.Fatal(string(body), err)
		}
	}
}

func TestPartialProfilePreservesUnchangedUserAgentValues(t *testing.T) {
	s := newPrimarySession(disguise.TargetSnapshot{
		Policy:  disguise.Policy{Enabled: true},
		Profile: disguise.ProfileRevision{Features: disguise.Features{Originator: "codex-tui"}},
	}, "unchanged-user-agent")
	original := http.Header{
		"User-Agent": {"codex-tui/0.149.0 (Linux; x86_64)", "forwarded-agent/1.0"},
		"Originator": {"codex-tui"},
	}
	got, err := s.Headers(context.Background(), original)
	if err != nil || !reflect.DeepEqual(got, original) || len(s.Differences()) != 0 {
		t.Fatal("unchanged identity rewrote the original header values", got, err)
	}
}

func TestEntryPointSelectionDoesNotRewriteDisabledOrResponseTraffic(t *testing.T) {
	target := disguise.TargetSnapshot{Profile: disguise.ProfileRevision{Features: disguise.Features{Originator: "codex_exec"}}}
	s := newPrimarySession(target, "disabled")
	original := http.Header{"User-Agent": {"codex-tui/0.149.0 (Linux; x86_64)"}, "Originator": {"thread-override"}}
	got, err := s.Headers(context.Background(), original)
	if err != nil || !reflect.DeepEqual(got, original) {
		t.Fatal(got, err)
	}
	target.Policy.Enabled = true
	s = newPrimarySession(target, "response")
	got, err = s.RestoreHeaders(context.Background(), original)
	if err != nil || !reflect.DeepEqual(got, original) {
		t.Fatal(got, err)
	}
	body := []byte(`{"type":"response.completed","client_metadata":{"user_agent":"codex-tui/0.149.0 (Linux; x86_64)","originator":"thread-override"}}`)
	for _, restore := range []func(context.Context, []byte) ([]byte, error){s.ResponseJSON, s.ServerFrame} {
		got, err := restore(context.Background(), body)
		if err != nil || !bytes.Equal(got, body) {
			t.Fatal(string(got), err)
		}
	}
}
