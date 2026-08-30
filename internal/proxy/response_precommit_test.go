package proxy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/continuity"
	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/codex/http"
	"github.com/doraemonkeys/switch-a/internal/codex/identity"
	"github.com/doraemonkeys/switch-a/internal/responseanalysis"
	"github.com/doraemonkeys/switch-a/internal/upstreamtransport"
)

type preCommitTestWriter struct {
	header       http.Header
	status       int
	writeN       int
	writeErr     error
	flushErr     error
	onPhysical   func(string)
	writtenBytes []byte
}

func (w *preCommitTestWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *preCommitTestWriter) WriteHeader(status int) {
	if w.onPhysical != nil {
		w.onPhysical("header")
	}
	w.status = status
}

func (w *preCommitTestWriter) Write(payload []byte) (int, error) {
	if w.onPhysical != nil {
		w.onPhysical("write")
	}
	n := w.writeN
	if n < 0 || n > len(payload) {
		n = len(payload)
	}
	w.writtenBytes = append(w.writtenBytes, payload[:n]...)
	return n, w.writeErr
}

func (w *preCommitTestWriter) Flush() {
	if w.onPhysical != nil {
		w.onPhysical("flush")
	}
}

func (w *preCommitTestWriter) FlushError() error {
	if w.onPhysical != nil {
		w.onPhysical("flush-error")
	}
	return w.flushErr
}

func TestFirstWriteResponseWriterGatesEveryVisibilityBoundary(t *testing.T) {
	methods := []struct {
		name string
		act  func(*firstWriteResponseWriter)
	}{
		{name: "WriteHeader", act: func(writer *firstWriteResponseWriter) { writer.WriteHeader(http.StatusAccepted) }},
		{name: "Write", act: func(writer *firstWriteResponseWriter) { _, _ = writer.Write([]byte("body")) }},
		{name: "Flush", act: func(writer *firstWriteResponseWriter) { writer.Flush() }},
		{name: "FlushError", act: func(writer *firstWriteResponseWriter) { _ = writer.FlushError() }},
		{name: "ReadFrom", act: func(writer *firstWriteResponseWriter) { _, _ = writer.ReadFrom(&oneByteReader{}) }},
	}
	for _, test := range methods {
		t.Run(test.name, func(t *testing.T) {
			prepared := false
			commits := 0
			physical := 0
			underlying := &preCommitTestWriter{writeN: -1}
			underlying.onPhysical = func(string) {
				physical++
				if !prepared {
					t.Fatal("underlying writer became visible before the pre-commit gate")
				}
			}
			writer := &firstWriteResponseWriter{
				ResponseWriter: underlying,
				prepareVisible: func(http.Header) (*codexhttp.Visibility, error) {
					prepared = true
					return &codexhttp.Visibility{}, nil
				},
				commitVisible: func(*codexhttp.Visibility) error { commits++; return nil },
			}
			test.act(writer)
			if !prepared || physical == 0 || commits != 1 {
				t.Fatalf("prepared/physical/commits = %t/%d/%d", prepared, physical, commits)
			}
		})
	}
}

func TestFirstWriteResponseWriterGateFailureCanEmitGatewayError(t *testing.T) {
	gateFailure := errors.New("database unavailable")
	underlying := &preCommitTestWriter{writeN: -1}
	writer := &firstWriteResponseWriter{
		ResponseWriter: underlying,
		prepareVisible: func(http.Header) (*codexhttp.Visibility, error) {
			return nil, gateFailure
		},
		onGateFailure: func(error) {
			underlying.Header().Set("Content-Type", "application/json")
			underlying.WriteHeader(http.StatusServiceUnavailable)
		},
	}
	writer.WriteHeader(http.StatusOK)
	if underlying.status != http.StatusServiceUnavailable || !errors.Is(writer.gateErr, gateFailure) {
		t.Fatalf("status/gate error = %d/%v", underlying.status, writer.gateErr)
	}
}

func TestFirstWriteResponseWriterShortAndUncertainWrites(t *testing.T) {
	tests := []struct {
		name          string
		writeN        int
		writeErr      error
		wantErr       error
		wantCommit    int
		wantUncertain int
	}{
		{name: "short visible", writeN: 2, wantErr: io.ErrShortWrite, wantCommit: 1},
		{name: "zero short", writeN: 0, wantErr: io.ErrShortWrite, wantUncertain: 1},
		{name: "zero failure", writeN: 0, writeErr: errors.New("write failed"), wantUncertain: 1},
		{name: "partial failure", writeN: 2, writeErr: errors.New("write failed"), wantCommit: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			commits, uncertain := 0, 0
			underlying := &preCommitTestWriter{writeN: test.writeN, writeErr: test.writeErr}
			writer := &firstWriteResponseWriter{
				ResponseWriter: underlying,
				prepareVisible: func(http.Header) (*codexhttp.Visibility, error) { return &codexhttp.Visibility{}, nil },
				commitVisible:  func(*codexhttp.Visibility) error { commits++; return nil },
				onUncertain:    func() { uncertain++ },
			}
			_, err := writer.Write([]byte("body"))
			if test.wantErr != nil && !errors.Is(err, test.wantErr) {
				t.Fatalf("Write error = %v, want %v", err, test.wantErr)
			}
			if test.writeErr != nil && !errors.Is(err, test.writeErr) {
				t.Fatalf("Write error = %v, want %v", err, test.writeErr)
			}
			if commits != test.wantCommit || uncertain != test.wantUncertain {
				t.Fatalf("commits/uncertain = %d/%d, want %d/%d", commits, uncertain, test.wantCommit, test.wantUncertain)
			}
		})
	}
}

func TestFirstWriteResponseWriterSSEWaitsForCompleteEventBeforeVisibility(t *testing.T) {
	attempt, gate, continuity := newPreCommitSSEGate(t)
	raw := []byte("event: response.created\r\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"response-ref\"}}\r\n\r\n")
	split := len(raw) - 3
	underlying := &preCommitTestWriter{writeN: -1}
	underlying.onPhysical = func(stage string) {
		if len(continuity.prepareCalls) != 1 || continuity.commitCalls != 0 {
			t.Fatalf("%s crossed pending boundary with prepare/commit %d/%d", stage, len(continuity.prepareCalls), continuity.commitCalls)
		}
	}
	writer := &firstWriteResponseWriter{
		ResponseWriter: underlying,
		prepareVisible: func(header http.Header) (*codexhttp.Visibility, error) {
			return attempt.PrepareVisible(context.Background(), header)
		},
		commitVisible: func(visibility *codexhttp.Visibility) error {
			return visibility.Commit(context.Background())
		},
		sseGate:    gate,
		sseContext: context.Background(),
	}
	writer.WriteHeader(http.StatusOK)
	if n, err := writer.Write(raw[:split]); err != nil || n != split {
		t.Fatalf("first fragment = (%d, %v), want (%d, nil)", n, err, split)
	}
	writer.Flush()
	if underlying.status != 0 || len(underlying.writtenBytes) != 0 || len(continuity.prepareCalls) != 0 {
		t.Fatalf("incomplete event became visible: status=%d bytes=%d prepares=%d", underlying.status, len(underlying.writtenBytes), len(continuity.prepareCalls))
	}
	if n, err := writer.Write(raw[split:]); err != nil || n != len(raw)-split {
		t.Fatalf("final fragment = (%d, %v)", n, err)
	}
	if underlying.status != http.StatusOK || !bytes.Equal(underlying.writtenBytes, raw) {
		t.Fatalf("wire = status %d body %q", underlying.status, underlying.writtenBytes)
	}
	if continuity.commitCalls != 1 {
		t.Fatalf("response reference commits = %d, want 1", continuity.commitCalls)
	}
}

func TestFirstWriteResponseWriterSSEShortWriteCommitsOnlyVisibleReference(t *testing.T) {
	for _, test := range []struct {
		name       string
		writeN     int
		wantCommit int
	}{
		{name: "partial visible", writeN: 5, wantCommit: 1},
		{name: "zero uncertain", writeN: 0, wantCommit: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			attempt, gate, continuity := newPreCommitSSEGate(t)
			raw := []byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"response-ref\"}}\n\n")
			underlying := &preCommitTestWriter{writeN: test.writeN}
			writer := &firstWriteResponseWriter{
				ResponseWriter: underlying,
				prepareVisible: func(header http.Header) (*codexhttp.Visibility, error) {
					return attempt.PrepareVisible(context.Background(), header)
				},
				commitVisible: func(visibility *codexhttp.Visibility) error {
					return visibility.Commit(context.Background())
				},
				sseGate: gate,
			}
			n, err := writer.Write(raw)
			if !errors.Is(err, io.ErrShortWrite) || n != test.writeN {
				t.Fatalf("Write = (%d, %v), want (%d, short write)", n, err, test.writeN)
			}
			if len(continuity.prepareCalls) != 1 || continuity.commitCalls != test.wantCommit {
				t.Fatalf("prepare/commit = %d/%d, want 1/%d", len(continuity.prepareCalls), continuity.commitCalls, test.wantCommit)
			}
			if !bytes.Equal(underlying.writtenBytes, raw[:test.writeN]) {
				t.Fatalf("short wire = %q", underlying.writtenBytes)
			}
		})
	}
}

func TestFirstWriteResponseWriterSSEDiscardKeepsIncompleteReferenceLocal(t *testing.T) {
	_, gate, continuity := newPreCommitSSEGate(t)
	underlying := &preCommitTestWriter{writeN: -1}
	writer := &firstWriteResponseWriter{ResponseWriter: underlying, sseGate: gate}
	partial := []byte("data: {\"type\":\"response.created\",\"response\":{\"id\":\"local-only\"}}")
	if n, err := writer.Write(partial); err != nil || n != len(partial) {
		t.Fatalf("Write = (%d, %v)", n, err)
	}
	writer.DiscardBufferedSSE()
	if len(underlying.writtenBytes) != 0 || len(continuity.prepareCalls) != 0 || continuity.commitCalls != 0 {
		t.Fatalf("discard leaked state: bytes=%d prepare=%d commit=%d", len(underlying.writtenBytes), len(continuity.prepareCalls), continuity.commitCalls)
	}
}

func TestFirstWriteResponseWriterRejectsOversizedIncompleteSSEEvent(t *testing.T) {
	_, gate, _ := newPreCommitSSEGate(t)
	underlying := &preCommitTestWriter{writeN: -1}
	writer := &firstWriteResponseWriter{ResponseWriter: underlying, sseGate: gate}
	payload := bytes.Repeat([]byte("x"), responseanalysis.MaxDecodedEventBytes+1)
	n, err := writer.Write(payload)
	if n != 0 || !errors.Is(err, codexhttp.ErrSSEEventTooLarge) {
		t.Fatalf("Write = (%d, %v), want (0, event too large)", n, err)
	}
	if len(underlying.writtenBytes) != 0 || gate.BufferedBytes() != 0 {
		t.Fatalf("oversized event leaked: physical=%d buffered=%d", len(underlying.writtenBytes), gate.BufferedBytes())
	}
}

func TestSSESettlementUsesPhysicalWriterVisibility(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/responses", nil)
	pending := &pendingHTTPResponse{
		head:              upstreamtransport.ResponseHead{StatusCode: http.StatusOK},
		media:             responseanalysis.ResolveResponseMedia("text/event-stream", nil),
		writer:            &firstWriteResponseWriter{sseGate: &codexhttp.SSEGate{}},
		pctx:              &proxyContext{r: request, startTime: time.Now()},
		analysisStartedAt: time.Now(),
	}
	completion := responseanalysis.Completion{
		HeadersCommitted: true, ClientBodyBytesWritten: 4096, UpstreamBytesRead: 8192,
		Termination: responseanalysis.TerminationUpstreamReadFailure,
	}
	result := pending.resultFromCompletion(completion)
	if result.headersWritten || result.responseCommitted || result.firstByteVisible || result.responseBytes != 0 || result.upstreamBytes != 8192 {
		t.Fatalf("logical commit escaped the SSE gate: %#v", result)
	}

	pending.wireBytesRead = func() int64 { return 1024 }
	pending.writer.committed = true
	result = pending.resultFromCompletion(completion)
	if !result.headersWritten || !result.responseCommitted || result.upstreamBytes != 1024 {
		t.Fatalf("physical commit was lost: %#v", result)
	}
}

type preCommitContinuity struct {
	prepareCalls []codexcontinuity.ClaimRequest
	commitCalls  int
}

func (*preCommitContinuity) ResolveOwner(context.Context, codexcontinuity.ResolveRequest) (codexcontinuity.Binding, error) {
	return codexcontinuity.Binding{}, &codexcontinuity.Error{Kind: codexcontinuity.ErrorUnknown}
}

func (*preCommitContinuity) AcquireExisting(context.Context, codexcontinuity.ValidateRequest) (codexcontinuity.Lease, error) {
	return codexcontinuity.Lease{}, &codexcontinuity.Error{Kind: codexcontinuity.ErrorUnknown}
}

func (*preCommitContinuity) Claim(context.Context, codexcontinuity.ClaimRequest) (codexcontinuity.Lease, error) {
	return codexcontinuity.Lease{}, nil
}

func (*preCommitContinuity) Adopt(context.Context, codexcontinuity.ClaimRequest) (codexcontinuity.Lease, error) {
	return codexcontinuity.Lease{}, nil
}

func (c *preCommitContinuity) PrepareVisible(_ context.Context, request codexcontinuity.ClaimRequest) (codexcontinuity.Lease, error) {
	c.prepareCalls = append(c.prepareCalls, request)
	return codexcontinuity.Lease{}, nil
}

func (c *preCommitContinuity) Commit(context.Context, codexcontinuity.Lease) (codexcontinuity.Binding, error) {
	c.commitCalls++
	return codexcontinuity.Binding{}, nil
}

func (*preCommitContinuity) AbandonBeforeDisclosure(context.Context, codexcontinuity.Lease) error {
	return nil
}

func TestFirstWriteResponseWriter(t *testing.T) {
	var callCount int
	recorder := httptest.NewRecorder()

	w := &firstWriteResponseWriter{
		ResponseWriter: recorder,
		onFirstWrite: func() {
			callCount++
		},
	}

	n, err := w.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 5 {
		t.Errorf("expected 5 bytes written, got %d", n)
	}
	if callCount != 1 {
		t.Errorf("expected callback to be called once, got %d", callCount)
	}

	n, err = w.Write([]byte("world"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 5 {
		t.Errorf("expected 5 bytes written, got %d", n)
	}
	if callCount != 1 {
		t.Errorf("expected callback to still be 1, got %d", callCount)
	}

	if recorder.Body.String() != "helloworld" {
		t.Errorf("expected body 'helloworld', got %q", recorder.Body.String())
	}
}

func TestFirstWriteResponseWriter_Flush(t *testing.T) {
	recorder := httptest.NewRecorder()
	w := &firstWriteResponseWriter{
		ResponseWriter: recorder,
		onFirstWrite:   func() {},
	}

	_, _ = w.Write([]byte("data: test\n\n"))
	w.Flush()

	if !recorder.Flushed {
		t.Error("expected Flush to be called on underlying ResponseWriter")
	}
}

func TestFirstWriteResponseWriter_NilCallback(t *testing.T) {
	recorder := httptest.NewRecorder()
	w := &firstWriteResponseWriter{
		ResponseWriter: recorder,
	}

	n, err := w.Write([]byte("test"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 4 {
		t.Errorf("expected 4 bytes written, got %d", n)
	}
}

type preCommitScopeDigester struct{ scope codexidentity.ClientScope }

func (d preCommitScopeDigester) ClientScope([]byte) (codexidentity.ClientScope, error) {
	return d.scope, nil
}

func (d preCommitScopeDigester) ClientScopeCandidates([]byte) ([]codexidentity.ClientScope, error) {
	return []codexidentity.ClientScope{d.scope}, nil
}

func newPreCommitSSEGate(t *testing.T) (*codexhttp.Attempt, *codexhttp.SSEGate, *preCommitContinuity) {
	t.Helper()
	clientDigest := sha256.Sum256([]byte("client"))
	clientScope, err := codexidentity.ClientScopeFromDigest("test-v1", clientDigest)
	if err != nil {
		t.Fatal(err)
	}
	credentialDigest := sha256.Sum256([]byte("provider"))
	credentialSubject, err := credentialsession.KeyedDigestSubject("test-v1", credentialDigest[:])
	if err != nil {
		t.Fatal(err)
	}
	finalURL := &url.URL{Scheme: "https", Host: "provider.test", Path: "/v1/responses"}
	candidate, err := codexidentity.NewAuthorityResolver().Resolve(credentialsession.RouteSnapshot{
		RouteTargetID: "route-sse",
		APIType:       APITypeCodex,
		VendorScope:   "openai",
		Credential: credentialsession.Snapshot{
			SessionID: "session-sse", Kind: credentialsession.KindAPIKey,
			SecretData: "provider-secret", Version: 1, Subject: credentialSubject,
			AuthState: credentialsession.AuthState{Status: credentialsession.AuthStatusActive},
		},
	}, APITypeCodex, finalURL)
	if err != nil {
		t.Fatal(err)
	}
	appliedSubject, err := codexidentity.CredentialSubjectFromSession(credentialSubject)
	if err != nil {
		t.Fatal(err)
	}
	applied, err := codexidentity.AppliedIdentityFromRequest("openai", finalURL, appliedSubject)
	if err != nil {
		t.Fatal(err)
	}
	continuity := &preCommitContinuity{}
	fixture := newProxyCodexFixture(t)
	runtime, err := codexhttp.New(codexhttp.Config{
		ClientScopes:    preCommitScopeDigester{scope: clientScope},
		Continuity:      continuity,
		ProviderCookies: fixture.providerCookies,
		ExternalScheme:  fixture.externalScheme,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "http://gateway.test/codex/v1/responses", nil)
	request.Header.Set("Authorization", "Bearer client-secret")
	request.Header.Set("Thread-Id", "precommit-request-anchor")
	operation, err := runtime.Begin(context.Background(), request, APITypeCodex, "operation-sse", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewRequest(http.MethodPost, finalURL.String(), nil)
	attempt, err := operation.PrepareAttempt(context.Background(), upstream, candidate, applied)
	if err != nil {
		t.Fatal(err)
	}
	gate := attempt.NewSSEGate(responseanalysis.MaxDecodedEventBytes)
	if gate == nil {
		t.Fatal("expected SSE gate")
	}
	return attempt, gate, continuity
}

type oneByteReader struct{ sent bool }

func (r *oneByteReader) Read(payload []byte) (int, error) {
	if r.sent {
		return 0, io.EOF
	}
	r.sent = true
	payload[0] = 'x'
	return 1, nil
}
