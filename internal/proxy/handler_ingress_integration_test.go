package proxy

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/requestingress/clientconnection"
	"github.com/doraemonkeys/switch-a/internal/requestingress/h2ingress"
	"go.uber.org/zap"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
)

func awaitIngressTest(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("ingress milestone did not settle")
	}
}

func ingressIntegrationHandler(t *testing.T, upstreamURL string, registry *ActiveRequestRegistry) *Handler {
	store := newMockStore()
	store.configs[ConfigKeyStickyMode] = "off"
	store.configs[ConfigKeyGlobalMaxAttempts] = "1"
	store.providers = []model.Provider{withTestStaticCredential(model.Provider{
		ID: "ingress-provider", Enabled: true, AuthMode: "bearer",
		APITypes: []model.ProviderAPIType{{ProviderID: "ingress-provider", APIType: APITypeClaude, BaseURL: upstreamURL}},
	}, "", "test-key")}
	return newProxyCodexTestHandler(t, Config{Store: store, ActiveRegistry: registry, Logger: zap.NewNop()})
}

func TestHandlerIngressEarlyResponseThenSilentUpstreamClientDisconnect(t *testing.T) {
	upstreamCanceled := make(chan struct{})
	upstreamStarted := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = http.NewResponseController(w).EnableFullDuplex()
		go func() { _, _ = io.Copy(io.Discard, r.Body) }()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: ready\n\n")
		_ = http.NewResponseController(w).Flush()
		close(upstreamStarted)
		select {
		case <-r.Context().Done():
			close(upstreamCanceled)
		case <-release:
		}
	}))
	defer upstream.Close()
	defer once.Do(func() { close(release) })
	removed := make(chan struct{})
	registry := NewActiveRequestRegistryWithHook(func(ActiveRequest, ActiveRequestRemovalReason) { close(removed) })
	handler := ingressIntegrationHandler(t, upstream.URL, registry)
	server := httptest.NewUnstartedServer(handler)
	server.Listener = clientconnection.Listen(server.Listener)
	server.Config.ConnContext = clientconnection.Context
	server.Start()
	defer server.Close()
	conn, err := net.Dial("tcp", server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, err = io.WriteString(conn, "POST /v1/messages HTTP/1.1\r\nHost: localhost\r\nContent-Length: 100000\r\nContent-Type: application/json\r\n\r\n{\"input\":\"")
	if err != nil {
		t.Fatal(err)
	}
	awaitIngressTest(t, upstreamStarted)
	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(response.Body).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if line != "data: ready\n" {
		t.Fatalf("response=%q", line)
	}
	// Upload never reached EOF, and upstream has no timeout or further writes.
	_ = conn.Close()
	awaitIngressTest(t, upstreamCanceled)
	awaitIngressTest(t, removed)
	if got := len(registry.List()); got != 0 {
		t.Fatalf("active leases=%d", got)
	}
}

func TestHandlerIngressReplacementReplaysPrefixAndFollowsLiveTail(t *testing.T) {
	prefix := `{"input":"`
	tail := `continued"}`
	firstSeen := make(chan struct{})
	replacementSeen := make(chan struct{})
	received := make(chan string, 1)
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = http.NewResponseController(w).EnableFullDuplex()
		read := make([]byte, len(prefix))
		_, err := io.ReadFull(r.Body, read)
		if err != nil {
			t.Error(err)
			return
		}
		close(firstSeen)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, "retry")
		_ = http.NewResponseController(w).Flush()
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		read := make([]byte, len(prefix))
		if _, err := io.ReadFull(r.Body, read); err != nil {
			t.Error(err)
			return
		}
		close(replacementSeen)
		remaining, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		received <- string(read) + string(remaining)
		_, _ = io.WriteString(w, "ok")
	}))
	defer second.Close()
	handler := ingressIntegrationHandler(t, first.URL, nil)
	store := handler.store.(*mockStore)
	store.configs[ConfigKeyGlobalMaxAttempts] = "2"
	store.providers[0].MaxRetries = 0
	secondProvider := withTestStaticCredential(model.Provider{ID: "replacement", Enabled: true, AuthMode: "bearer",
		APITypes: []model.ProviderAPIType{{ProviderID: "replacement", APIType: APITypeClaude, BaseURL: second.URL}}}, "", "test-key")
	store.providers = append(store.providers, secondProvider)
	reader, writer := io.Pipe()
	defer writer.Close()
	request := httptest.NewRequest(http.MethodPost, RouteClaudeMessages, reader)
	request.ContentLength = -1
	recorder := httptest.NewRecorder()
	finished := make(chan struct{})
	go func() { handler.ServeHTTP(recorder, request); close(finished) }()
	_, _ = io.WriteString(writer, prefix)
	awaitIngressTest(t, firstSeen)
	awaitIngressTest(t, replacementSeen)
	_, _ = io.WriteString(writer, tail)
	_ = writer.Close()
	awaitIngressTest(t, finished)
	if got := <-received; got != prefix+tail {
		t.Fatalf("replacement body=%q", got)
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body)
	}
}

func TestHandlerIngressLimitDuringUploadReturns413WithoutProviderPenalty(t *testing.T) {
	receiving := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		prefix := make([]byte, 1)
		if _, err := io.ReadFull(r.Body, prefix); err != nil {
			return
		}
		close(receiving)
		_, _ = io.Copy(io.Discard, r.Body)
	}))
	defer upstream.Close()
	handler := ingressIntegrationHandler(t, upstream.URL, nil)
	store := handler.store.(*mockStore)
	store.configs[ConfigKeyMaxBodySize] = "1"
	health := newTrackingHealthManager()
	handler.health = health
	reader, writer := io.Pipe()
	defer writer.Close()
	request := httptest.NewRequest(http.MethodPost, RouteClaudeMessages, reader)
	request.ContentLength = -1
	recorder := httptest.NewRecorder()
	finished := make(chan struct{})
	go func() { handler.ServeHTTP(recorder, request); close(finished) }()
	_, _ = io.WriteString(writer, "{")
	awaitIngressTest(t, receiving)
	_, _ = io.WriteString(writer, strings.Repeat("x", 1<<20))
	_ = writer.Close()
	awaitIngressTest(t, finished)
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body)
	}
	if len(health.getMarkFailureCalls()) != 0 {
		t.Fatal("ingress limit damaged provider health")
	}
}

func TestHandlerIngressHTTP2UndeclaredTrailerReachesHTTP1Upstream(t *testing.T) {
	const payload = "client-wire-body"
	trailers := make(chan http.Header, 1)
	prefixRead := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, len(payload))
		if _, err := io.ReadFull(r.Body, body); err != nil {
			t.Error(err)
			return
		}
		close(prefixRead)
		remaining, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		if string(body) != payload || len(remaining) != 0 {
			t.Errorf("body=%q remaining=%q", body, remaining)
		}
		if r.ProtoMajor != 1 {
			t.Errorf("upstream protocol=%s", r.Proto)
		}
		trailers <- r.Trailer.Clone()
		_, _ = io.WriteString(w, "ok")
	}))
	defer upstream.Close()
	handler := ingressIntegrationHandler(t, upstream.URL, nil)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 2 {
			t.Errorf("client protocol=%s", r.Proto)
		}
		if r.Trailer != nil {
			t.Errorf("initial trailer=%v; want undeclared nil map", r.Trailer)
		}
		handler.ServeHTTP(w, r)
	}))
	server.EnableHTTP2 = true
	server.Listener = clientconnection.Listen(server.Listener)
	server.Config.ConnContext = clientconnection.Context
	if err := h2ingress.Configure(server.Config, zap.NewNop()); err != nil {
		t.Fatal(err)
	}
	server.StartTLS()
	defer server.Close()
	conn, err := tls.Dial("tcp", server.Listener.Addr().String(), &tls.Config{InsecureSkipVerify: true, NextProtos: []string{http2.NextProtoTLS}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err = io.WriteString(conn, http2.ClientPreface); err != nil {
		t.Fatal(err)
	}
	framer := http2.NewFramer(conn, conn)
	if err = framer.WriteSettings(); err != nil {
		t.Fatal(err)
	}
	var block bytes.Buffer
	encoder := hpack.NewEncoder(&block)
	for _, field := range []hpack.HeaderField{
		{Name: ":method", Value: http.MethodPost}, {Name: ":scheme", Value: "https"},
		{Name: ":authority", Value: server.Listener.Addr().String()}, {Name: ":path", Value: RouteClaudeMessages},
		{Name: "content-length", Value: strconv.Itoa(len(payload))},
	} {
		if err = encoder.WriteField(field); err != nil {
			t.Fatal(err)
		}
	}
	if err = framer.WriteHeaders(http2.HeadersFrameParam{StreamID: 1, BlockFragment: block.Bytes(), EndHeaders: true}); err != nil {
		t.Fatal(err)
	}
	if err = framer.WriteData(1, false, []byte(payload)); err != nil {
		t.Fatal(err)
	}
	awaitIngressTest(t, prefixRead)
	block.Reset()
	if err = encoder.WriteField(hpack.HeaderField{Name: "x-late", Value: "after-eof"}); err != nil {
		t.Fatal(err)
	}
	if err = framer.WriteHeaders(http2.HeadersFrameParam{StreamID: 1, BlockFragment: block.Bytes(), EndHeaders: true, EndStream: true}); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-trailers:
		if got.Get("X-Late") != "after-eof" {
			t.Fatalf("late trailer lost: %v", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("upstream trailer not completed")
	}
}
func TestHandlerIngressCommittedOverflowTerminatesResponse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = http.NewResponseController(w).EnableFullDuplex()
		go func() { _, _ = io.Copy(io.Discard, r.Body) }()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: visible\n\n")
		_ = http.NewResponseController(w).Flush()
		<-r.Context().Done()
	}))
	defer upstream.Close()
	handler := ingressIntegrationHandler(t, upstream.URL, nil)
	handler.store.(*mockStore).configs[ConfigKeyMaxBodySize] = "1"
	health := newTrackingHealthManager()
	handler.health = health
	server := httptest.NewUnstartedServer(handler)
	server.Listener = clientconnection.Listen(server.Listener)
	server.Config.ConnContext = clientconnection.Context
	server.Start()
	defer server.Close()
	reader, writer := io.Pipe()
	defer writer.Close()
	request, err := http.NewRequest(http.MethodPost, server.URL+RouteClaudeMessages, reader)
	if err != nil {
		t.Fatal(err)
	}
	responseReady := make(chan *http.Response, 1)
	errorsReady := make(chan error, 1)
	go func() {
		response, err := server.Client().Do(request)
		if err != nil {
			errorsReady <- err
			return
		}
		responseReady <- response
	}()
	_, _ = io.WriteString(writer, "{")
	var response *http.Response
	select {
	case response = <-responseReady:
	case err := <-errorsReady:
		t.Fatal(err)
	case <-time.After(5 * time.Second):
		t.Fatal("no early response")
	}
	buffered := bufio.NewReader(response.Body)
	if line, err := buffered.ReadString('\n'); err != nil || line != "data: visible\n" {
		t.Fatalf("visible line=%q err=%v", line, err)
	}
	_, _ = io.WriteString(writer, strings.Repeat("x", 1<<20))
	_ = writer.Close()
	_, err = io.ReadAll(buffered)
	_ = response.Body.Close()
	if err == nil {
		t.Fatal("committed source overflow returned a complete response")
	}
	if len(health.getMarkFailureCalls()) != 0 {
		t.Fatal("committed ingress failure damaged provider health")
	}
}

func TestHandlerIngressStreamsBeforeEOF(t *testing.T) {
	received := make(chan struct{})
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		prefix := make([]byte, 1)
		if _, err := io.ReadFull(r.Body, prefix); err != nil {
			t.Error(err)
			return
		}
		close(received)
		<-release
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = io.WriteString(w, "ok")
	}))
	defer upstream.Close()
	handler := ingressIntegrationHandler(t, upstream.URL, nil)
	reader, writer := io.Pipe()
	request := httptest.NewRequest(http.MethodPost, RouteClaudeMessages, reader)
	request.ContentLength = -1
	recorder := httptest.NewRecorder()
	finished := make(chan struct{})
	go func() { handler.ServeHTTP(recorder, request); close(finished) }()
	_, _ = io.WriteString(writer, "{")
	awaitIngressTest(t, received)
	close(release)
	_, _ = io.WriteString(writer, "\"model\":\"late-model\"}")
	_ = writer.Close()
	awaitIngressTest(t, finished)
	if recorder.Code != http.StatusOK || strings.TrimSpace(recorder.Body.String()) != "ok" {
		t.Fatalf("response=%d %s", recorder.Code, recorder.Body.String())
	}
}
