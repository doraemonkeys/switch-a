package websocketproxy

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/model"
	"go.uber.org/zap"
)

const retryTestFrame = `{ "type":"response.create", "model":"gpt-5", "previous_response_id":"opaque-prior" }`
const retryTestCompleted = `{"type":"response.completed","response":{"id":"retry-response","model":"gpt-5","status":"completed"}}`

type retryTestWaiter func(context.Context, time.Duration) error

func (wait retryTestWaiter) Wait(ctx context.Context, delay time.Duration) error {
	return wait(ctx, delay)
}

func closeRetryTestHandshake(t *testing.T, w http.ResponseWriter) {
	t.Helper()
	conn, _, err := w.(http.Hijacker).Hijack()
	if err != nil {
		t.Error(err)
		return
	}
	_ = conn.Close()
}

func retryTestServer(t *testing.T, gateway *Gateway, cfg RequestConfig) (*httptest.Server, <-chan struct{}) {
	t.Helper()
	done := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(done)
		gateway.Handle(r.Context(), w, r, cfg, APITypeCodex, "same-provider-retry", time.Now())
	}))
	t.Cleanup(server.Close)
	return server, done
}

func awaitRetryTestSession(t *testing.T, ctx context.Context, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func TestWebSocketProviderRetryHandshakeBudgets(t *testing.T) {
	for _, tc := range []struct {
		name                            string
		retries, global, failures, want int
		success                         bool
	}{
		{"disabled", 0, 0, 1, 1, false},
		{"recovers", 4, 0, 1, 2, true},
		{"provider-exhausted", 2, 0, 5, 3, false},
		{"global-one", 4, 1, 1, 1, false},
		{"global-two", 4, 2, 3, 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if int(calls.Add(1)) <= tc.failures {
					closeRetryTestHandshake(t, w)
					return
				}
				conn, err := websocket.Accept(w, r, nil)
				if err != nil {
					return
				}
				defer conn.CloseNow()
				_, data, err := conn.Read(r.Context())
				if err != nil {
					return
				}
				if string(data) != retryTestFrame {
					t.Errorf("changed payload: %s", data)
				}
				_ = conn.Write(r.Context(), websocket.MessageText, []byte(retryTestCompleted))
				_ = conn.Close(websocket.StatusNormalClosure, "complete")
			}))
			defer upstream.Close()
			p := routingTestProvider("a")
			p.APITypes[0].BaseURL, p.MaxRetries = upstream.URL, tc.retries
			p.Backoff = model.BackoffPolicy{InitialDelay: model.Duration(time.Millisecond), Multiplier: 2}
			store := &mockStore{providers: []model.Provider{p}}
			var delays []time.Duration
			gateway := newTestGateway(t, Config{Store: store, Logger: zap.NewNop(), Backoff: retryTestWaiter(func(_ context.Context, delay time.Duration) error {
				delays = append(delays, delay)
				return nil
			})})
			server, done := retryTestServer(t, gateway, RequestConfig{GlobalAuthMode: "bearer", GlobalMaxAttempts: tc.global})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			conn, resp, err := websocket.Dial(ctx, wsURL(server)+"/responses?model=gpt-5", codexDialOptions())
			if tc.success {
				if err != nil {
					t.Fatal(err)
				}
				defer conn.CloseNow()
				if err := conn.Write(ctx, websocket.MessageText, []byte(retryTestFrame)); err != nil {
					t.Fatal(err)
				}
				_, data, err := conn.Read(ctx)
				if err != nil || string(data) != retryTestCompleted {
					t.Fatalf("response=%s err=%v", data, err)
				}
				_, _, _ = conn.Read(ctx)
			} else if err == nil || resp == nil || resp.StatusCode != http.StatusBadGateway {
				t.Fatalf("expected terminal 502, response=%v err=%v", resp, err)
			}
			awaitRetryTestSession(t, ctx, done)
			if int(calls.Load()) != tc.want || len(delays) != tc.want-1 {
				t.Fatalf("calls=%d delays=%v", calls.Load(), delays)
			}
			for i, delay := range delays {
				if delay != time.Millisecond<<i {
					t.Fatalf("backoff %d=%s", i, delay)
				}
			}
			assertRetryTestAttempts(t, store, tc.want)
		})
	}
}

func assertRetryTestAttempts(t *testing.T, store *mockStore, want int) {
	t.Helper()
	waitFor(t, func() bool { return len(store.LastAttempts(want)) == want }, time.Second)
	attempts, log := store.LastAttempts(want), store.LastLog()
	if len(attempts) != want || log == nil || log.RetryCount != want-1 {
		t.Fatalf("attempts=%d log=%+v", len(attempts), log)
	}
	for i, attempt := range attempts {
		if attempt.ProviderID != "a" || attempt.ProviderAttempt != i+1 || attempt.ProviderSwitchCount != 0 {
			t.Fatalf("attempt %d: %+v", i, attempt)
		}
	}
}

func TestWebSocketProviderRetryPreservesSettingsAndReplay(t *testing.T) {
	const created = `{"type":"response.created","response":{"id":"retry-response","model":"gpt-5"}}`
	for _, policy := range []model.ConversationRecoveryPolicy{model.ConversationRecoveryPreserveConversation, model.ConversationRecoverySwitchAccountPreserveConversation} {
		for _, sticky := range []model.StickyMode{model.StickyModeOff, model.StickyModeModel, model.StickyModeAPIType} {
			for _, probe := range []bool{false, true} {
				for _, phase := range []string{"handshake", "pre-visible-relay"} {
					t.Run(fmt.Sprintf("%s/%s/probe-%t/%s", policy, sticky, probe, phase), func(t *testing.T) {
						frame := retryTestFrame
						if probe {
							// An unowned previous_response_id is correctly rejected before
							// provider selection under preserve_conversation.
							frame = `{ "type":"response.create", "model":"gpt-5", "input":[] }`
						}
						var calls atomic.Int32
						frames := make(chan string, 2)
						upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							n := calls.Add(1)
							if r.Header.Get("Thread-Id") != "original-thread" || r.Header.Get("Cookie") != "" {
								t.Error("retry changed client headers")
							}
							if n == 1 && phase == "handshake" {
								closeRetryTestHandshake(t, w)
								return
							}
							conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"realtime.v1"}})
							if err != nil {
								return
							}
							defer conn.CloseNow()
							_, data, err := conn.Read(r.Context())
							if err != nil {
								return
							}
							frames <- string(data)
							if n == 1 {
								return
							}
							_ = conn.Write(r.Context(), websocket.MessageText, []byte(created))
							_ = conn.Write(r.Context(), websocket.MessageText, []byte(retryTestCompleted))
							_ = conn.Close(websocket.StatusNormalClosure, "complete")
						}))
						defer upstream.Close()
						p := routingTestProvider("a")
						p.APITypes[0].BaseURL, p.MaxRetries = upstream.URL, 4
						store := &mockStore{}
						if probe {
							store.routingPolicies = []model.RoutingPolicy{{Enabled: true, APIType: APITypeCodex, ModelMatchType: model.RoutingPolicyModelMatchTypePrefix, ModelMatchValue: "gpt-"}}
						}
						selection := &accountRecoverySelector{providers: []model.Provider{p}}
						gateway := newTestGateway(t, Config{Store: store, Selector: selection, Logger: zap.NewNop()})
						selection.gateway = gateway
						server, done := retryTestServer(t, gateway, RequestConfig{ConversationRecoveryPolicy: policy, GlobalAuthMode: "bearer", ProbeClientModel: probe, StickyMode: sticky, StickyTTL: time.Minute})
						ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
						defer cancel()
						options := codexDialOptions("realtime.v1")
						options.HTTPHeader.Set("Thread-Id", "original-thread")
						path := "/responses?model=gpt-5"
						if probe {
							path = "/responses"
						}
						conn, _, err := websocket.Dial(ctx, wsURL(server)+path, options)
						if err != nil {
							t.Fatal(err)
						}
						defer conn.CloseNow()
						if conn.Subprotocol() != "realtime.v1" {
							t.Fatal("subprotocol changed")
						}
						if err := conn.Write(ctx, websocket.MessageText, []byte(frame)); err != nil {
							t.Fatal(err)
						}
						if _, data, err := conn.Read(ctx); err != nil || string(data) != created {
							t.Fatalf("response=%s err=%v", data, err)
						}
						_, data, err := conn.Read(ctx)
						if err != nil || string(data) != retryTestCompleted {
							t.Fatalf("response=%s err=%v", data, err)
						}
						_, _, _ = conn.Read(ctx)
						awaitRetryTestSession(t, ctx, done)
						if calls.Load() != 2 {
							t.Fatalf("calls=%d", calls.Load())
						}
						wantFrames := 1
						if phase == "pre-visible-relay" {
							wantFrames = 2
						}
						if len(frames) != wantFrames {
							t.Fatalf("frames=%d want=%d", len(frames), wantFrames)
						}
						for range wantFrames {
							if got := <-frames; got != frame {
								t.Fatalf("replayed frame changed: %s", got)
							}
						}
						selections, stickyWrites, active := selection.snapshot()
						wantSticky := []string{"a"}
						if sticky == model.StickyModeOff {
							wantSticky = nil
						}
						if len(selections) != 1 || active != 0 || !reflect.DeepEqual(stickyWrites, wantSticky) {
							t.Fatalf("selections=%d active=%d sticky=%v", len(selections), active, stickyWrites)
						}
						assertRetryTestAttempts(t, store, 2)
					})
				}
			}
		}
	}
}

func TestWebSocketProviderRetryStopsAtSessionBoundaries(t *testing.T) {
	for _, boundary := range []string{"client-visible", "projected-handshake", "connection-bound", "replay-budget"} {
		t.Run(boundary, func(t *testing.T) {
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if boundary == "projected-handshake" {
					w.Header().Set("X-Codex-Turn-State", "opaque-upstream-state")
				}
				conn, err := websocket.Accept(w, r, nil)
				if err != nil {
					return
				}
				defer conn.CloseNow()
				conn.SetReadLimit(int64(2 * preVisibleClientReplayBufferLimitBytes))
				if _, _, err := conn.Read(r.Context()); err != nil {
					return
				}
				if boundary == "client-visible" {
					_ = conn.Write(r.Context(), websocket.MessageText, []byte(`{"type":"response.created","response":{"id":"visible","model":"gpt-5"}}`))
				}
			}))
			defer upstream.Close()
			p := routingTestProvider("a")
			p.APITypes[0].BaseURL, p.MaxRetries = upstream.URL, 4
			store := &mockStore{providers: []model.Provider{p}}
			gateway := newTestGateway(t, Config{Store: store, Logger: zap.NewNop()})
			server, done := retryTestServer(t, gateway, RequestConfig{GlobalAuthMode: "bearer"})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			conn, _, err := websocket.Dial(ctx, wsURL(server)+"/responses?model=gpt-5", codexDialOptions())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.CloseNow()
			payload := []byte(retryTestFrame)
			if boundary == "connection-bound" {
				payload = []byte(`{"type":"response.append"}`)
			}
			if boundary == "replay-budget" {
				payload = bytes.Repeat([]byte("x"), preVisibleClientReplayBufferLimitBytes+1)
			}
			if err := conn.Write(ctx, websocket.MessageText, payload); err != nil {
				t.Fatal(err)
			}
			for {
				if _, _, err := conn.Read(ctx); err != nil {
					break
				}
			}
			awaitRetryTestSession(t, ctx, done)
			if calls.Load() != 1 {
				t.Fatalf("boundary %s replayed request %d times", boundary, calls.Load())
			}
			assertRetryTestAttempts(t, store, 1)
		})
	}
}

func TestWebSocketProviderRetryRevalidatesLiveConfiguration(t *testing.T) {
	for _, change := range []string{"disabled", "deleted", "api-removed", "session-changed", "authority-changed", "budget-reduced", "credential-refresh"} {
		t.Run(change, func(t *testing.T) {
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				closeRetryTestHandshake(t, w)
			}))
			defer upstream.Close()
			p := routingTestProvider("a")
			p.APITypes[0].BaseURL, p.MaxRetries = upstream.URL, 1
			store := &mockStore{providers: []model.Provider{p}}
			gateway := newTestGateway(t, Config{Store: store, Logger: zap.NewNop(), Backoff: retryTestWaiter(func(context.Context, time.Duration) error {
				live := &store.providers[0]
				// Clone mutable slices so the leased identity remains the original snapshot.
				live.CredentialSessions = append([]credentialsession.RouteSnapshot(nil), live.CredentialSessions...)
				switch change {
				case "disabled":
					live.Enabled = false
				case "deleted":
					store.providers = nil
				case "api-removed":
					live.APITypes = nil
				case "session-changed":
					live.CredentialSessions[0].Credential.SessionID = "new-session"
				case "authority-changed":
					live.Vendor = "another-vendor"
				case "budget-reduced":
					live.MaxRetries = 0
				case "credential-refresh":
					live.CredentialSessions[0].Credential.Version++
					live.CredentialSessions[0].Credential.SecretData = "refreshed-key"
				}
				return nil
			})})
			server, done := retryTestServer(t, gateway, RequestConfig{GlobalAuthMode: "bearer"})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			conn, _, err := websocket.Dial(ctx, wsURL(server)+"/responses?model=gpt-5", codexDialOptions())
			if err == nil {
				_ = conn.CloseNow()
				t.Fatal("expected exhausted retry")
			}
			awaitRetryTestSession(t, ctx, done)
			want := 1
			if change == "credential-refresh" {
				want = 2
			}
			if int(calls.Load()) != want {
				t.Fatalf("calls=%d want=%d", calls.Load(), want)
			}
			assertRetryTestAttempts(t, store, want)
		})
	}
}

func TestWebSocketProviderRetryCancellationDoesNotDispatch(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); closeRetryTestHandshake(t, w) }))
	defer upstream.Close()
	p := routingTestProvider("a")
	p.APITypes[0].BaseURL, p.MaxRetries = upstream.URL, 4
	store := &mockStore{providers: []model.Provider{p}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	gateway := newTestGateway(t, Config{Store: store, Logger: zap.NewNop(), Backoff: retryTestWaiter(func(context.Context, time.Duration) error {
		cancel()
		return ctx.Err()
	})})
	done := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(done)
		gateway.Handle(ctx, w, r, RequestConfig{GlobalAuthMode: "bearer"}, APITypeCodex, "canceled-retry", time.Now())
	}))
	defer server.Close()
	clientCtx, clientCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer clientCancel()
	conn, _, err := websocket.Dial(clientCtx, wsURL(server)+"/responses?model=gpt-5", codexDialOptions())
	if err == nil {
		_ = conn.CloseNow()
		t.Fatal("canceled request accepted")
	}
	awaitRetryTestSession(t, clientCtx, done)
	if calls.Load() != 1 {
		t.Fatal("cancellation dispatched another attempt")
	}
	assertRetryTestAttempts(t, store, 1)
	if got := store.LastLog().TerminationReason; got == nil || *got != model.TerminationReasonClientDisconnect {
		t.Fatalf("termination=%v", got)
	}
}

func TestWebSocketProviderRetryExhaustionKeepsProviderSwitchAccounting(t *testing.T) {
	var calls atomic.Int32
	failed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		closeRetryTestHandshake(t, w)
	}))
	defer failed.Close()
	ready := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		if _, _, err := conn.Read(r.Context()); err != nil {
			return
		}
		_ = conn.Write(r.Context(), websocket.MessageText, []byte(retryTestCompleted))
		_ = conn.Close(websocket.StatusNormalClosure, "complete")
	}))
	defer ready.Close()
	a, b := routingTestProvider("a"), routingTestProvider("b")
	a.APITypes[0].BaseURL, a.MaxRetries = failed.URL, 1
	b.APITypes[0].BaseURL = ready.URL
	store := &mockStore{}
	selection := &accountRecoverySelector{providers: []model.Provider{a, b}}
	gateway := newTestGateway(t, Config{Store: store, Selector: selection, Logger: zap.NewNop()})
	selection.gateway = gateway
	server, done := retryTestServer(t, gateway, RequestConfig{GlobalAuthMode: "bearer", GlobalMaxAttempts: 3})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL(server)+"/responses?model=gpt-5", codexDialOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	if err := conn.Write(ctx, websocket.MessageText, []byte(retryTestFrame)); err != nil {
		t.Fatal(err)
	}
	if _, data, err := conn.Read(ctx); err != nil || string(data) != retryTestCompleted {
		t.Fatalf("response=%s err=%v", data, err)
	}
	_, _, _ = conn.Read(ctx)
	awaitRetryTestSession(t, ctx, done)
	waitFor(t, func() bool { return len(store.LastAttempts(3)) == 3 }, time.Second)
	attempts := store.LastAttempts(3)
	if calls.Load() != 3 || len(attempts) != 3 || store.LastLog().RetryCount != 2 {
		t.Fatalf("calls=%d attempts=%+v", calls.Load(), attempts)
	}
	for i, want := range []struct {
		provider          string
		attempt, switches int
	}{{"a", 1, 0}, {"a", 2, 0}, {"b", 1, 1}} {
		got := attempts[i]
		if got.ProviderID != want.provider || got.ProviderAttempt != want.attempt || got.ProviderSwitchCount != want.switches {
			t.Fatalf("attempt %d: %+v", i, got)
		}
	}
	selections, _, active := selection.snapshot()
	if len(selections) != 2 || active != 0 {
		t.Fatalf("selections=%d active=%d", len(selections), active)
	}
}
