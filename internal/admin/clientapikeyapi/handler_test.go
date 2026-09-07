package clientapikeyapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/clientaccess"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

type stubManager struct {
	snapshot        clientaccess.Snapshot
	key             clientaccess.Key
	err             error
	snapshotErr     error
	calls           int
	name, value, id string
	mode            clientaccess.Mode
}

func (m *stubManager) Snapshot(context.Context) (clientaccess.Snapshot, error) {
	return m.snapshot, m.snapshotErr
}
func (m *stubManager) Create(_ context.Context, n, k string) (clientaccess.Key, error) {
	m.calls++
	m.name, m.value = n, k
	return m.key, m.err
}
func (m *stubManager) Generate(_ context.Context, n string) (clientaccess.Key, error) {
	m.calls++
	m.name = n
	return m.key, m.err
}
func (m *stubManager) Rename(_ context.Context, id, n string) (clientaccess.Key, error) {
	m.calls++
	m.id, m.name = id, n
	return m.key, m.err
}
func (m *stubManager) Delete(_ context.Context, id string) error { m.calls++; m.id = id; return m.err }
func (m *stubManager) SetMode(_ context.Context, mode clientaccess.Mode) error {
	m.calls++
	m.mode = mode
	return m.err
}

func TestAdministrationFailureDiagnosticsRetainCauses(t *testing.T) {
	for _, cause := range []string{"sql: database is closed", "no such table: client_api_keys"} {
		core, logs := observer.New(zap.DebugLevel)
		h := NewHandler(&stubManager{snapshotErr: errors.New(cause)}, zap.New(core))
		w := httptest.NewRecorder()
		h.List(w, httptest.NewRequest(http.MethodGet, "/admin/api/client-api-keys", nil))
		entries := logs.All()
		if len(entries) != 1 {
			t.Fatalf("logs %+v", entries)
		}
		fields := entries[0].ContextMap()
		if fields["error"] != cause || fields["operation_id"] == "" || fields["error_code"] != "internal_error" {
			t.Fatalf("fields %+v", fields)
		}
		if strings.Contains(w.Body.String(), cause) {
			t.Fatal("storage detail escaped into client response")
		}
	}
}

func TestHandlers(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		route      func(*Handler) http.HandlerFunc
	}{
		{"list", "", 200, func(h *Handler) http.HandlerFunc { return h.List }},
		{"policy", `{"mode":"restricted"}`, 200, func(h *Handler) http.HandlerFunc { return h.SetPolicy }},
		{"create", `{"name":"Laptop","key":"existing-key"}`, 201, func(h *Handler) http.HandlerFunc { return h.Create }},
		{"generate", `{"name":"Laptop"}`, 201, func(h *Handler) http.HandlerFunc { return h.Generate }},
		{"rename", `{"name":"Laptop"}`, 200, func(h *Handler) http.HandlerFunc { return h.Rename }},
		{"delete", "", 204, func(h *Handler) http.HandlerFunc { return h.Delete }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := &stubManager{snapshot: clientaccess.Snapshot{Mode: clientaccess.ModePermissive}, key: clientaccess.Key{ID: "key-id", Name: "Laptop", Key: "existing-key"}}
			h := NewHandler(manager, nil)
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body))
			req.SetPathValue("id", "key-id")
			w := httptest.NewRecorder()
			tc.route(h)(w, req)
			if w.Code != tc.status {
				t.Fatalf("status %d: %s", w.Code, w.Body)
			}
			switch tc.name {
			case "list":
				if !strings.Contains(w.Body.String(), `"keys":[]`) {
					t.Fatal(w.Body.String())
				}
			case "policy":
				if manager.mode != clientaccess.ModeRestricted || !strings.Contains(w.Body.String(), `"mode":"restricted"`) {
					t.Fatal(w.Body.String())
				}
			case "create":
				if manager.value != "existing-key" || manager.name != "Laptop" {
					t.Fatal("create arguments")
				}
			case "generate":
				if manager.name != "Laptop" {
					t.Fatal("generate arguments")
				}
			case "rename":
				if manager.id != "key-id" || manager.name != "Laptop" {
					t.Fatal("rename arguments")
				}
			case "delete":
				if manager.id != "key-id" || w.Body.Len() != 0 {
					t.Fatal("delete response")
				}
			}
		})
	}
}

func TestMutationErrorsAndMalformedDocuments(t *testing.T) {
	routes := []struct {
		name  string
		route func(*Handler) http.HandlerFunc
	}{
		{"policy", func(h *Handler) http.HandlerFunc { return h.SetPolicy }},
		{"create", func(h *Handler) http.HandlerFunc { return h.Create }},
		{"generate", func(h *Handler) http.HandlerFunc { return h.Generate }},
		{"rename", func(h *Handler) http.HandlerFunc { return h.Rename }},
		{"delete", func(h *Handler) http.HandlerFunc { return h.Delete }},
	}
	for _, route := range routes {
		for _, tc := range []struct {
			name   string
			err    error
			status int
			code   string
		}{
			{"validation", clientaccess.ErrValidation, 400, "validation_error"},
			{"duplicate", clientaccess.ErrDuplicate, 409, "duplicate_key"},
			{"missing", clientaccess.ErrNotFound, 404, "not_found"},
			{"storage", errors.New("private SQL credential"), 500, "internal_error"},
		} {
			t.Run(route.name+"/"+tc.name, func(t *testing.T) {
				m := &stubManager{err: tc.err}
				w := httptest.NewRecorder()
				route.route(NewHandler(m, nil))(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{}")))
				if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.code) || strings.Contains(w.Body.String(), "private SQL") {
					t.Fatalf("response %d %s", w.Code, w.Body)
				}
			})
		}
		if route.name == "delete" {
			continue
		}
		for _, body := range []string{"", "{", "{} {}", "{} trailing"} {
			t.Run(route.name+"/malformed/"+body, func(t *testing.T) {
				m := &stubManager{}
				w := httptest.NewRecorder()
				route.route(NewHandler(m, nil))(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)))
				if w.Code != 400 || m.calls != 0 {
					t.Fatalf("status %d calls %d", w.Code, m.calls)
				}
			})
		}
	}
	for _, route := range []func(*Handler) http.HandlerFunc{func(h *Handler) http.HandlerFunc { return h.List }, func(h *Handler) http.HandlerFunc { return h.SetPolicy }} {
		m := &stubManager{snapshotErr: errors.New("unavailable")}
		w := httptest.NewRecorder()
		route(NewHandler(m, nil))(w, httptest.NewRequest(http.MethodPut, "/", strings.NewReader("{}")))
		if w.Code != 500 || m.calls != 0 {
			t.Fatalf("status %d calls %d", w.Code, m.calls)
		}
	}
}
