package websocketproxy

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	codexheaders "github.com/doraemonkeys/switch-a/internal/codex/headers"
	codexhttp "github.com/doraemonkeys/switch-a/internal/codex/http"
	codexws "github.com/doraemonkeys/switch-a/internal/codex/websocket"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/upstreamtransport"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestCodexRejectedHandshakeReleasesOnlyNewOwnership(t *testing.T) {
	for _, status := range []int{
		http.StatusOK, http.StatusFound, http.StatusUnauthorized,
		http.StatusNotFound, http.StatusUpgradeRequired, http.StatusServiceUnavailable,
	} {
		for _, ownerState := range []string{"unbound", "pending", "committed"} {
			for _, carrier := range []string{"websocket", "http"} {
				t.Run(fmt.Sprintf("%d/owner=%s/%s", status, ownerState, carrier), func(t *testing.T) {
					established := ownerState != "unbound"
					config := testCodexRuntimeConfig(t)
					runtime, err := codexws.New(config)
					if err != nil {
						t.Fatal(err)
					}
					request := httptest.NewRequest(http.MethodGet, "http://gateway.test/responses", nil)
					authorizeCodexRequest(request)
					request.Header.Set("Thread-Id", "rejected-thread")
					if established {
						seed, err := runtime.Begin(t.Context(), request, APITypeCodex, "established-owner", model.ConversationRecoveryPreserveConversation)
						if err != nil {
							t.Fatal(err)
						}
						first := disclosurePreparedDial(t, "first")
						permit, err := seed.PrepareDial(t.Context(), first.headers, first.candidate, first.applied, first.finalURL)
						if err != nil {
							t.Fatal(err)
						}
						if ownerState == "committed" {
							if err := permit.Commit(t.Context()); err != nil {
								t.Fatal(err)
							}
						}
						seed.DiscardCookies()
					}
					// A new session must be released even when the thread already has an owner.
					request.Header.Set("Session-Id", "rejected-new-session")
					operation, err := runtime.Begin(t.Context(), request, APITypeCodex, "rejected-dial", model.ConversationRecoveryPreserveConversation)
					if err != nil {
						t.Fatal(err)
					}
					defer operation.DiscardCookies()
					core, logs := observer.New(zap.DebugLevel)
					orchestrator := &WebSocketSessionOrchestrator{
						codexOperation: operation, handler: &Gateway{logger: zap.New(core)}, requestID: "rejected-dial",
					}
					first := disclosurePreparedDial(t, "first")
					if err := orchestrator.prepareCodexPhysicalDial(t.Context(), &first); err != nil {
						t.Fatal(err)
					}
					settleContext, cancel := context.WithCancel(t.Context())
					cancel()
					if err := orchestrator.finishCodexPhysicalDial(settleContext, first, DialExchange{
						Disclosure:          upstreamtransport.RequestDisclosureConfirmed,
						HandshakeStatusCode: status, Err: errors.New("upstream refused the upgrade"),
					}); err != nil {
						t.Fatal(err)
					}
					entries := logs.FilterMessage("websocket.dial_ownership_settled").All()
					if len(entries) != 1 || entries[0].ContextMap()["decision"] != "abandon_rejected_handshake" ||
						entries[0].ContextMap()["request_disclosure"] != "confirmed" {
						t.Fatalf("rejected handshake settlement = %+v", entries)
					}
					httpRuntime, err := codexhttp.New(codexhttp.Config{
						ClientIdentities: config.ClientIdentities, Continuity: config.Continuity,
						ProviderCookies: config.ProviderCookies, ExternalScheme: config.ExternalScheme,
					})
					if err != nil {
						t.Fatal(err)
					}
					sessionRequest := request.Clone(t.Context())
					sessionRequest.Header.Del("Thread-Id")
					sessionOperation, err := httpRuntime.Begin(t.Context(), sessionRequest, APITypeCodex, "session-fallback",
						model.ConversationRecoveryPreserveConversation, codexheaders.ClientEvidence{})
					if err != nil {
						t.Fatal(err)
					}
					defer sessionOperation.Discard()
					if authority, _ := sessionOperation.RequiredAuthority(); authority != nil {
						t.Fatal("rejected handshake left a new session owner in shared persistence")
					}

					second := disclosurePreparedDial(t, "second")
					switch carrier {
					case "websocket":
						if err := operation.ReplacePhysicalAttempt(); err != nil {
							t.Fatal(err)
						}
						err = orchestrator.prepareCodexPhysicalDial(t.Context(), &second)
						if established {
							if codexws.Classify(err) != codexws.FailureIdentity {
								t.Fatalf("established WebSocket owner was lost: %v", err)
							}
						} else {
							if err != nil {
								t.Fatalf("new conversation cannot replace rejected handshake: %v", err)
							}
							if err := second.boundaryPermit.Commit(t.Context()); err != nil {
								t.Fatal(err)
							}
						}
					case "http":
						request.Method = http.MethodPost
						fallback, err := httpRuntime.Begin(t.Context(), request, APITypeCodex, "http-fallback",
							model.ConversationRecoveryPreserveConversation, codexheaders.ClientEvidence{})
						if err != nil {
							t.Fatal(err)
						}
						defer fallback.Discard()
						upstreamRequest := request.Clone(t.Context())
						upstreamRequest.URL = second.finalURL
						attempt, err := fallback.PrepareAttempt(t.Context(), upstreamRequest, second.candidate, second.applied)
						if established {
							if !codexhttp.IsKind(err, codexhttp.ErrorIdentityMismatch) {
								t.Fatalf("established HTTP owner was lost: %v", err)
							}
						} else {
							if err != nil {
								t.Fatalf("rejected WebSocket handshake pinned HTTP fallback: %v", err)
							}
							if err := attempt.MarkDisclosed(t.Context()); err != nil {
								t.Fatal(err)
							}
						}
					}
				})
			}
		}
	}
}

func TestGatewayReplacesRejectedHandshakeUnderPreserveConversation(t *testing.T) {
	const (
		threadID   = "new-thread-handshake-replacement"
		sessionID  = "new-session-handshake-replacement"
		clientData = `{"type":"response.create","model":"gpt-5","input":[]}`
		serverData = `{"type":"response.created","provider":"fallback"}`
	)
	var primaryDials, fallbackDials atomic.Int32
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryDials.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer primary.Close()
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fallbackDials.Add(1)
		if r.Header.Get("Thread-Id") != threadID || r.Header.Get("Session-Id") != sessionID {
			t.Error("replacement changed conversation identifiers")
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept fallback: %v", err)
			return
		}
		defer conn.CloseNow()
		messageType, payload, err := conn.Read(r.Context())
		if err != nil || messageType != websocket.MessageText || string(payload) != clientData {
			t.Errorf("fallback request = %s, error = %v", payload, err)
			return
		}
		if err := conn.Write(r.Context(), websocket.MessageText, []byte(serverData)); err != nil {
			t.Errorf("write fallback response: %v", err)
			return
		}
		_ = conn.Close(websocket.StatusNormalClosure, "complete")
	}))
	defer fallback.Close()
	store := newMockStore()
	store.providers = []model.Provider{
		{ID: "primary", AuthMode: "bearer", Enabled: true,
			APITypes:           []model.ProviderAPIType{{ProviderID: "primary", APIType: APITypeCodex, BaseURL: primary.URL, Transport: "websocket"}},
			CredentialSessions: testCredentialSessions("primary", APITypeCodex, credentialsession.KindAPIKey, "primary-key")},
		{ID: "fallback", AuthMode: "bearer", Enabled: true,
			APITypes:           []model.ProviderAPIType{{ProviderID: "fallback", APIType: APITypeCodex, BaseURL: fallback.URL, Transport: "websocket"}},
			CredentialSessions: testCredentialSessions("fallback", APITypeCodex, credentialsession.KindAPIKey, "fallback-key")},
	}
	gateway := newTestGateway(t, Config{Store: store, Logger: zap.NewNop()})
	server := newGatewayIntegrationServer(gateway, RequestConfig{
		GlobalAuthMode: "bearer", GlobalMaxAttempts: 2,
		ConversationRecoveryPolicy: model.ConversationRecoveryPreserveConversation,
	}, "rejected-handshake-replacement")
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	options := codexDialOptions()
	options.HTTPHeader.Set("Thread-Id", threadID)
	options.HTTPHeader.Set("Session-Id", sessionID)
	conn, _, err := websocket.Dial(ctx, wsURL(server)+"/responses?model=gpt-5", options)
	if err != nil {
		t.Fatalf("gateway failed to replace a rejected first handshake: %v", err)
	}
	defer conn.CloseNow()
	if err := conn.Write(ctx, websocket.MessageText, []byte(clientData)); err != nil {
		t.Fatal(err)
	}
	_, payload, err := conn.Read(ctx)
	if err != nil || string(payload) != serverData {
		t.Fatalf("client response = %s, error = %v", payload, err)
	}
	_, _, _ = conn.Read(ctx)
	waitFor(t, func() bool { return len(store.LastAttempts(2)) == 2 }, testPollTimeout)
	attempts := store.LastAttempts(2)
	if primaryDials.Load() != 1 || fallbackDials.Load() != 1 ||
		attempts[0].ProviderID != "primary" || attempts[1].ProviderID != "fallback" {
		t.Fatalf("dial counts = %d/%d, attempts = %+v", primaryDials.Load(), fallbackDials.Load(), attempts)
	}
	if attempts[0].ResultVisibleToClient == nil || *attempts[0].ResultVisibleToClient {
		t.Fatal("failed handshake became client-visible before replacement")
	}
}
