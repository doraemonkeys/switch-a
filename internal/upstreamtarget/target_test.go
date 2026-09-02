package upstreamtarget

import (
	"errors"
	"net/url"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/apicontract"
)

func TestBuildPreservesEscapedPathAndMergesRawQueries(t *testing.T) {
	requestURL := mustParseRequestURL(t, "/gemini/v1beta/models/org%2Fmodel:generateContent?alt=sse&label=a%20b")
	target, err := Build("https://proxy.example/google?tenant=a%2Fb", requestURL, string(apicontract.APITypeGemini))
	if err != nil {
		t.Fatal(err)
	}

	want := "https://proxy.example/google/v1beta/models/org%2Fmodel:generateContent?tenant=a%2Fb&alt=sse&label=a%20b"
	if got := target.String(); got != want {
		t.Fatalf("target = %q, want %q", got, want)
	}
	if got, want := target.Path, "/google/v1beta/models/org/model:generateContent"; got != want {
		t.Fatalf("Path = %q, want %q", got, want)
	}
	if got, want := target.RawPath, "/google/v1beta/models/org%2Fmodel:generateContent"; got != want {
		t.Fatalf("RawPath = %q, want %q", got, want)
	}
}

func TestBuildAppliesOnlyExplicitPathPolicies(t *testing.T) {
	tests := []struct {
		name       string
		baseURL    string
		requestURL string
		apiType    string
		want       string
	}{
		{
			name:       "claude namespace",
			baseURL:    "https://provider.example/base/",
			requestURL: "/claude/v1/messages",
			apiType:    string(apicontract.APITypeClaude),
			want:       "https://provider.example/base/v1/messages",
		},
		{
			name:       "encoded optional v1",
			baseURL:    "https://provider.example",
			requestURL: "/codex/%76%31/responses/org%2Fmodel",
			apiType:    string(apicontract.APITypeCodex),
			want:       "https://provider.example/responses/org%2Fmodel",
		},
		{
			name:       "encoded slash does not activate optional v1",
			baseURL:    "https://provider.example",
			requestURL: "/codex/v1%2Fresponses",
			apiType:    string(apicontract.APITypeCodex),
			want:       "https://provider.example/v1%2Fresponses",
		},
		{
			name:       "double encoded percent",
			baseURL:    "https://provider.example",
			requestURL: "/gemini/v1beta/models/org%252Fmodel:generateContent",
			apiType:    string(apicontract.APITypeGemini),
			want:       "https://provider.example/v1beta/models/org%252Fmodel:generateContent",
		},
		{
			name:       "repeated slash",
			baseURL:    "https://provider.example/root",
			requestURL: "/gemini/v1beta//models/gemini-pro:generateContent",
			apiType:    string(apicontract.APITypeGemini),
			want:       "https://provider.example/root/v1beta//models/gemini-pro:generateContent",
		},
		{
			name:       "encoded dot segments",
			baseURL:    "https://provider.example",
			requestURL: "/gemini/v1beta/models/%2E%2E/gemini-pro:generateContent",
			apiType:    string(apicontract.APITypeGemini),
			want:       "https://provider.example/v1beta/models/%2E%2E/gemini-pro:generateContent",
		},
		{
			name:       "encoded slash in base path",
			baseURL:    "https://provider.example/root%2Ftenant",
			requestURL: "/claude/v1/messages",
			apiType:    string(apicontract.APITypeClaude),
			want:       "https://provider.example/root%2Ftenant/v1/messages",
		},
		{
			name:       "exact namespace becomes root",
			baseURL:    "https://provider.example/base",
			requestURL: "/custom/tool",
			apiType:    "custom:tool",
			want:       "https://provider.example/base/",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			target, err := Build(testCase.baseURL, mustParseRequestURL(t, testCase.requestURL), testCase.apiType)
			if err != nil {
				t.Fatal(err)
			}
			if got := target.String(); got != testCase.want {
				t.Fatalf("target = %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestBuildPreservesQueryPresence(t *testing.T) {
	tests := []struct {
		name       string
		baseURL    string
		requestURL string
		want       string
	}{
		{name: "base query", baseURL: "https://provider.example?tenant=a", requestURL: "/claude/v1/messages", want: "https://provider.example/v1/messages?tenant=a"},
		{name: "request query", baseURL: "https://provider.example", requestURL: "/claude/v1/messages?debug=true", want: "https://provider.example/v1/messages?debug=true"},
		{name: "empty request query", baseURL: "https://provider.example", requestURL: "/claude/v1/messages?", want: "https://provider.example/v1/messages?"},
		{name: "empty base query", baseURL: "https://provider.example?", requestURL: "/claude/v1/messages", want: "https://provider.example/v1/messages?"},
		{name: "duplicate keys", baseURL: "https://provider.example?key=base", requestURL: "/claude/v1/messages?key=client&key=second", want: "https://provider.example/v1/messages?key=base&key=client&key=second"},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			target, err := Build(testCase.baseURL, mustParseRequestURL(t, testCase.requestURL), string(apicontract.APITypeClaude))
			if err != nil {
				t.Fatal(err)
			}
			if got := target.String(); got != testCase.want {
				t.Fatalf("target = %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestValidateBaseURL(t *testing.T) {
	tests := []struct {
		name      string
		raw       string
		want      error
		wantError bool
	}{
		{name: "absolute", raw: "https://provider.example/base?tenant=a"},
		{name: "missing scheme and host", raw: "provider.example/base", want: ErrBaseURLMustBeAbsolute, wantError: true},
		{name: "malformed", raw: "https://provider.example/%zz", wantError: true},
		{name: "fragment", raw: "https://provider.example/base#route", want: ErrBaseURLHasFragment, wantError: true},
		{name: "empty fragment", raw: "https://provider.example/base#", want: ErrBaseURLHasFragment, wantError: true},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			err := ValidateBaseURL(testCase.raw)
			if !testCase.wantError {
				if err != nil {
					t.Fatalf("ValidateBaseURL() error = %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("ValidateBaseURL() accepted invalid URL")
			}
			if testCase.want != nil && !errors.Is(err, testCase.want) {
				t.Fatalf("ValidateBaseURL() error = %v, want %v", err, testCase.want)
			}
		})
	}
}

func TestBuildRejectsMissingRequestAndBaseFragment(t *testing.T) {
	if _, err := Build("https://provider.example", nil, string(apicontract.APITypeClaude)); !errors.Is(err, ErrRequestURLRequired) {
		t.Fatalf("nil request error = %v, want %v", err, ErrRequestURLRequired)
	}
	if _, err := Build("https://provider.example#fragment", mustParseRequestURL(t, "/claude/v1/messages"), string(apicontract.APITypeClaude)); !errors.Is(err, ErrBaseURLHasFragment) {
		t.Fatalf("fragment error = %v, want %v", err, ErrBaseURLHasFragment)
	}
}

func TestEscapedPathHelpers(t *testing.T) {
	for _, testCase := range []struct {
		name        string
		basePath    string
		requestPath string
		want        string
	}{
		{name: "empty base", requestPath: "/request", want: "/request"},
		{name: "empty request", basePath: "/base", want: "/base"},
		{name: "missing boundary slashes", basePath: "/base", requestPath: "request", want: "/base/request"},
		{name: "single boundary slash", basePath: "/base/", requestPath: "request", want: "/base/request"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := joinEscapedPaths(testCase.basePath, testCase.requestPath); got != testCase.want {
				t.Fatalf("joinEscapedPaths() = %q, want %q", got, testCase.want)
			}
		})
	}

	if err := setEscapedPath(&url.URL{}, "/invalid%zz"); err == nil {
		t.Fatal("setEscapedPath() accepted an invalid escape")
	}
}

func mustParseRequestURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.ParseRequestURI(raw)
	if err != nil {
		t.Fatalf("url.ParseRequestURI(%q): %v", raw, err)
	}
	return parsed
}
