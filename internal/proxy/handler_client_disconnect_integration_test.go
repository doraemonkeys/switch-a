package proxy

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"

	"go.uber.org/zap"
)

const clientDisconnectIntegrationTimeout = 5 * time.Second

const delayedClientDisconnectAfterUpstreamEOF = 250 * time.Millisecond

func TestHandler_RealSSEClientDisconnectPersistsClientAttribution(t *testing.T) {
	tests := []struct {
		name        string
		enableHTTP2 bool
		protoMajor  int
	}{
		{name: "HTTP/1.1 connection close", protoMajor: 1},
		{name: "HTTP/2 stream reset", enableHTTP2: true, protoMajor: 2},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			upstreamCanceled := make(chan struct{})
			releaseUpstream := make(chan struct{})
			var cancelObserved sync.Once
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				if _, err := io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n"); err != nil {
					t.Errorf("write upstream SSE event: %v", err)
					return
				}
				w.(http.Flusher).Flush()

				select {
				case <-r.Context().Done():
					cancelObserved.Do(func() { close(upstreamCanceled) })
				case <-releaseUpstream:
				}
			}))
			defer func() {
				close(releaseUpstream)
				upstream.Close()
			}()

			store := newMockStore()
			store.configs[ConfigKeyGlobalMaxAttempts] = "1"
			store.providers = []model.Provider{{
				ID: "sse-disconnect-provider", Name: "SSE Disconnect Provider", APIKey: "test-key",
				AuthMode: "bearer", Enabled: true,
				APITypes: []model.ProviderAPIType{{
					ProviderID: "sse-disconnect-provider", APIType: APITypeCodex, BaseURL: upstream.URL,
				}},
			}}
			health := newTrackingHealthManager()
			handler := NewHandler(Config{Store: store, Health: health, Logger: zap.NewNop()})

			proxyServer := httptest.NewUnstartedServer(handler)
			proxyServer.EnableHTTP2 = test.enableHTTP2
			if test.enableHTTP2 {
				proxyServer.StartTLS()
			} else {
				proxyServer.Start()
			}
			defer proxyServer.Close()

			client := proxyServer.Client()
			transport := client.Transport.(*http.Transport).Clone()
			transport.DisableKeepAlives = true
			client.Transport = transport
			defer transport.CloseIdleConnections()

			requestCtx, cancelRequest := context.WithTimeout(t.Context(), clientDisconnectIntegrationTimeout)
			defer cancelRequest()
			request, err := http.NewRequestWithContext(
				requestCtx,
				http.MethodPost,
				proxyServer.URL+RouteCodexResponses,
				strings.NewReader(`{"model":"gpt-5","stream":true}`),
			)
			if err != nil {
				t.Fatalf("NewRequestWithContext() error = %v", err)
			}
			request.Header.Set("Content-Type", "application/json")

			response, err := client.Do(request)
			if err != nil {
				t.Fatalf("client.Do() error = %v", err)
			}
			if response.ProtoMajor != test.protoMajor {
				t.Fatalf("response protocol = %s, want HTTP/%d", response.Proto, test.protoMajor)
			}
			line, err := bufio.NewReader(response.Body).ReadString('\n')
			if err != nil {
				t.Fatalf("read first SSE event: %v", err)
			}
			if !strings.HasPrefix(line, "data: ") {
				t.Fatalf("first SSE line = %q, want data event", line)
			}
			if err := response.Body.Close(); err != nil {
				t.Fatalf("close client response body: %v", err)
			}

			select {
			case <-upstreamCanceled:
			case <-time.After(clientDisconnectIntegrationTimeout):
				t.Fatal("upstream request context was not canceled after the client disconnected")
			}
			waitFor(t, func() bool { return store.LogsLen() == 1 }, clientDisconnectIntegrationTimeout)

			log := store.LastLog()
			if reason := requestLogTerminationReason(log); reason != model.TerminationReasonClientDisconnect {
				t.Fatalf("TerminationReason = %q, want %q", reason, model.TerminationReasonClientDisconnect)
			}
			if log.TerminationActor == nil || *log.TerminationActor != model.TerminationActorClient {
				t.Fatalf("TerminationActor = %v, want %q", log.TerminationActor, model.TerminationActorClient)
			}
			if outcome := requestLogServiceOutcome(log); outcome != model.ServiceOutcomeAbandonedByClient {
				t.Fatalf("ServiceOutcome = %q, want %q", outcome, model.ServiceOutcomeAbandonedByClient)
			}
			if completion := requestLogCompletionState(log); completion != model.CompletionStateIncomplete {
				t.Fatalf("CompletionState = %q, want %q", completion, model.CompletionStateIncomplete)
			}
			if failures := health.getMarkFailureCalls(); len(failures) != 0 {
				t.Fatalf("MarkFailure calls = %d, want health-neutral client termination", len(failures))
			}
			if successes := health.getMarkSuccessIDs(); len(successes) != 0 {
				t.Fatalf("MarkSuccess calls = %d, want health-neutral incomplete exchange", len(successes))
			}
		})
	}
}

func TestHandler_UpstreamFirstByteTimeoutIsNotClientDisconnect(t *testing.T) {
	upstreamStarted := make(chan struct{})
	releaseUpstream := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(upstreamStarted)
		select {
		case <-r.Context().Done():
		case <-releaseUpstream:
		}
	}))
	defer func() {
		close(releaseUpstream)
		upstream.Close()
	}()

	store := newMockStore()
	store.configs[ConfigKeyGlobalMaxAttempts] = "1"
	store.configs[ConfigKeyFirstByteTimeout] = "1"
	store.providers = []model.Provider{{
		ID: "slow-first-byte-provider", Name: "Slow First Byte Provider", APIKey: "test-key",
		AuthMode: "bearer", Enabled: true,
		APITypes: []model.ProviderAPIType{{
			ProviderID: "slow-first-byte-provider", APIType: APITypeClaude, BaseURL: upstream.URL,
		}},
	}}
	health := newTrackingHealthManager()
	handler := NewHandler(Config{Store: store, Health: health, Logger: zap.NewNop()})
	proxyServer := httptest.NewServer(handler)
	defer proxyServer.Close()

	requestCtx, cancelRequest := context.WithTimeout(t.Context(), clientDisconnectIntegrationTimeout)
	defer cancelRequest()
	request, err := http.NewRequestWithContext(
		requestCtx,
		http.MethodPost,
		proxyServer.URL+RouteClaudeMessages,
		strings.NewReader(`{"model":"claude-test"}`),
	)
	if err != nil {
		t.Fatalf("NewRequestWithContext() error = %v", err)
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := proxyServer.Client().Do(request)
	if err != nil {
		t.Fatalf("client.Do() error = %v", err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	if err := response.Body.Close(); err != nil {
		t.Fatalf("close response body: %v", err)
	}
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusServiceUnavailable)
	}

	select {
	case <-upstreamStarted:
	default:
		t.Fatal("upstream was not contacted")
	}
	waitFor(t, func() bool { return store.LogsLen() == 1 }, clientDisconnectIntegrationTimeout)

	log := store.LastLog()
	if reason := requestLogTerminationReason(log); reason == model.TerminationReasonClientDisconnect {
		t.Fatalf("TerminationReason = %q, upstream timeout must not be attributed to the client", reason)
	}
	if log.TerminationActor != nil && *log.TerminationActor == model.TerminationActorClient {
		t.Fatalf("TerminationActor = %q, upstream timeout must not be attributed to the client", *log.TerminationActor)
	}
	if failures := health.getMarkFailureCalls(); len(failures) != 1 {
		t.Fatalf("MarkFailure calls = %d, want one upstream timeout failure", len(failures))
	}
}

func TestHandler_UpstreamEOFBeforeDelayedClientDisconnectIsNormalCompletion(t *testing.T) {
	upstreamReturned := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			// Flushing before the body forces a streaming response, so the proxy
			// observes the upstream connection's EOF instead of a buffered write.
			flusher.Flush()
		}
		_, _ = io.WriteString(w, `{"ok":true}`)
		close(upstreamReturned)
	}))
	defer upstream.Close()

	store := newMockStore()
	store.configs[ConfigKeyGlobalMaxAttempts] = "1"
	store.providers = []model.Provider{{
		ID: "upstream-eof-provider", Name: "Upstream EOF Provider", APIKey: "test-key",
		AuthMode: "bearer", Enabled: true,
		APITypes: []model.ProviderAPIType{{
			ProviderID: "upstream-eof-provider", APIType: APITypeClaude, BaseURL: upstream.URL,
		}},
	}}
	handler := NewHandler(Config{Store: store, Logger: zap.NewNop()})
	proxyServer := httptest.NewServer(handler)
	defer proxyServer.Close()

	requestCtx, cancelRequest := context.WithCancel(t.Context())
	defer cancelRequest()
	request, err := http.NewRequestWithContext(
		requestCtx,
		http.MethodPost,
		proxyServer.URL+RouteClaudeMessages,
		strings.NewReader(`{"model":"claude-test"}`),
	)
	if err != nil {
		t.Fatalf("NewRequestWithContext() error = %v", err)
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := proxyServer.Client().Do(request)
	if err != nil {
		t.Fatalf("client.Do() error = %v", err)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read response to upstream EOF: %v", err)
	}
	if string(body) != `{"ok":true}` {
		t.Fatalf("response body = %q, want upstream body", body)
	}
	select {
	case <-upstreamReturned:
	case <-time.After(clientDisconnectIntegrationTimeout):
		t.Fatal("upstream did not return after writing the response")
	}
	waitFor(t, func() bool { return store.LogsLen() == 1 }, clientDisconnectIntegrationTimeout)
	// The upstream has already reached EOF. A later client close must not
	// overwrite the completed exchange with a client-disconnect attribution.
	time.Sleep(delayedClientDisconnectAfterUpstreamEOF)
	cancelRequest()
	if err := response.Body.Close(); err != nil {
		t.Fatalf("close client response body: %v", err)
	}

	log := store.LastLog()
	if reason := requestLogTerminationReason(log); reason == model.TerminationReasonClientDisconnect {
		t.Fatalf("TerminationReason = %q, upstream EOF followed by delayed client close must remain normal", reason)
	}
	if outcome := requestLogServiceOutcome(log); outcome != model.ServiceOutcomeCompleted {
		t.Fatalf("ServiceOutcome = %q, want %q", outcome, model.ServiceOutcomeCompleted)
	}
	if completion := requestLogCompletionState(log); completion != model.CompletionStateCompleted {
		t.Fatalf("CompletionState = %q, want %q", completion, model.CompletionStateCompleted)
	}
}
