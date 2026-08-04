package apicontract

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestCatalogMatchesGoldenContracts(t *testing.T) {
	t.Parallel()

	assertJSONGolden(t, "api-catalog.json", Projection())
	assertJSONGolden(t, "api-catalog-internal.json", All())
}

func TestCatalogInvariants(t *testing.T) {
	t.Parallel()

	definitions := All()
	if len(definitions) != 6 {
		t.Fatalf("All() returned %d definitions, want 6", len(definitions))
	}

	seenTypes := make(map[APIType]struct{}, len(definitions))
	seenNamespaces := make(map[string]struct{}, len(definitions))
	seenRoutes := make(map[Route]APIType)
	seenProtocols := make(map[ResponseProtocolID]struct{})
	for index, definition := range definitions {
		if definition.DisplayOrder != index {
			t.Errorf("definition %q display order = %d, want %d", definition.APIType, definition.DisplayOrder, index)
		}
		if _, duplicate := seenTypes[definition.APIType]; duplicate {
			t.Errorf("duplicate API type %q", definition.APIType)
		}
		seenTypes[definition.APIType] = struct{}{}
		if _, duplicate := seenNamespaces[definition.NamespacePattern]; duplicate {
			t.Errorf("duplicate namespace %q", definition.NamespacePattern)
		}
		seenNamespaces[definition.NamespacePattern] = struct{}{}
		if definition.Label == "" || definition.Description == "" {
			t.Errorf("definition %q has empty presentation metadata", definition.APIType)
		}
		if !definition.SemanticErrorSupported {
			t.Errorf("built-in definition %q unexpectedly disables semantic analysis", definition.APIType)
		}
		if definition.RequestDialect == "" {
			t.Errorf("definition %q has no request dialect", definition.APIType)
		}
		if definition.UpstreamPathPolicy != UpstreamPathPreserve && definition.UpstreamPathPolicy != UpstreamPathStripOptionalV1 {
			t.Errorf("definition %q has unknown upstream path policy %q", definition.APIType, definition.UpstreamPathPolicy)
		}
		if len(definition.ResponseProtocolIDs) != 2 {
			t.Errorf("definition %q has %d protocol IDs, want JSON and SSE", definition.APIType, len(definition.ResponseProtocolIDs))
		}
		for _, protocolID := range definition.ResponseProtocolIDs {
			seenProtocols[protocolID] = struct{}{}
		}
		for _, route := range definition.UnnamespacedRoutes {
			if owner, duplicate := seenRoutes[route]; duplicate {
				t.Errorf("route %+v belongs to both %q and %q", route, owner, definition.APIType)
			}
			seenRoutes[route] = definition.APIType
		}
	}
	if len(seenProtocols) != 8 {
		t.Errorf("catalog exposes %d distinct protocols, want 8", len(seenProtocols))
	}
}

func TestCatalogReturnsDefensiveCopies(t *testing.T) {
	t.Parallel()

	definitions := All()
	definitions[0].Label = "mutated"
	definitions[0].ResponseProtocolIDs[0] = "mutated"
	definitions[0].UnnamespacedRoutes[0].Pattern = "/mutated"

	fresh, ok := Lookup(string(APITypeClaude))
	if !ok {
		t.Fatal("Claude definition missing")
	}
	if fresh.Label == "mutated" || fresh.ResponseProtocolIDs[0] == "mutated" || fresh.UnnamespacedRoutes[0].Pattern == "/mutated" {
		t.Fatal("All exposed mutable catalog state")
	}

	projection := Projection()
	projection.APITypes[0].Label = "mutated"
	projection.APITypes[0].ResponseProtocolIDs[0] = "mutated"
	freshProjection := Projection()
	if freshProjection.APITypes[0].Label == "mutated" || freshProjection.APITypes[0].ResponseProtocolIDs[0] == "mutated" {
		t.Fatal("Projection exposed mutable catalog state")
	}
}

func TestProviderAPITypeValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		apiType   string
		valid     bool
		supported bool
	}{
		{apiType: "claude", valid: true, supported: true},
		{apiType: "deepseek-claude", valid: true, supported: true},
		{apiType: "codex", valid: true, supported: true},
		{apiType: "gemini", valid: true, supported: true},
		{apiType: "grok", valid: true, supported: true},
		{apiType: "deepseek-openai", valid: true, supported: true},
		{apiType: "custom:tool", valid: true, supported: false},
		{apiType: "custom:", valid: false, supported: false},
		{apiType: "custom:foo/bar", valid: false, supported: false},
		{apiType: "custom:.", valid: false, supported: false},
		{apiType: "custom:..", valid: false, supported: false},
		{apiType: "Custom:tool", valid: false, supported: false},
		{apiType: "", valid: false, supported: false},
		{apiType: "unknown", valid: false, supported: false},
	}
	for _, test := range tests {
		t.Run(test.apiType, func(t *testing.T) {
			if got := IsValidProviderAPIType(test.apiType); got != test.valid {
				t.Errorf("IsValidProviderAPIType(%q) = %v, want %v", test.apiType, got, test.valid)
			}
			if got := SupportsSemanticErrors(test.apiType); got != test.supported {
				t.Errorf("SupportsSemanticErrors(%q) = %v, want %v", test.apiType, got, test.supported)
			}
		})
	}
}

func TestResolveRequestUsesFrozenRouteSemantics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		method string
		path   string
		want   string
		ok     bool
	}{
		{method: http.MethodPost, path: "/v1/messages", want: "claude", ok: true},
		{method: http.MethodPost, path: "/v1/messages/extra"},
		{method: http.MethodHead, path: "/v1/models", want: "claude", ok: true},
		{method: http.MethodPost, path: "/v1beta/models/x:generateContent", want: "gemini", ok: true},
		{method: http.MethodGet, path: "/v1beta/models/x"},
		{method: http.MethodGet, path: "/responses", want: "codex", ok: true},
		{method: http.MethodPost, path: "/deepseek-openai/v1/chat/completions", want: "deepseek-openai", ok: true},
		{method: http.MethodHead, path: "/grok/v1/models", want: "grok", ok: true},
		{method: http.MethodDelete, path: "/grok/v1/models"},
		{method: http.MethodPost, path: "/custom/tool/v1/messages", want: "custom:tool", ok: true},
		{method: http.MethodPost, path: "/custom/foo/bar", want: "custom:foo", ok: true},
		{method: http.MethodPost, path: "/custom/"},
	}
	for _, test := range tests {
		t.Run(test.method+" "+test.path, func(t *testing.T) {
			got, ok := ResolveRequest(test.method, test.path)
			if got != test.want || ok != test.ok {
				t.Fatalf("ResolveRequest(%q, %q) = (%q, %v), want (%q, %v)", test.method, test.path, got, ok, test.want, test.ok)
			}
		})
	}
}

func TestCustomAPITypeParserDefinesOneRoutableSegment(t *testing.T) {
	t.Parallel()

	tests := []struct {
		apiType string
		toolID  string
		ok      bool
	}{
		{apiType: "custom:tool", toolID: "tool", ok: true},
		{apiType: "custom:a:b", toolID: "a:b", ok: true},
		{apiType: "custom:"},
		{apiType: "custom:foo/bar"},
		{apiType: "custom:."},
		{apiType: "custom:.."},
		{apiType: "claude"},
	}
	for _, test := range tests {
		t.Run(test.apiType, func(t *testing.T) {
			toolID, ok := ParseCustomAPIType(test.apiType)
			if toolID != test.toolID || ok != test.ok {
				t.Fatalf("ParseCustomAPIType(%q) = (%q, %v), want (%q, %v)", test.apiType, toolID, ok, test.toolID, test.ok)
			}
		})
	}
}

func TestRewriteUpstreamPathUsesRequestPathPolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		path     string
		apiType  string
		wantPath string
	}{
		{name: "Claude preserves v1", path: "/claude/v1/messages", apiType: "claude", wantPath: "/v1/messages"},
		{name: "Codex strips optional v1", path: "/codex/v1/responses", apiType: "codex", wantPath: "/responses"},
		{name: "Codex root v1", path: "/v1", apiType: "codex", wantPath: "/"},
		{name: "Codex preserves v1beta", path: "/v1beta/models", apiType: "codex", wantPath: "/v1beta/models"},
		{name: "Gemini preserves v1beta", path: "/gemini/v1beta/models/x", apiType: "gemini", wantPath: "/v1beta/models/x"},
		{name: "Custom strips matching namespace", path: "/custom/tool/v1/messages", apiType: "custom:tool", wantPath: "/v1/messages"},
		{name: "Custom namespace root", path: "/custom/tool", apiType: "custom:tool", wantPath: "/"},
		{name: "Custom preserves a different namespace", path: "/custom/other/v1/messages", apiType: "custom:tool", wantPath: "/custom/other/v1/messages"},
		{name: "Invalid API type preserves path", path: "/v1/messages", apiType: "unknown", wantPath: "/v1/messages"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := RewriteUpstreamPath(test.path, test.apiType); got != test.wantPath {
				t.Fatalf("RewriteUpstreamPath(%q, %q) = %q, want %q", test.path, test.apiType, got, test.wantPath)
			}
		})
	}
}

func TestRequestDialectIsIndependentCatalogPolicy(t *testing.T) {
	t.Parallel()

	if !UsesRequestDialect("deepseek-claude", RequestDialectAnthropicMessages) {
		t.Fatal("DeepSeek Claude request dialect was not cataloged as Anthropic Messages")
	}
	if UsesRequestDialect("claude", RequestDialectOpenAIResponses) || UsesRequestDialect("unknown", RequestDialectAnthropicMessages) {
		t.Fatal("request dialect lookup accepted a different or unknown contract")
	}
}

func TestRouteProjectionsAreDefensiveAndDeterministic(t *testing.T) {
	t.Parallel()

	wantPatterns := []string{"/claude/", "/codex/", "/deepseek-claude/", "/deepseek-openai/", "/gemini/", "/grok/"}
	if got := NamespaceRoutePatterns(); !reflect.DeepEqual(got, wantPatterns) {
		t.Fatalf("NamespaceRoutePatterns() = %v, want %v", got, wantPatterns)
	}
	routes := BareRoutes()
	if len(routes) != 12 {
		t.Fatalf("BareRoutes() returned %d routes, want 12", len(routes))
	}
	routes[0] = BareRoute{Method: http.MethodDelete, Pattern: "/mutated"}
	if fresh := BareRoutes()[0]; fresh.Method == http.MethodDelete || fresh.Pattern == "/mutated" {
		t.Fatal("BareRoutes exposed mutable state")
	}
}

func TestSplitNamespaceRejectsNonNamespacedPaths(t *testing.T) {
	t.Parallel()

	for _, path := range []string{"claude/v1/messages", "/claude", "/unknown/v1/messages"} {
		if apiType, contractPath, ok := SplitNamespace(path); ok || apiType != "" || contractPath != "" {
			t.Errorf("SplitNamespace(%q) = (%q, %q, %v), want empty false result", path, apiType, contractPath, ok)
		}
	}
}

func assertJSONGolden(t *testing.T, name string, got any) {
	t.Helper()

	wantBytes, err := os.ReadFile(filepath.Join("..", "..", "contracts", "internal-error", "v1", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	gotBytes, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal %s value: %v", name, err)
	}
	var wantValue any
	if err := json.Unmarshal(wantBytes, &wantValue); err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
	var gotValue any
	if err := json.Unmarshal(gotBytes, &gotValue); err != nil {
		t.Fatalf("decode marshaled %s value: %v", name, err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Fatalf("%s drifted\ngot:  %s\nwant: %s", name, gotBytes, wantBytes)
	}
}
