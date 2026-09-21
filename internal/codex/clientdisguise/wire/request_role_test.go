package wire

import (
	"bytes"
	"context"
	"net/http"
	"reflect"
	"testing"

	disguise "github.com/doraemonkeys/switch-a/internal/codex/clientdisguise"
)

func TestCrossPlatformProjectionDoesNotCarryAnUnobservedOSRelease(t *testing.T) {
	ctx := context.Background()
	const browser = "codex-browser-use/0.155.0 (Linux 6.8; aarch64) unknown (codex-browser-use; 0.1.0)"
	profile := disguise.ProfileRevision{Tuple: disguise.Tuple{ClientType: "desktop", Platform: "windows", Arch: "amd64"}, ClientVersion: "0.156.0", Features: disguise.Features{Originator: "Codex Desktop"}}
	original := http.Header{"User-Agent": {browser}, "X-Codex-Os-Version": {"6.8"}, "Version": {"0.155.0"}, "Thread-Id": {"thread"}}
	for _, enabled := range []bool{false, true} {
		s := NewSession(disguise.TargetSnapshot{Policy: disguise.Policy{Enabled: enabled}, Profile: profile}, "cross-platform", disguise.ProjectPlatform(original))
		head, err := s.Headers(ctx, original)
		if err != nil {
			t.Fatal(err)
		}
		if !enabled {
			if !reflect.DeepEqual(head, original) {
				t.Fatal("disabled policy changed headers", head)
			}
			continue
		}
		if head.Get("X-Codex-Os-Version") != "" || head.Get("Thread-Id") != "thread" || head.Get("User-Agent") != "codex-browser-use/0.156.0 (Windows; x86_64) unknown (codex-browser-use; 0.1.0)" {
			t.Fatal("source OS release survived platform projection", head)
		}
		input := []byte(`{ "client_metadata":{ "os_version":"6.8", "originator":"codex-browser-use", "client_version":"0.155.0" }, "input":[{"os_version":"6.8"}] }`)
		want := []byte(`{ "client_metadata":{ "os_version":"", "originator":"codex-browser-use", "client_version":"0.156.0" }, "input":[{"os_version":"6.8"}] }`)
		got, err := s.RequestJSON(ctx, input)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatal("metadata projection changed unrelated JSON bytes", string(got), err)
		}
		replayed, err := s.Headers(ctx, original)
		if err != nil || !reflect.DeepEqual(replayed, head) || original.Get("X-Codex-Os-Version") != "6.8" {
			t.Fatal("retry or original evidence changed", replayed, err)
		}
		restored, err := s.ResponseJSON(ctx, input)
		if err != nil || !bytes.Equal(restored, input) {
			t.Fatal("response client features were rewritten", string(restored), err)
		}
	}
}

func TestExplicitEmptyOriginatorHeaderStillClearsTheOriginal(t *testing.T) {
	s := newPrimarySession(disguise.TargetSnapshot{Policy: disguise.Policy{Enabled: true}, Profile: disguise.ProfileRevision{Features: disguise.Features{Headers: map[string]string{"originator": ""}}}}, "empty-originator")
	got, err := s.Headers(context.Background(), http.Header{"Originator": {"original"}})
	if err != nil || got.Get("Originator") != "" {
		t.Fatal("explicit empty header observation was ignored", got, err)
	}
}

func TestMissingUAUsesOriginatorRoleWithoutInsertingAPrimaryUA(t *testing.T) {
	headers := http.Header{"Originator": {"codex-browser-use"}, "Version": {"0.155.0"}}
	s := NewSession(disguise.TargetSnapshot{Policy: disguise.Policy{Enabled: true}, Profile: disguise.BuiltinAccountProfile()}, "missing-ua", disguise.ProjectPlatform(headers))
	got, err := s.Headers(context.Background(), headers)
	if err != nil || got.Get("User-Agent") != "" || got.Get("Originator") != "codex-browser-use" || got.Get("Version") != "0.150.0-alpha.8" {
		t.Fatal("missing UA was replaced with the primary client's UA", got, err)
	}
}
