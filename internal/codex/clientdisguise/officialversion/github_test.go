package officialversion

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type doFunc func(*http.Request) (*http.Response, error)

func (f doFunc) Do(r *http.Request) (*http.Response, error) { return f(r) }

func TestGitHubStableSelection(t *testing.T) {
	for _, tc := range []struct {
		name, latest string
		pages        [][]githubRelease
		want         string
	}{
		{"latest", `{"tag_name":"rust-v0.150.0"}`, nil, "0.150.0"},
		{"other component", `{"tag_name":"rusty-v8-1.0.0"}`, [][]githubRelease{{{Tag: "rust-v0.149.0"}, {Tag: "rust-v0.151.0"}, {Tag: "rust-v0.152.0", Draft: true}, {Tag: "rust-v0.153.0", Prerelease: true}, {Tag: "rust-v0.154.0-alpha.1"}, {Tag: "rust-v01.155.0"}}}, "0.151.0"},
		{"malformed latest", "{", [][]githubRelease{{{Tag: "rust-v0.150.1"}}}, "0.150.1"},
		{"no stable", `{"tag_name":"rust-v0.150.0-alpha.1"}`, [][]githubRelease{{{Tag: "other-1.0.0"}}}, ""},
		{"pagination", `{"tag_name":"other"}`, [][]githubRelease{make([]githubRelease, releasePageSize), {{Tag: "rust-v0.150.0"}}}, "0.150.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Accept") != "application/vnd.github+json" || r.Header.Get("X-GitHub-Api-Version") != githubAPIVersion || r.Header.Get("User-Agent") == "" {
					t.Error(r.Header)
				}
				if r.URL.Path == "/latest" {
					_, _ = w.Write([]byte(tc.latest))
					return
				}
				if r.URL.Query().Get("per_page") != "100" {
					t.Error(r.URL)
				}
				if page >= len(tc.pages) {
					t.Error("unexpected page")
					w.WriteHeader(500)
					return
				}
				_ = json.NewEncoder(w).Encode(tc.pages[page])
				page++
			}))
			defer server.Close()
			client := NewGitHub(server.Client())
			client.endpoint = server.URL
			got, err := client.Latest(context.Background())
			if got.Version != tc.want || (err != nil) != (tc.want == "") {
				t.Fatalf("%+v %v", got, err)
			}
			if got.Version != "" && got.URL != RepositoryURL+"/releases/tag/rust-v"+tc.want {
				t.Fatal(got)
			}
		})
	}
}
func TestGitHubFailures(t *testing.T) {
	for _, status := range []int{403, 404, 429, 500} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }))
		client := NewGitHub(server.Client())
		client.endpoint = server.URL
		if _, err := client.Latest(context.Background()); err == nil {
			t.Fatal(status)
		}
		server.Close()
	}
	failure := errors.New("network unavailable")
	client := NewGitHub(doFunc(func(*http.Request) (*http.Response, error) { return nil, failure }))
	if _, err := client.Latest(context.Background()); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	client.endpoint = ":"
	if err := client.get(context.Background(), ":", &githubRelease{}); err == nil {
		t.Fatal("invalid URL accepted")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/latest" {
			_, _ = w.Write([]byte("{}"))
			return
		}
		_, _ = w.Write([]byte("{"))
	}))
	defer server.Close()
	client = NewGitHub(server.Client())
	client.endpoint = server.URL
	if _, err := client.Latest(context.Background()); err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatal(err)
	}
}
func TestStableVersionOrdering(t *testing.T) {
	for _, pair := range [][2]string{{"0.9.0", "0.10.0"}, {"1.0.0", "2.0.0"}, {"1.0.9", "1.1.0"}, {"0.1.0", "10.0.0"}, {"", "1.0.0"}} {
		if Compare(pair[0], pair[1]) >= 0 || Compare(pair[1], pair[0]) <= 0 || Compare(pair[1], pair[1]) != 0 {
			t.Fatal(pair)
		}
	}
	for _, version := range []string{"", "1.2", "v1.2.3", "1.2.3-alpha.1", "1.2.3+meta", "01.2.3"} {
		if ValidVersion(version) {
			t.Fatal(version)
		}
	}
	if !ValidVersion("0.150.0") {
		t.Fatal("stable rejected")
	}
}
