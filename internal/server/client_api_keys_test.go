package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/admin/clientapikeyapi"
	"github.com/doraemonkeys/switch-a/internal/clientaccess"
	storepkg "github.com/doraemonkeys/switch-a/internal/store"
	"go.uber.org/zap"
)

func clientAccessServers(t *testing.T) (*Server, *AdminServer, *clientaccess.Service) {
	t.Helper()
	db, err := storepkg.NewSQLiteStore(filepath.Join(t.TempDir(), "access.db"), internal.RealClock{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	service := clientaccess.NewService(clientaccess.ServiceConfig{Store: db.ClientAPIKeyRepository()})
	httpRuntime, wsRuntime := testCodexRuntimes(t)
	proxy := New(Config{Port: "0", Logger: zap.NewNop(), Store: &mockStore{}, CodexHTTP: httpRuntime, CodexWebSocket: wsRuntime, ClientAdmission: service})
	admin := NewAdmin(AdminConfig{Port: "0", AdminToken: "admin-secret", Logger: zap.NewNop(), Store: &mockStore{}, ClientAPIKeys: clientapikeyapi.NewHandler(service, nil)})
	return proxy, admin, service
}

func TestClientAPIKeyItemRoutesPreservePolicyNamedIDs(t *testing.T) {
	db, err := storepkg.NewSQLiteStore(filepath.Join(t.TempDir(), "reserved.db"), internal.RealClock{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	service := clientaccess.NewService(clientaccess.ServiceConfig{Store: db.ClientAPIKeyRepository(), NewID: func() string { return "policy" }})
	if _, err := service.Create(context.Background(), "Original", "key-value"); err != nil {
		t.Fatal(err)
	}
	admin := NewAdmin(AdminConfig{AdminToken: "admin", Logger: zap.NewNop(), Store: &mockStore{}, ClientAPIKeys: clientapikeyapi.NewHandler(service, nil)})
	req := httptest.NewRequest("PUT", "/admin/api/client-api-keys/keys/policy", strings.NewReader(`{"name":"Renamed"}`))
	req.Header.Set("Authorization", "Bearer admin")
	w := httptest.NewRecorder()
	admin.server.Handler.ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"name":"Renamed"`) {
		t.Fatalf("rename status %d: %s", w.Code, w.Body)
	}
	snapshot, err := service.Snapshot(context.Background())
	if err != nil || snapshot.Mode != clientaccess.ModePermissive {
		t.Fatalf("rename changed policy %+v: %v", snapshot, err)
	}
	req = httptest.NewRequest("DELETE", "/admin/api/client-api-keys/keys/policy", nil)
	req.Header.Set("Authorization", "Bearer admin")
	w = httptest.NewRecorder()
	admin.server.Handler.ServeHTTP(w, req)
	if w.Code != 204 {
		t.Fatalf("delete status %d: %s", w.Code, w.Body)
	}
}

func TestClientAPIKeyAdministrationAndAuthSeparation(t *testing.T) {
	proxy, admin, service := clientAccessServers(t)
	request := func(method, path, body, token string, status int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		admin.server.Handler.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("%s %s status %d: %s", method, path, w.Code, w.Body)
		}
		return w
	}
	const collection = "/admin/api/client-api-keys"
	request("GET", collection, "", "", 401)
	request("POST", collection, `{"name":"Laptop","key":"client-secret"}`, "admin-secret", 201)
	request("GET", collection, "", "client-secret", 401)
	w := request("GET", collection, "", "admin-secret", 200)
	var snapshot clientaccess.Snapshot
	if err := json.Unmarshal(w.Body.Bytes(), &snapshot); err != nil || len(snapshot.Keys) != 1 || snapshot.Keys[0].Key != "client-secret" {
		t.Fatalf("snapshot %s: %v", w.Body, err)
	}
	id := snapshot.Keys[0].ID
	request("POST", collection, `{"name":"Duplicate","key":"client-secret"}`, "admin-secret", 409)
	request("PUT", collection+"/keys/"+id, `{"name":"Desktop"}`, "admin-secret", 200)
	request("PUT", collection+"/policy", `{"mode":"restricted"}`, "admin-secret", 200)
	// Admin authentication does not create a managed downstream credential.
	denied := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{"model":"test"}`))
	req.Header.Set("Authorization", "Bearer admin-secret")
	proxy.server.Handler.ServeHTTP(denied, req)
	if denied.Code != 401 {
		t.Fatalf("admin token granted downstream access: %d", denied.Code)
	}
	request("POST", collection+"/generate", `{"name":"Generated"}`, "admin-secret", 201)
	request("DELETE", collection+"/keys/"+id, "", "admin-secret", 204)
	request("DELETE", collection+"/keys/"+id, "", "admin-secret", 404)
	decision, err := service.Admit(context.Background(), req, "claude")
	if err != nil || decision.Allowed {
		t.Fatalf("unexpected admission: %+v %v", decision, err)
	}
	// Management and health remain available even when all clients are blocked.
	request("GET", "/health", "", "", 200)
	health := httptest.NewRecorder()
	proxy.server.Handler.ServeHTTP(health, httptest.NewRequest("GET", "/health", nil))
	if health.Code != 200 {
		t.Fatalf("proxy health %d", health.Code)
	}
}

type unreadClientBody struct{ reads int }

func (b *unreadClientBody) Read([]byte) (int, error) { b.reads++; return 0, io.EOF }
func (*unreadClientBody) Close() error               { return nil }

func TestClientAdmissionCoversAllProxyRouteBoundaries(t *testing.T) {
	proxy, _, service := clientAccessServers(t)
	if err := service.SetMode(context.Background(), clientaccess.ModeRestricted); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, path string
		websocket    bool
	}{
		{"POST", "/v1/messages", false},
		{"POST", "/v1/chat/completions", false},
		{"POST", "/responses", false},
		{"POST", "/v1beta/models/gemini:generateContent?key=unlisted", false},
		{"PATCH", "/claude/opaque//resource", false},
		{"POST", "/codex/./responses", false},
		{"POST", "/grok/a%2Fb", false},
		{"POST", "/gemini/opaque", false},
		{"POST", "/custom/vendor/opaque", false},
		{"GET", "/responses?model=test", true},
		{"GET", "/codex//responses?model=test", true},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			body := &unreadClientBody{}
			req := httptest.NewRequest(tc.method, tc.path, body)
			if tc.websocket {
				req.Header.Set("Connection", "Upgrade")
				req.Header.Set("Upgrade", "websocket")
			}
			w := httptest.NewRecorder()
			proxy.server.Handler.ServeHTTP(w, req)
			if w.Code != 401 || !strings.Contains(w.Body.String(), `"code":"unauthorized"`) {
				t.Fatalf("status %d: %s", w.Code, w.Body)
			}
			if body.reads != 0 {
				t.Fatalf("denial read request body %d times", body.reads)
			}
		})
	}
}
