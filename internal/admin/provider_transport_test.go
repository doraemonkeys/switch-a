package admin

import (
	"net/http"
	"reflect"
	"testing"
)

func TestProviderTransportValidation(t *testing.T) {
	route := APITypeInput{APIType: "codex", Transport: "http", BaseURL: "https://example.test", CredentialSessionID: "http-key"}
	ws := route
	ws.Transport = "websocket"
	ws.CredentialSessionID = "ws-key"
	if err := validateAPITypeInputs([]APITypeInput{route, ws}); err != "" {
		t.Fatal(err)
	}
	if err := validateAPITypeInputs([]APITypeInput{ws}); err != "" {
		t.Fatal(err)
	}
	for _, routes := range [][]APITypeInput{
		{route, route}, {}, {{APIType: "codex", Transport: "sse", BaseURL: route.BaseURL, CredentialSessionID: "key"}},
		{{APIType: "claude", Transport: "websocket", BaseURL: route.BaseURL, CredentialSessionID: "key"}},
	} {
		if err := validateAPITypeInputs(routes); err == "" {
			t.Fatal("invalid route configuration accepted", routes)
		}
	}
}

func TestProviderTransportBackupPreservesExplicitRoutesAndUpgradesHistorical(t *testing.T) {
	for _, tc := range []struct {
		name   string
		routes []ExportedAPIType
		want   []ExportedAPIType
	}{
		{"historical", []ExportedAPIType{{APIType: "codex", BaseURL: "https://example.test", CredentialSessionID: "http-key"}}, []ExportedAPIType{
			{APIType: "codex", Transport: "http", BaseURL: "https://example.test", CredentialSessionID: "http-key"},
			{APIType: "codex", Transport: "websocket", BaseURL: "https://example.test", CredentialSessionID: "http-key"},
		}},
		{"http-only", []ExportedAPIType{{APIType: "codex", Transport: "http", BaseURL: "https://example.test", CredentialSessionID: "http-key"}}, nil},
		{"ws-only", []ExportedAPIType{{APIType: "codex", Transport: "websocket", BaseURL: "https://ws.example.test", CredentialSessionID: "ws-key"}}, nil},
		{"distinct-keys", []ExportedAPIType{
			{APIType: "codex", Transport: "http", BaseURL: "https://example.test", CredentialSessionID: "http-key"},
			{APIType: "codex", Transport: "websocket", BaseURL: "https://ws.example.test", CredentialSessionID: "ws-key"},
		}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler, store, _ := testHandler()
			request := ImportConfigRequest{Version: ConfigExportVersion, CredentialSessions: []ExportedCredentialSession{importedTestSession("http-key", "http-secret"), importedTestSession("ws-key", "ws-secret")}, Providers: []ExportedProvider{{
				ID: "p", Name: "Provider", AuthMode: "bearer", Weight: 1, Enabled: true, APITypes: tc.routes,
			}}}
			response := performConfigImport(t, handler, request, false)
			if response.Code != http.StatusOK {
				t.Fatalf("import: %d %s", response.Code, response.Body)
			}
			exported := buildExportedProvider(store.providers["p"])
			expected := tc.want
			if expected == nil {
				expected = tc.routes
			}
			if !reflect.DeepEqual(exported.APITypes, expected) {
				t.Fatalf("round trip: %+v, want %+v", exported.APITypes, expected)
			}
			// Current backups must remain explicit on every subsequent import.
			request.Providers = []ExportedProvider{exported}
			if response := performConfigImport(t, handler, request, false); response.Code != http.StatusOK {
				t.Fatal(response.Body.String())
			}
			if got := buildExportedProvider(store.providers["p"]).APITypes; !reflect.DeepEqual(got, expected) {
				t.Fatal("second import changed capability", got)
			}
		})
	}
}
