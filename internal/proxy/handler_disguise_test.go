package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/clientdisguise"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/upstreamtransport"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type httpDisguiseRepository struct {
	mu             sync.Mutex
	commits        []string
	mappings       map[clientdisguise.MappingKey]string
	mapFailure     error
	restoreFailure error
	commitFailure  error
	excludeCommit  string
}

func (r *httpDisguiseRepository) EvaluateCandidate(_ context.Context, id string, basis clientdisguise.AccountBasis, policy clientdisguise.Policy, facts clientdisguise.PlatformFacts) (clientdisguise.Candidate, error) {
	return clientdisguise.Candidate{CredentialSessionID: id, AccountBasis: basis, Policy: policy, Facts: facts, Profile: clientdisguise.ProfileRevision{ID: "revision-one", ClientVersion: "1.2.3", Features: clientdisguise.Features{UserAgent: "frozen-agent"}}, Decision: clientdisguise.PlatformDecision{Allowed: true}}, nil
}
func (r *httpDisguiseRepository) CommitTarget(_ context.Context, c clientdisguise.Candidate) (clientdisguise.TargetSnapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commits = append(r.commits, c.CredentialSessionID)
	if r.commitFailure != nil {
		return clientdisguise.TargetSnapshot{}, r.commitFailure
	}
	if c.CredentialSessionID == r.excludeCommit {
		return clientdisguise.TargetSnapshot{}, clientdisguise.ErrCandidateExcluded
	}
	return clientdisguise.TargetSnapshot{Policy: c.Policy, Profile: c.Profile, Login: clientdisguise.LoginIdentity{CredentialSessionID: c.CredentialSessionID, GenerationID: c.CredentialSessionID, DeviceID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"}}, nil
}
func (r *httpDisguiseRepository) MapIdentity(_ context.Context, key clientdisguise.MappingKey) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.mapFailure != nil {
		return "", r.mapFailure
	}
	if r.mappings == nil {
		r.mappings = make(map[clientdisguise.MappingKey]string)
	}
	if r.mappings[key] == "" {
		r.mappings[key] = uuid.NewSHA1(uuid.NameSpaceOID, []byte(key.GenerationID+key.Namespace+key.Original)).String()
	}
	return r.mappings[key], nil
}
func (r *httpDisguiseRepository) RestoreIdentity(_ context.Context, generation, client, namespace, mapped string) (string, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.restoreFailure != nil {
		return "", false, r.restoreFailure
	}
	for key, value := range r.mappings {
		if key.GenerationID == generation && key.ClientIdentityID == client && key.Namespace == namespace && value == mapped {
			return key.Original, true, nil
		}
	}
	return mapped, false, nil
}

func disguiseHandler(t *testing.T, upstreamURL string, repo *httpDisguiseRepository, transport HTTPTransport) (*Handler, *mockStore) {
	t.Helper()
	store := newMockStore()
	store.configs[ConfigKeyStickyMode] = "off"
	store.configs[ConfigKeyGlobalMaxAttempts] = "3"
	store.providers = []model.Provider{withTestStaticCredential(model.Provider{ID: "disguise-provider", Enabled: true, AuthMode: "bearer", MaxRetries: 2, ClientDisguise: clientdisguise.Policy{Enabled: true}, APITypes: []model.ProviderAPIType{{ProviderID: "disguise-provider", APIType: APITypeCodex, BaseURL: upstreamURL}}}, APITypeCodex, "upstream-key")}
	return newProxyCodexTestHandler(t, Config{Store: store, Transport: transport, ClientDisguise: repo, Logger: zap.NewNop()}), store
}

func TestHTTPDisguiseNonConversationSendsBeforeEOF(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled", true: "enabled"}[enabled], func(t *testing.T) {
			started := make(chan struct{})
			received := make(chan string, 1)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				prefix := make([]byte, 128)
				if _, err := io.ReadFull(r.Body, prefix); err != nil {
					t.Error(err)
					return
				}
				close(started)
				tail, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					return
				}
				received <- string(prefix) + string(tail)
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"ok":true}`)
			}))
			defer upstream.Close()
			h, store := disguiseHandler(t, upstream.URL, &httpDisguiseRepository{}, nil)
			store.providers[0].ClientDisguise.Enabled = enabled
			reader, writer := io.Pipe()
			request := httptest.NewRequest("POST", "/codex/alpha/search", reader)
			request.Header.Set("Authorization", proxyCodexTestAuthorization)
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			done := make(chan struct{})
			go func() { defer close(done); h.ServeHTTP(response, request) }()
			prefix := `{"query":"` + strings.Repeat("x", 65536)
			if _, err := io.WriteString(writer, prefix); err != nil {
				t.Fatal(err)
			}
			awaitIngressTest(t, started)
			_, _ = io.WriteString(writer, `"}`)
			_ = writer.Close()
			awaitIngressTest(t, done)
			if response.Code != 200 {
				t.Fatalf("response=%d %s", response.Code, response.Body.String())
			}
			if actual := <-received; actual != prefix+`"}` {
				t.Fatal("business payload changed")
			}
		})
	}
}

func TestHTTPDisguiseLateInvalidTailTerminatesWithoutRetry(t *testing.T) {
	started := make(chan struct{})
	var attempts atomic.Int32
	transport := disguiseTransportFunc(func(ctx context.Context, r *http.Request, _ upstreamtransport.ExecutionOptions) (*upstreamtransport.Response, upstreamtransport.RequestDisclosure, error) {
		attempts.Add(1)
		prefix := make([]byte, 128)
		_, err := io.ReadFull(r.Body, prefix)
		if err == nil {
			close(started)
			_, err = io.Copy(io.Discard, r.Body)
		}
		_ = r.Body.Close()
		return nil, upstreamtransport.RequestDisclosureConfirmed, err
	})
	h, store := disguiseHandler(t, "https://example.test", &httpDisguiseRepository{}, transport)
	reader, writer := io.Pipe()
	request := httptest.NewRequest("POST", "/codex/alpha/search", reader)
	request.Header.Set("Authorization", proxyCodexTestAuthorization)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { defer close(done); h.ServeHTTP(response, request) }()
	_, _ = io.WriteString(writer, `{"query":"`+strings.Repeat("x", 65536))
	awaitIngressTest(t, started)
	_, _ = io.WriteString(writer, `"} invalid`)
	_ = writer.Close()
	awaitIngressTest(t, done)
	if attempts.Load() != 1 || response.Code != 500 || response.Header().Get("X-Switch-A-Diagnostic-Id") == "" {
		t.Fatalf("attempts=%d status=%d body=%s", attempts.Load(), response.Code, response.Body.String())
	}
	waitFor(t, func() bool { store.mu.Lock(); defer store.mu.Unlock(); return len(store.logs) == 1 }, time.Second)
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.attempts) != 1 || store.attempts[0].AttemptEvidenceJSON == nil || !strings.Contains(*store.attempts[0].AttemptEvidenceJSON, `"decision":"failed"`) {
		t.Fatalf("missing conversion evidence: %+v", store.attempts)
	}
}

type disguiseTransportFunc func(context.Context, *http.Request, upstreamtransport.ExecutionOptions) (*upstreamtransport.Response, upstreamtransport.RequestDisclosure, error)

func (f disguiseTransportFunc) FetchUpstream(ctx context.Context, r *http.Request, o upstreamtransport.ExecutionOptions) (*upstreamtransport.Response, upstreamtransport.RequestDisclosure, error) {
	return f(ctx, r, o)
}

func TestHTTPDisguiseHeaderFailureDoesNotSend(t *testing.T) {
	var attempts atomic.Int32
	h, _ := disguiseHandler(t, "https://example.test", &httpDisguiseRepository{mapFailure: errors.New("mapping storage failed")}, disguiseTransportFunc(func(context.Context, *http.Request, upstreamtransport.ExecutionOptions) (*upstreamtransport.Response, upstreamtransport.RequestDisclosure, error) {
		attempts.Add(1)
		return nil, 0, errors.New("unexpected send")
	}))
	request := httptest.NewRequest("GET", "/codex/models", nil)
	request.Header.Set("Authorization", proxyCodexTestAuthorization)
	request.Header.Set("X-Client-Request-Id", "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if attempts.Load() != 0 || response.Code != 500 || !strings.Contains(response.Body.String(), "mapping storage failed") {
		t.Fatalf("sent=%d status=%d body=%s", attempts.Load(), response.Code, response.Body.String())
	}
}

func TestCodexHTTPAdmissionConversationStillWaits(t *testing.T) {
	started := make(chan struct{})
	var once sync.Once
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(started) })
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{}`)
	}))
	defer upstream.Close()
	h, _ := disguiseHandler(t, upstream.URL, &httpDisguiseRepository{}, nil)
	reader, writer := io.Pipe()
	request := httptest.NewRequest("POST", "/codex/responses", reader)
	request.Header.Set("Authorization", proxyCodexTestAuthorization)
	request.Header.Set("Content-Type", "application/json")
	done := make(chan struct{})
	go func() { defer close(done); h.ServeHTTP(httptest.NewRecorder(), request) }()
	_, _ = io.WriteString(writer, `{"model":"test","input":"`+strings.Repeat("x", 65536))
	select {
	case <-started:
		t.Fatal("conversation admitted before complete ownership facts")
	case <-time.After(50 * time.Millisecond):
	}
	_, _ = io.WriteString(writer, `"}`)
	_ = writer.Close()
	awaitIngressTest(t, done)
	awaitIngressTest(t, started)
}

func TestHTTPDisguiseResponseRestoresOnlyProtocolFields(t *testing.T) {
	const original = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	for _, sse := range []bool{false, true} {
		t.Run(map[bool]string{false: "json", true: "sse"}[sse], func(t *testing.T) {
			var sent string
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sent = r.Header.Get("X-Client-Request-Id")
				if sent == original || r.Header.Get("User-Agent") != "frozen-agent" {
					t.Error("request identity/profile was not derived")
				}
				w.Header().Set("X-Client-Request-Id", sent)
				payload := `{"type":"response.completed","request_id":"` + sent + `","text":"` + sent + `"}`
				if sse {
					w.Header().Set("Content-Type", "text/event-stream")
					payload = "data: " + payload + "\n\n"
				} else {
					w.Header().Set("Content-Type", "application/json")
				}
				_, _ = io.WriteString(w, payload)
			}))
			defer upstream.Close()
			h, _ := disguiseHandler(t, upstream.URL, &httpDisguiseRepository{}, nil)
			request := httptest.NewRequest("GET", "/codex/models", nil)
			request.Header.Set("Authorization", proxyCodexTestAuthorization)
			request.Header.Set("X-Client-Request-Id", original)
			response := httptest.NewRecorder()
			h.ServeHTTP(response, request)
			if response.Code != 200 || response.Header().Get("X-Client-Request-Id") != original {
				t.Fatalf("response=%d %v", response.Code, response.Header())
			}
			payload := strings.TrimSpace(strings.TrimPrefix(response.Body.String(), "data: "))
			var fields map[string]string
			if err := json.Unmarshal([]byte(payload), &fields); err != nil {
				t.Fatal(err)
			}
			if fields["request_id"] != original || fields["text"] != sent {
				t.Fatalf("restored protocol/business fields: %s", payload)
			}
		})
	}
}

func TestHTTPDisguiseResponseFailureRemainsHealthNeutral(t *testing.T) {
	for _, status := range []int{200, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				_, _ = io.WriteString(w, `{"request_id":"unterminated`)
			}))
			defer upstream.Close()
			h, store := disguiseHandler(t, upstream.URL, &httpDisguiseRepository{}, nil)
			store.configs[ConfigKeyGlobalMaxAttempts] = "1"
			health := newTrackingHealthManager()
			h.health = health
			request := httptest.NewRequest("GET", "/codex/models", nil)
			request.Header.Set("Authorization", proxyCodexTestAuthorization)
			response := httptest.NewRecorder()
			func() {
				defer func() {
					if value := recover(); value != nil && value != http.ErrAbortHandler {
						panic(value)
					}
				}()
				h.ServeHTTP(response, request)
			}()
			if calls.Load() != 1 || len(health.getMarkFailureCalls()) != 0 {
				t.Fatalf("sends=%d health penalties=%v", calls.Load(), health.getMarkFailureCalls())
			}
			waitFor(t, func() bool { store.mu.Lock(); defer store.mu.Unlock(); return len(store.logs) == 1 }, time.Second)
			store.mu.Lock()
			defer store.mu.Unlock()
			if store.logs[0].SessionEvidenceJSON == nil || !strings.Contains(*store.logs[0].SessionEvidenceJSON, `"decision":"failed"`) {
				t.Fatalf("missing response failure: %+v", store.logs[0])
			}
		})
	}
}

func TestHTTPDisguiseCancellationJoinsUploadWithoutConversionFailure(t *testing.T) {
	started := make(chan struct{})
	stopped := make(chan struct{})
	transport := disguiseTransportFunc(func(_ context.Context, r *http.Request, _ upstreamtransport.ExecutionOptions) (*upstreamtransport.Response, upstreamtransport.RequestDisclosure, error) {
		defer close(stopped)
		prefix := make([]byte, 128)
		_, _ = io.ReadFull(r.Body, prefix)
		close(started)
		_, err := io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
		return nil, upstreamtransport.RequestDisclosureConfirmed, err
	})
	h, store := disguiseHandler(t, "https://example.test", &httpDisguiseRepository{}, transport)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader, writer := io.Pipe()
	defer writer.Close()
	request := httptest.NewRequest("POST", "/codex/alpha/search", reader).WithContext(ctx)
	request.Header.Set("Authorization", proxyCodexTestAuthorization)
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { defer close(done); h.ServeHTTP(response, request) }()
	_, _ = io.WriteString(writer, `{"query":"`+strings.Repeat("x", 65536))
	awaitIngressTest(t, started)
	cancel()
	awaitIngressTest(t, stopped)
	awaitIngressTest(t, done)
	waitFor(t, func() bool { store.mu.Lock(); defer store.mu.Unlock(); return len(store.logs) == 1 }, time.Second)
	store.mu.Lock()
	defer store.mu.Unlock()
	if evidence := store.logs[0].SessionEvidenceJSON; evidence != nil && strings.Contains(*evidence, `"decision":"failed"`) {
		t.Fatalf("cancellation misclassified: %s", *evidence)
	}
}

func TestHTTPDisguiseFirstBindRaceReselectsWithoutAttempt(t *testing.T) {
	var sends atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sends.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{}`)
	}))
	defer upstream.Close()
	repo := &httpDisguiseRepository{excludeCommit: "disguise-provider-codex"}
	h, store := disguiseHandler(t, upstream.URL, repo, nil)
	store.providers = append(store.providers, withTestStaticCredential(model.Provider{ID: "second", Enabled: true, AuthMode: "bearer", ClientDisguise: clientdisguise.Policy{Enabled: true}, APITypes: []model.ProviderAPIType{{ProviderID: "second", APIType: APITypeCodex, BaseURL: upstream.URL}}}, APITypeCodex, "second-key"))
	request := httptest.NewRequest("GET", "/codex/models", nil)
	request.Header.Set("Authorization", proxyCodexTestAuthorization)
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != 200 || sends.Load() != 1 {
		t.Fatalf("response=%d sends=%d body=%s", response.Code, sends.Load(), response.Body.String())
	}
	waitFor(t, func() bool { store.mu.Lock(); defer store.mu.Unlock(); return len(store.attempts) == 1 }, time.Second)
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.attempts[0].ProviderID != "second" || store.attempts[0].Attempt != 0 {
		t.Fatalf("race consumed attempt: %+v", store.attempts)
	}
}

func TestHTTPDisguiseRetryFreezesTargetAndReopensOriginal(t *testing.T) {
	const original = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	repo := &httpDisguiseRepository{}
	var store *mockStore
	var headers []http.Header
	var bodies []string
	transport := disguiseTransportFunc(func(_ context.Context, r *http.Request, _ upstreamtransport.ExecutionOptions) (*upstreamtransport.Response, upstreamtransport.RequestDisclosure, error) {
		body, err := io.ReadAll(r.Body)
		_ = r.Body.Close()
		if err != nil {
			return nil, 0, err
		}
		headers = append(headers, r.Header.Clone())
		bodies = append(bodies, string(body))
		status := 200
		if len(headers) == 1 {
			status = 503
			store.providers[0].ClientDisguise.Enabled = false
		}
		response, err := upstreamtransport.NewResponse(upstreamtransport.ResponseHead{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, SourceHeader: http.Header{"Content-Type": []string{"application/json"}}}, io.NopCloser(strings.NewReader(`{}`)))
		return response, upstreamtransport.RequestDisclosureConfirmed, err
	})
	h, createdStore := disguiseHandler(t, "https://example.test", repo, transport)
	store = createdStore
	payload := `{"request_id":"` + original + `","business":"` + original + `"}`
	serve := func() {
		request := httptest.NewRequest("POST", "/codex/alpha/search", strings.NewReader(payload))
		request.Header.Set("Authorization", proxyCodexTestAuthorization)
		request.Header.Set("X-Client-Request-Id", original)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("User-Agent", "original-agent")
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if response.Code != 200 {
			t.Fatalf("response=%d %s", response.Code, response.Body.String())
		}
	}
	serve()
	if len(headers) != 2 || headers[0].Get("User-Agent") != "frozen-agent" || headers[1].Get("User-Agent") != "frozen-agent" || bodies[0] != bodies[1] {
		t.Fatalf("retry thawed target or reused derived as original: headers=%v bodies=%v", headers, bodies)
	}
	var fields map[string]string
	if err := json.Unmarshal([]byte(bodies[0]), &fields); err != nil {
		t.Fatal(err)
	}
	if fields["request_id"] != headers[0].Get("X-Client-Request-Id") || fields["request_id"] == original || fields["business"] != original {
		t.Fatalf("cross-carrier mapping=%v", fields)
	}
	serve()
	if len(headers) != 3 || headers[2].Get("User-Agent") != "original-agent" || bodies[2] != payload {
		t.Fatalf("new request ignored disabled policy: headers=%v bodies=%v", headers, bodies)
	}
}

func TestHTTPDisguiseOpaqueExtensionPreservesBody(t *testing.T) {
	const payload = "opaque business bytes\x00 are not JSON"
	transport := disguiseTransportFunc(func(_ context.Context, r *http.Request, _ upstreamtransport.ExecutionOptions) (*upstreamtransport.Response, upstreamtransport.RequestDisclosure, error) {
		body, err := io.ReadAll(r.Body)
		_ = r.Body.Close()
		if err != nil || string(body) != payload {
			t.Fatalf("opaque request=%q err=%v", body, err)
		}
		response, err := upstreamtransport.NewResponse(upstreamtransport.ResponseHead{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/octet-stream"}}, SourceHeader: http.Header{"Content-Type": []string{"application/octet-stream"}}}, io.NopCloser(strings.NewReader(payload)))
		return response, upstreamtransport.RequestDisclosureConfirmed, err
	})
	h, _ := disguiseHandler(t, "https://example.test", &httpDisguiseRepository{}, transport)
	request := httptest.NewRequest("POST", "/codex/alpha/search", strings.NewReader(payload))
	request.Header.Set("Authorization", proxyCodexTestAuthorization)
	request.Header.Set("Content-Type", "application/octet-stream")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != 200 || response.Body.String() != payload {
		t.Fatalf("opaque response=%d %q", response.Code, response.Body.String())
	}
}

func TestHTTPDisguiseSSEFailureAfterVisibilityAbortsWithEvidence(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: opaque-ready\n\n")
		_ = http.NewResponseController(w).Flush()
		_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"request_id\":\"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb\"}\n\n")
	}))
	defer upstream.Close()
	h, store := disguiseHandler(t, upstream.URL, &httpDisguiseRepository{restoreFailure: errors.New("inverse mapping unavailable")}, nil)
	health := newTrackingHealthManager()
	h.health = health
	request := httptest.NewRequest("GET", "/codex/models", nil)
	request.Header.Set("Authorization", proxyCodexTestAuthorization)
	response := httptest.NewRecorder()
	aborted := false
	func() {
		defer func() {
			if value := recover(); value == http.ErrAbortHandler {
				aborted = true
			} else if value != nil {
				panic(value)
			}
		}()
		h.ServeHTTP(response, request)
	}()
	if !aborted || response.Body.String() != "data: opaque-ready\n\n" || len(health.getMarkFailureCalls()) != 0 {
		t.Fatalf("aborted=%v response=%q penalties=%v", aborted, response.Body.String(), health.getMarkFailureCalls())
	}
	waitFor(t, func() bool { store.mu.Lock(); defer store.mu.Unlock(); return len(store.logs) == 1 }, time.Second)
	store.mu.Lock()
	defer store.mu.Unlock()
	if e := store.logs[0].SessionEvidenceJSON; e == nil || !strings.Contains(*e, "inverse mapping unavailable") || !strings.Contains(*e, `"decision":"failed"`) {
		t.Fatalf("SSE failure evidence=%v", e)
	}
}

func TestHTTPDisguiseBodylessResponsesPreserveRepresentationMetadata(t *testing.T) {
	for _, test := range []struct {
		method string
		status int
	}{{http.MethodHead, 200}, {http.MethodGet, 304}, {http.MethodGet, 204}, {http.MethodGet, 205}} {
		t.Run(test.method+http.StatusText(test.status), func(t *testing.T) {
			transport := disguiseTransportFunc(func(context.Context, *http.Request, upstreamtransport.ExecutionOptions) (*upstreamtransport.Response, upstreamtransport.RequestDisclosure, error) {
				header := http.Header{"Content-Type": []string{"application/json"}, "Content-Encoding": []string{"gzip"}, "Content-Length": []string{"128"}, "Etag": []string{"original-representation"}}
				response, err := upstreamtransport.NewResponse(upstreamtransport.ResponseHead{StatusCode: test.status, ContentLength: 128, Header: header, SourceHeader: header.Clone()}, http.NoBody)
				return response, upstreamtransport.RequestDisclosureConfirmed, err
			})
			h, _ := disguiseHandler(t, "https://example.test", &httpDisguiseRepository{}, transport)
			request := httptest.NewRequest(test.method, "/codex/models", nil)
			request.Header.Set("Authorization", proxyCodexTestAuthorization)
			response := httptest.NewRecorder()
			h.ServeHTTP(response, request)
			if response.Code != test.status || response.Body.Len() != 0 || response.Header().Get("Etag") != "original-representation" || response.Header().Get("Content-Encoding") != "gzip" || response.Header().Get("Content-Length") != "128" {
				t.Fatalf("bodyless response=%d %v %q", response.Code, response.Header(), response.Body.String())
			}
		})
	}
}

type disguiseCircuitHealth struct{ *trackingHealthManager }

func (h disguiseCircuitHealth) MarkFailure(ctx context.Context, providerID string, err error) bool {
	h.trackingHealthManager.MarkFailure(ctx, providerID, err)
	h.mu.Lock()
	h.available[providerID] = false
	h.mu.Unlock()
	return true
}

func TestHTTPDisguiseDeferredHealthDoesNotSpendUnsentRetry(t *testing.T) {
	var sends []string
	transport := disguiseTransportFunc(func(_ context.Context, r *http.Request, _ upstreamtransport.ExecutionOptions) (*upstreamtransport.Response, upstreamtransport.RequestDisclosure, error) {
		sends = append(sends, r.Header.Get("Authorization"))
		status := 200
		if len(sends) == 1 {
			status = 503
		}
		head := http.Header{"Content-Type": []string{"application/json"}}
		response, err := upstreamtransport.NewResponse(upstreamtransport.ResponseHead{StatusCode: status, Header: head, SourceHeader: head.Clone()}, io.NopCloser(strings.NewReader(`{}`)))
		return response, upstreamtransport.RequestDisclosureConfirmed, err
	})
	h, store := disguiseHandler(t, "https://example.test", &httpDisguiseRepository{}, transport)
	store.configs[ConfigKeyGlobalMaxAttempts] = "2"
	h.health = disguiseCircuitHealth{newTrackingHealthManager()}
	store.providers = append(store.providers, withTestStaticCredential(model.Provider{ID: "second", Enabled: true, AuthMode: "bearer", ClientDisguise: clientdisguise.Policy{Enabled: true}, APITypes: []model.ProviderAPIType{{ProviderID: "second", APIType: APITypeCodex, BaseURL: "https://example.test"}}}, APITypeCodex, "second-key"))
	request := httptest.NewRequest("GET", "/codex/models", nil)
	request.Header.Set("Authorization", proxyCodexTestAuthorization)
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != 200 || len(sends) != 2 || sends[1] != "Bearer second-key" {
		t.Fatalf("response=%d sends=%v", response.Code, sends)
	}
	waitFor(t, func() bool { store.mu.Lock(); defer store.mu.Unlock(); return len(store.attempts) == 2 }, time.Second)
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.attempts[1].Attempt != 1 {
		t.Fatalf("unsent retry consumed budget: %+v", store.attempts)
	}
}

func TestHTTPDisguiseTargetCommitFailureReturnsDiagnosticWithoutAttempt(t *testing.T) {
	var sends atomic.Int32
	h, store := disguiseHandler(t, "https://example.test", &httpDisguiseRepository{commitFailure: errors.New("commit storage unavailable")}, disguiseTransportFunc(func(context.Context, *http.Request, upstreamtransport.ExecutionOptions) (*upstreamtransport.Response, upstreamtransport.RequestDisclosure, error) {
		sends.Add(1)
		return nil, 0, errors.New("unexpected send")
	}))
	health := newTrackingHealthManager()
	h.health = health
	request := httptest.NewRequest("GET", "/codex/models", nil)
	request.Header.Set("Authorization", proxyCodexTestAuthorization)
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != 500 || response.Header().Get("X-Switch-A-Diagnostic-Id") == "" || sends.Load() != 0 || len(health.getMarkFailureCalls()) != 0 {
		t.Fatalf("status=%d body=%s sends=%d", response.Code, response.Body.String(), sends.Load())
	}
	waitFor(t, func() bool { store.mu.Lock(); defer store.mu.Unlock(); return len(store.logs) == 1 }, time.Second)
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.attempts) != 0 || store.logs[0].SessionEvidenceJSON == nil || !strings.Contains(*store.logs[0].SessionEvidenceJSON, "commit storage unavailable") {
		t.Fatalf("failed target evidence=%+v attempts=%+v", store.logs, store.attempts)
	}
}
