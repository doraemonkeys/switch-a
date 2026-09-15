package proxy

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/selector"
	"go.uber.org/zap"
)

func TestCodexWebSocketNewThreadReplacesUndisclosedAccount(t *testing.T) {
	t.Run("connection refused", func(t *testing.T) {
		testCodexWebSocketDialReplacement(t, refusedWebSocketURL(t), true)
	})
	t.Run("TLS handshake failure", func(t *testing.T) {
		plain := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Error("TLS failure reached the application handler")
		}))
		defer plain.Close()
		testCodexWebSocketDialReplacement(t, strings.Replace(plain.URL, "http://", "https://", 1), true)
	})
}

func TestCodexWebSocketDisclosedNewThreadRetainsAccount(t *testing.T) {
	t.Run("handshake rejected", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer upstream.Close()
		testCodexWebSocketDialReplacement(t, upstream.URL, false)
	})
	t.Run("connection lost after request", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			conn.Close()
		}))
		defer upstream.Close()
		testCodexWebSocketDialReplacement(t, upstream.URL, false)
	})
}

func refusedWebSocketURL(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	refusedURL := "http://" + listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return refusedURL
}

func testCodexWebSocketDialReplacement(t *testing.T, failedURL string, wantReplacement bool) {
	t.Helper()
	const (
		threadID    = "new-thread-before-dial"
		clientFrame = `{"type":"response.create","model":"gpt-5","input":"hello"}`
		completed   = `{"type":"response.completed","response":{"id":"response-b","status":"completed","model":"gpt-5"}}`
	)
	var replacementCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		replacementCalls.Add(1)
		if r.Header.Get("Thread-Id") != threadID || r.Header.Get("Chatgpt-Account-Id") != "account-b" {
			t.Errorf("replacement identity: thread=%q account=%q", r.Header.Get("Thread-Id"), r.Header.Get("Chatgpt-Account-Id"))
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.CloseNow()
		_, payload, err := conn.Read(r.Context())
		if err != nil || string(payload) != clientFrame {
			t.Errorf("replacement request=%q err=%v", payload, err)
			return
		}
		if err := conn.Write(r.Context(), websocket.MessageText, []byte(completed)); err != nil {
			t.Error(err)
		}
		_ = conn.Close(websocket.StatusNormalClosure, "done")
	}))
	defer upstream.Close()
	providers := []model.Provider{
		withTestChatGPTCredential(model.Provider{
			ID: "provider-a", Name: "Account A", Enabled: true,
			APITypes: []model.ProviderAPIType{{ProviderID: "provider-a", APIType: "codex", BaseURL: failedURL}},
		}, "codex", testChatGPTCredentialData(t, "access-a", "refresh-a", "account-a")),
		withTestChatGPTCredential(model.Provider{
			ID: "provider-b", Name: "Account B", Enabled: true, Priority: 100,
			APITypes: []model.ProviderAPIType{{ProviderID: "provider-b", APIType: "codex", BaseURL: upstream.URL}},
		}, "codex", testChatGPTCredentialData(t, "access-b", "refresh-b", "account-b")),
	}
	// Real selection must enforce account ownership; a scripted alternate could
	// conceal a stale claim left behind by the first dial.
	concrete := selector.NewSelector(selector.Config{
		Store: &x3AdapterStore{providers: providers}, Logger: zap.NewNop(),
	})
	store := newMockStore()
	store.providers = providers
	store.configs[ConfigKeyGlobalMaxAttempts] = "2"
	handler := newProxyCodexTestHandler(t, Config{Store: store, Selector: concrete, Logger: zap.NewNop()})
	done := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(done)
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	options := proxyCodexDialOptions()
	options.HTTPHeader.Set("Thread-Id", threadID)
	conn, response, err := websocket.Dial(ctx, wsURL(server)+"/responses?model=gpt-5", options)
	if !wantReplacement {
		if err == nil {
			conn.CloseNow()
			t.Fatal("disclosed thread unexpectedly crossed accounts")
		}
		if response == nil || response.StatusCode < http.StatusBadRequest || replacementCalls.Load() != 0 {
			t.Fatalf("failed handshake=%v replacement calls=%d", response, replacementCalls.Load())
		}
		return
	}
	if err != nil {
		t.Fatalf("new thread did not replace failed account: response=%v err=%v", response, err)
	}
	defer conn.CloseNow()
	if err := conn.Write(ctx, websocket.MessageText, []byte(clientFrame)); err != nil {
		t.Fatal(err)
	}
	_, payload, err := conn.Read(ctx)
	if err != nil || string(payload) != completed {
		t.Fatalf("response=%q err=%v", payload, err)
	}
	_ = conn.Close(websocket.StatusNormalClosure, "done")
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	waitFor(t, func() bool { return store.AttemptsLen() == 2 }, time.Second)
	attempts := store.LastAttempts(2)
	if len(attempts) != 2 || attempts[0].ProviderID != "provider-a" || attempts[1].ProviderID != "provider-b" ||
		attempts[1].SwitchMode != model.RequestAttemptSwitchModeReplacement {
		t.Fatalf("attempts=%+v, want account A followed by replacement account B", attempts)
	}
}
