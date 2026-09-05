package wire

import (
	"bytes"
	"context"
	"net/http"
	"testing"
)

func TestObservedFeaturePositionsDoNotInventMissingEnvironment(t *testing.T) {
	s := testSession()
	s.target.Profile.ClientVersion = "2.0.0"
	s.target.Profile.Features.DesktopBuild = "42"
	s.target.Profile.Features.OSVersion = "10.1"
	s.target.Profile.Features.Headers = map[string]string{"X-Stainless-OS": ""}
	headers, err := s.Headers(context.Background(), http.Header{"Version": {"1.0.0"}, "X-Client-Version": {"1.0.0"}, "X-Codex-Client-Version": {"1.0.0"}, "X-Codex-Desktop-Build": {"1"}, "X-Codex-Os-Version": {"9"}, "X-Stainless-Os": {"old"}})
	if err != nil || headers.Get("Version") != "2.0.0" || headers.Get("X-Client-Version") != "2.0.0" || headers.Get("X-Codex-Desktop-Build") != "42" || headers.Get("X-Codex-Os-Version") != "10.1" || headers.Get("X-Stainless-OS") != "" {
		t.Fatal(headers, err)
	}
	original := []byte(`{"version":"business","client_metadata":{"user_agent":"old","originator":"old","client_version":"1","version":"1","desktop_build":"1","os_version":"9","unknown":"keep"},"input":[{"client_version":"business"}]}`)
	got, err := s.RequestJSON(context.Background(), original)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{`"version":"business"`, `"client_version":"2.0.0"`, `"desktop_build":"42"`, `"os_version":"10.1"`, `"user_agent":"sample-agent"`, `"originator":"sample-origin"`, `"unknown":"keep"`, `"input":[{"client_version":"business"}]`} {
		if !bytes.Contains(got, []byte(fragment)) {
			t.Fatalf("missing %s in %s", fragment, got)
		}
	}
	s.target.Profile.Features.OSVersion = ""
	original = []byte(`{"client_metadata":{"os_version":"keep","desktop_build":42,"client_version":"","originator":null}}`)
	got, err = s.RequestJSON(context.Background(), original)
	if err != nil || !bytes.Equal(got, original) {
		t.Fatal(string(got), err)
	}
	bare, err := s.Headers(context.Background(), nil)
	if err != nil || bare.Get("Version") != "" || bare.Get("X-Codex-Desktop-Build") != "" {
		t.Fatal(bare, err)
	}
	if s.profileFeature("unsupported") != "" {
		t.Fatal("invented feature")
	}
}
