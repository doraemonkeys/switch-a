package proxy

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/clientaccess"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/requestingress"
	storepkg "github.com/doraemonkeys/switch-a/internal/store"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func proxyClientAccess(t *testing.T) *clientaccess.Service {
	t.Helper()
	db, err := storepkg.NewSQLiteStore(filepath.Join(t.TempDir(), "access.db"), internal.RealClock{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return clientaccess.NewService(clientaccess.ServiceConfig{Store: db.ClientAPIKeyRepository()})
}

type admissionFunc func(context.Context, *http.Request, string) (clientaccess.Decision, error)

func (f admissionFunc) Admit(ctx context.Context, r *http.Request, api string) (clientaccess.Decision, error) {
	return f(ctx, r, api)
}

func TestClientAdmissionDiagnosticsRetainCauses(t *testing.T) {
	for _, cause := range []string{"sql: database is closed", "no such table: client_api_key_policy"} {
		core, logs := observer.New(zap.DebugLevel)
		h := &Handler{logger: zap.New(core), clientAdmission: admissionFunc(func(context.Context, *http.Request, string) (clientaccess.Decision, error) {
			return clientaccess.Decision{}, errors.New(cause)
		})}
		w := httptest.NewRecorder()
		if h.admitClient(w, httptest.NewRequest("POST", "/v1/messages", nil), "claude", "request-id") {
			t.Fatal("unavailable admission allowed")
		}
		entries := logs.All()
		if len(entries) != 1 {
			t.Fatalf("logs %+v", entries)
		}
		fields := entries[0].ContextMap()
		if fields["error"] != cause || fields["request_id"] != "request-id" || fields["api_type"] != "claude" {
			t.Fatalf("fields %+v", fields)
		}
		if strings.Contains(w.Body.String(), cause) {
			t.Fatal("storage detail escaped into client response")
		}
	}
}

func TestClientAdmissionDenialPrecedesIngressAndSelection(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
	}{{"denied", nil, 401}, {"unavailable", errors.New("policy storage unavailable"), 500}} {
		for _, ws := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: "/http", true: "/websocket"}[ws], func(t *testing.T) {
				var calls atomic.Int32
				selector := &mockSelector{selectWithMetadataFunc: func(context.Context, *model.SelectRequest) (*selectResult, error) {
					calls.Add(1)
					return nil, internal.ErrNoProvider
				}}
				handler := newProxyCodexTestHandler(t, Config{
					Store: newMockStore(), Logger: zap.NewNop(), Selector: selector,
					StartIngress: func(context.Context, *http.Request, requestingress.Options) (*requestingress.Handle, error) {
						t.Error("denied request started ingress")
						return nil, errors.New("unexpected ingress")
					},
					ClientAdmission: admissionFunc(func(_ context.Context, r *http.Request, api string) (clientaccess.Decision, error) {
						if api != APITypeCodex {
							t.Errorf("API type %s", api)
						}
						return clientaccess.Decision{Mode: clientaccess.ModeRestricted, Reason: "test_denial"}, tc.err
					}),
				})
				method := "POST"
				if ws {
					method = "GET"
				}
				req := httptest.NewRequest(method, "/responses?model=test", strings.NewReader("{}"))
				if ws {
					req.Header.Set("Connection", "Upgrade")
					req.Header.Set("Upgrade", "websocket")
				}
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, req)
				if w.Code != tc.status || calls.Load() != 0 {
					t.Fatalf("status %d selections %d: %s", w.Code, calls.Load(), w.Body)
				}
			})
		}
	}
}

func TestClientAdmissionPreservesHTTPForwarding(t *testing.T) {
	service := proxyClientAccess(t)
	if _, err := service.Create(context.Background(), "Existing", "client/key"); err != nil {
		t.Fatal(err)
	}
	type wireRequest struct {
		path, query, body string
		headers           http.Header
	}
	captured := make(chan wireRequest, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		headers := http.Header{}
		for _, name := range []string{"Authorization", "X-Api-Key", "X-Goog-Api-Key", "Accept-Encoding", "Cookie", "X-Client-Probe"} {
			headers[name] = r.Header.Values(name)
		}
		captured <- wireRequest{r.URL.EscapedPath(), r.URL.RawQuery, string(body), headers}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()
	provider := withTestStaticCredential(model.Provider{ID: "provider", Name: "Provider", Enabled: true, AuthMode: "bearer", APITypes: []model.ProviderAPIType{{ProviderID: "provider", APIType: "gemini", BaseURL: upstream.URL}}}, "", "upstream-key")
	store := newMockStore()
	store.providers = []model.Provider{provider}
	baseline := newProxyCodexTestHandler(t, Config{Store: store, Logger: zap.NewNop()})
	gated := newProxyCodexTestHandler(t, Config{Store: store, Logger: zap.NewNop(), ClientAdmission: service})
	const body = "{ \"unknown\": {\"preserve\": [1, 2]}, \"contents\": [] }\n"
	for _, tc := range []struct {
		name    string
		mode    clientaccess.Mode
		query   string
		headers http.Header
	}{
		{"permissive-missing", clientaccess.ModePermissive, "trace=a%2Fb", nil},
		{"permissive-conflicting", clientaccess.ModePermissive, "key=anything&trace=%2f", http.Header{"Authorization": {"Basic arbitrary"}, "X-Api-Key": {"first", "second"}, "X-Goog-Api-Key": {"native"}}},
		{"restricted-bearer", clientaccess.ModeRestricted, "trace=a%2Fb", http.Header{"Authorization": {"Bearer client/key"}}},
		{"restricted-api-key", clientaccess.ModeRestricted, "trace=a%2Fb", http.Header{"X-Api-Key": {"client/key"}}},
		{"restricted-equal", clientaccess.ModeRestricted, "key=client%2Fkey", http.Header{"Authorization": {"Bearer client/key"}, "X-Api-Key": {"client/key"}, "X-Goog-Api-Key": {"client/key"}}},
		{"restricted-native-header", clientaccess.ModeRestricted, "trace=a%2Fb", http.Header{"X-Goog-Api-Key": {"client/key"}}},
		{"restricted-native-query", clientaccess.ModeRestricted, "key=client%2Fkey&trace=%2f", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := service.SetMode(context.Background(), tc.mode); err != nil {
				t.Fatal(err)
			}
			run := func(h *Handler) wireRequest {
				t.Helper()
				req := httptest.NewRequest("POST", "/gemini/v1beta/models/test:generateContent?"+tc.query, strings.NewReader(body))
				for key, values := range tc.headers {
					req.Header[key] = append([]string(nil), values...)
				}
				req.Header.Set("Accept-Encoding", "identity")
				req.Header.Set("Cookie", "client=original")
				req.Header.Set("X-Client-Probe", "unchanged")
				w := httptest.NewRecorder()
				h.ServeHTTP(w, req)
				if w.Code != 200 {
					t.Fatalf("forward status %d: %s", w.Code, w.Body)
				}
				return <-captured
			}
			before, after := run(baseline), run(gated)
			if !reflect.DeepEqual(before, after) || after.body != body || after.query != tc.query {
				t.Fatalf("forwarding changed\nbefore: %+v\nafter: %+v", before, after)
			}
		})
	}
}

func TestClientAdmissionRevocationLeavesEstablishedWebSocketAlive(t *testing.T) {
	service := proxyClientAccess(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	key, err := service.Create(ctx, "WebSocket", "ws-client")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.SetMode(ctx, clientaccess.ModeRestricted); err != nil {
		t.Fatal(err)
	}
	upstream := newEchoWSServer(t)
	defer upstream.Close()
	store := newMockStore()
	store.providers = []model.Provider{withTestStaticCredential(model.Provider{
		ID: "ws-provider", Name: "WS", Enabled: true, AuthMode: "bearer",
		APITypes: []model.ProviderAPIType{{ProviderID: "ws-provider", APIType: "codex", BaseURL: upstream.URL}},
	}, "", "upstream-key")}
	handler := newProxyCodexTestHandler(t, Config{Store: store, Logger: zap.NewNop(), ClientAdmission: service})
	server := httptest.NewServer(handler)
	defer server.Close()
	options := &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer ws-client"}}}
	conn, _, err := websocket.Dial(ctx, wsURL(server)+"/responses?model=test", options)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	if err := service.Delete(ctx, key.ID); err != nil {
		t.Fatal(err)
	}
	// The request policy applies at admission; revocation cannot retrospectively
	// alter a stream whose handshake and provider ownership already committed.
	const message = "existing stream survives revocation"
	if err := conn.Write(ctx, websocket.MessageText, []byte(message)); err != nil {
		t.Fatal(err)
	}
	_, data, err := conn.Read(ctx)
	if err != nil || string(data) != message {
		t.Fatalf("echo %q: %v", data, err)
	}
	rejected, response, err := websocket.Dial(ctx, wsURL(server)+"/responses?model=test", options)
	if rejected != nil {
		rejected.CloseNow()
	}
	if err == nil || response == nil || response.StatusCode != 401 {
		t.Fatalf("subsequent handshake response %+v error %v", response, err)
	}
}
