package websocketproxy

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

	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/requestcapture"
	"github.com/doraemonkeys/switch-a/internal/responseanalysis/tokenusage"

	"github.com/coder/websocket"
)

const (
	hostileCaptureDeadline = 250 * time.Millisecond
	testPollTimeout        = 100 * time.Millisecond
)

type Handler = Gateway

func NewHandler(cfg Config) *Gateway { return NewGateway(cfg) }

func observedTokenCount(value int64) tokenusage.ObservedCount {
	return tokenusage.ObservedCount{Value: value, Present: true}
}

type LiveBytesTracker struct {
	BytesSent, BytesReceived, MsgsSent, MsgsReceived atomic.Int64
	LastActivityAt                                   atomic.Int64
}

func (tracker *LiveBytesTracker) ObserveClientToUpstream(bytes int64) {
	tracker.BytesSent.Add(bytes)
	tracker.MsgsSent.Add(1)
	tracker.LastActivityAt.Store(time.Now().UnixMilli())
}

func (tracker *LiveBytesTracker) ObserveUpstreamToClient(bytes int64) {
	tracker.BytesReceived.Add(bytes)
	tracker.MsgsReceived.Add(1)
	tracker.LastActivityAt.Store(time.Now().UnixMilli())
}

type mockStore struct {
	mu               sync.Mutex
	providers        []model.Provider
	routingPolicies  []model.RoutingPolicy
	routingPolicyErr error
	logs             []model.RequestLog
	attempts         []model.RequestAttempt
}

func newMockStore() *mockStore { return &mockStore{} }

func (store *mockStore) ListProvidersByAPIType(context.Context, string) ([]model.Provider, error) {
	return append([]model.Provider(nil), store.providers...), nil
}

func (*mockStore) GetConfig(context.Context, string) (string, error) { return "", nil }

func (store *mockStore) InsertLog(_ context.Context, log *model.RequestLog) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.logs = append(store.logs, *log)
	return nil
}

func (store *mockStore) InsertAttempts(_ context.Context, attempts []model.RequestAttempt) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.attempts = append(store.attempts, attempts...)
	return nil
}

func (store *mockStore) LastLog() *model.RequestLog {
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.logs) == 0 {
		return nil
	}
	log := store.logs[len(store.logs)-1]
	return &log
}

func (store *mockStore) LastAttempts(count int) []model.RequestAttempt {
	store.mu.Lock()
	defer store.mu.Unlock()
	if count <= 0 || len(store.attempts) == 0 {
		return nil
	}
	if count > len(store.attempts) {
		count = len(store.attempts)
	}
	return append([]model.RequestAttempt(nil), store.attempts[len(store.attempts)-count:]...)
}

func requestLogClientTransportStatusCode(log *model.RequestLog) int {
	if log == nil || log.ClientTransportStatusCode == nil {
		return StatusCodeNoResponse
	}
	return *log.ClientTransportStatusCode
}

func requestLogServiceOutcome(log *model.RequestLog) model.ServiceOutcome {
	if log == nil || log.ServiceOutcome == nil {
		return ""
	}
	return *log.ServiceOutcome
}

func requestLogTerminationReason(log *model.RequestLog) model.TerminationReason {
	if log == nil || log.TerminationReason == nil {
		return ""
	}
	return *log.TerminationReason
}

func requestLogClientAction(log *model.RequestLog) model.ClientAction {
	if log == nil || log.ClientAction == nil {
		return ""
	}
	return *log.ClientAction
}

func requestLogEvidenceMessage(t *testing.T, log *model.RequestLog) string {
	t.Helper()
	if log == nil || log.SessionEvidenceJSON == nil || *log.SessionEvidenceJSON == "" {
		return ""
	}
	var evidence webSocketEvidence
	if err := json.Unmarshal([]byte(*log.SessionEvidenceJSON), &evidence); err != nil {
		t.Fatalf("decode SessionEvidenceJSON: %v", err)
	}
	switch {
	case evidence.Gateway != nil && evidence.Gateway.TerminalMessageSnippet != "":
		return evidence.Gateway.TerminalMessageSnippet
	case evidence.UpstreamHandshake != nil && evidence.UpstreamHandshake.BodySnippet != "":
		return evidence.UpstreamHandshake.BodySnippet
	case evidence.UpstreamEvent != nil && evidence.UpstreamEvent.RawPayloadSnippet != "":
		return evidence.UpstreamEvent.RawPayloadSnippet
	case evidence.UpstreamEvent != nil:
		return evidence.UpstreamEvent.MessageSnippet
	case evidence.Transport != nil:
		return evidence.Transport.RawErrorSnippet
	default:
		return ""
	}
}

type trackingHealthManager struct {
	mu               sync.Mutex
	markFailureCalls []markFailureCall
	markSuccessIDs   []string
	suspendCalls     []suspendCall
	available        map[string]bool
}

type markFailureCall struct {
	providerID string
	err        error
}

type suspendCall struct {
	providerID    string
	disabledUntil time.Time
	reason        string
}

func newTrackingHealthManager() *trackingHealthManager {
	return &trackingHealthManager{available: make(map[string]bool)}
}

func (manager *trackingHealthManager) MarkSuccess(_ context.Context, providerID string) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.markSuccessIDs = append(manager.markSuccessIDs, providerID)
}

func (manager *trackingHealthManager) MarkFailure(_ context.Context, providerID string, err error) bool {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.markFailureCalls = append(manager.markFailureCalls, markFailureCall{providerID: providerID, err: err})
	return false
}

func (*trackingHealthManager) RecoverIfExpired(context.Context, string) bool { return false }

func (manager *trackingHealthManager) IsAvailable(_ context.Context, providerID string) bool {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	available, ok := manager.available[providerID]
	return !ok || available
}

func (*trackingHealthManager) ManualDisable(context.Context, string, string) error { return nil }

func (manager *trackingHealthManager) SuspendUntil(_ context.Context, providerID string, disabledUntil time.Time, reason string) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.suspendCalls = append(manager.suspendCalls, suspendCall{providerID: providerID, disabledUntil: disabledUntil, reason: reason})
	return nil
}

func (*trackingHealthManager) ManualEnable(context.Context, string) error { return nil }

func (*trackingHealthManager) ResetCircuitBreaker(string) {}

func (manager *trackingHealthManager) getMarkFailureCalls() []markFailureCall {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return append([]markFailureCall(nil), manager.markFailureCalls...)
}

func (manager *trackingHealthManager) getMarkSuccessIDs() []string {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return append([]string(nil), manager.markSuccessIDs...)
}

func (manager *trackingHealthManager) getSuspendCalls() []suspendCall {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return append([]suspendCall(nil), manager.suspendCalls...)
}

func (store *mockStore) ListRoutingPoliciesByAPIType(_ context.Context, apiType string) ([]model.RoutingPolicy, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.routingPolicyErr != nil {
		return nil, store.routingPolicyErr
	}
	var policies []model.RoutingPolicy
	for _, policy := range store.routingPolicies {
		if policy.APIType == apiType {
			policies = append(policies, policy)
		}
	}
	return policies, nil
}

type proxyHostileCaptureError struct {
	calls atomic.Int32
	block <-chan struct{}
}

func (e *proxyHostileCaptureError) attack() {
	e.calls.Add(1)
	<-e.block
	panic("hostile capture error method invoked")
}

func (e *proxyHostileCaptureError) Error() string { e.attack(); return "" }
func (e *proxyHostileCaptureError) As(any) bool   { e.attack(); return false }
func (e *proxyHostileCaptureError) Is(error) bool { e.attack(); return false }
func (e *proxyHostileCaptureError) Unwrap() error { e.attack(); return nil }
func (e *proxyHostileCaptureError) Timeout() bool { e.attack(); return false }

type captureReadErrorBody struct {
	payload []byte
	err     error
	read    bool
}

func (b *captureReadErrorBody) Read(p []byte) (int, error) {
	if b.read {
		return 0, b.err
	}
	b.read = true
	return copy(p, b.payload), b.err
}

func (*captureReadErrorBody) Close() error { return nil }

type noProbeBoundaryReadCloser struct {
	remaining         int64
	readsPastBoundary int
	closed            bool
}

func (r *noProbeBoundaryReadCloser) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		r.readsPastBoundary++
		return 0, errors.New("unexpected read past boundary")
	}
	n := min(int64(len(p)), r.remaining)
	for index := range p[:n] {
		p[index] = 'x'
	}
	r.remaining -= n
	return int(n), nil
}

func (r *noProbeBoundaryReadCloser) Close() error { r.closed = true; return nil }

func startCaptureTestManager(t *testing.T, providers []requestcapture.ProviderIdentity) (*requestcapture.Manager, requestcapture.SessionInfo) {
	t.Helper()
	manager, err := requestcapture.NewManager(requestcapture.Config{})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	session, err := manager.Start(requestcapture.StartRequest{
		Providers: providers, AcknowledgeRawPayloadRisk: true,
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	return manager, session
}

func waitFor(t *testing.T, condition func() bool, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}

// newEchoWSServer keeps the common echo fixture in one place so transport tests
// can stay focused on forwarder behavior instead of repeating server plumbing.
func newEchoWSServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Logf("echo server accept error: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		conn.SetReadLimit(wsReadLimit)
		for {
			msgType, data, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			if err := conn.Write(r.Context(), msgType, data); err != nil {
				return
			}
		}
	}))
}

// newCloseAfterNWSServer isolates close-sequence fixtures because multiple test
// themes depend on the same protocol shape.
func newCloseAfterNWSServer(t *testing.T, n int, code websocket.StatusCode, reason string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		conn.SetReadLimit(wsReadLimit)
		for range n {
			msgType, data, err := conn.Read(r.Context())
			if err != nil {
				conn.Close(websocket.StatusInternalError, "read failed")
				return
			}
			if err := conn.Write(r.Context(), msgType, data); err != nil {
				return
			}
		}
		conn.Close(code, reason)
	}))
}

// newHeaderCapturingWSServer centralizes handshake capture because several
// header-propagation tests share the same contract.
func newHeaderCapturingWSServer(t *testing.T, captured *http.Header, mu *sync.Mutex) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		*captured = r.Header.Clone()
		mu.Unlock()

		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		conn.Close(websocket.StatusNormalClosure, "")
	}))
}

func newSemanticErrorWSServer(t *testing.T, payload []byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		conn.SetReadLimit(wsReadLimit)
		if _, _, err := conn.Read(r.Context()); err != nil {
			return
		}
		_ = conn.Write(r.Context(), websocket.MessageText, payload)
		<-r.Context().Done()
	}))
}

func newRecordingWSServer(t *testing.T, received chan<- webSocketReplayMessage) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		conn.SetReadLimit(wsReadLimit)
		messageType, data, err := conn.Read(r.Context())
		if err != nil {
			return
		}
		received <- webSocketReplayMessage{
			MessageType: messageType,
			Data:        append([]byte(nil), data...),
		}
	}))
}

func newPushMessagesWSServer(t *testing.T, messages []webSocketReplayMessage) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		for _, message := range messages {
			if err := conn.Write(r.Context(), message.MessageType, message.Data); err != nil {
				return
			}
		}
	}))
}

func wsURL(s *httptest.Server) string {
	return "ws" + strings.TrimPrefix(s.URL, "http")
}

func connectWSClient(t *testing.T, ctx context.Context, url string) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	return conn
}

func readTerminalGatewayErrorEvent(
	t *testing.T,
	ctx context.Context,
	conn *websocket.Conn,
	wantStatus int,
	wantCode string,
) webSocketGatewayErrorEnvelope {
	t.Helper()

	msgType, payload, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read terminal gateway event: %v", err)
	}
	if msgType != websocket.MessageText {
		t.Fatalf("message type = %v, want %v", msgType, websocket.MessageText)
	}

	var envelope webSocketGatewayErrorEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		t.Fatalf("decode terminal gateway event %q: %v", string(payload), err)
	}
	if envelope.Type != webSocketGatewayErrorEventType {
		t.Fatalf("event type = %q, want %q", envelope.Type, webSocketGatewayErrorEventType)
	}
	if envelope.Error.Type != webSocketGatewayErrorType {
		t.Fatalf("error.type = %q, want %q", envelope.Error.Type, webSocketGatewayErrorType)
	}
	if envelope.Status != wantStatus {
		t.Fatalf("status = %d, want %d", envelope.Status, wantStatus)
	}
	if envelope.Error.Code != wantCode {
		t.Fatalf("error.code = %q, want %q", envelope.Error.Code, wantCode)
	}

	if _, _, err := conn.Read(ctx); err == nil || (!errors.Is(err, io.EOF) && !isNormalClose(err)) {
		t.Fatalf("expected websocket close after terminal gateway error, got %v", err)
	}

	return envelope
}

type mockDialer struct {
	dialFunc func(ctx context.Context, url string, opts *websocket.DialOptions) (*websocket.Conn, *http.Response, error)
}

func (m *mockDialer) Dial(ctx context.Context, url string, opts *websocket.DialOptions) (*websocket.Conn, *http.Response, error) {
	if m.dialFunc != nil {
		return m.dialFunc(ctx, url, opts)
	}
	return nil, nil, nil
}
