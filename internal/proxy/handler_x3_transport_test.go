package proxy

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/upstreamtransport"
)

func TestX3CompressedCodexSSEUsesOneDecodedDownstreamRepresentation(t *testing.T) {
	decoded := []byte(
		"event: response.created\ndata: {\"type\":\"response.created\"}\n\n" +
			"event: response.completed\ndata: {\"type\":\"response.completed\"}\n\n",
	)
	wire := x3Gzip(t, decoded)
	events := &x3EventLog{}
	provider := x3Provider("p1")
	lease := x3NewLease(provider, events)
	selection := &x3Selector{initial: provider, initialLease: lease, events: events}
	body := x3NewTrackedBody(wire, "close:gzip-sse", events)
	step := x3HTTPResponseStep(http.StatusOK, "text/event-stream", "gzip", body, len(wire))
	step.header.Set("Content-Length", strconv.Itoa(len(wire)))
	step.header.Set("ETag", "encoded-validator")
	step.header.Set("Digest", "sha-256=encoded")
	step.onRequest = func(request *http.Request) {
		if got := request.Header.Get("Accept-Encoding"); got != "identity" {
			t.Errorf("upstream Accept-Encoding = %q, want identity", got)
		}
	}
	transport := &x3ScriptedTransport{events: events, steps: []x3TransportStep{step}}
	rules := x3CompiledRuleSet(t, 45, x3RetryThenSwitchAction(t, 0), "never-match")
	recorder, _ := x3Execute(t, x3ExecutionConfig{
		providers: []*model.Provider{provider}, selector: selection, transport: transport,
		rules: &x3RuleProvider{current: rules}, analyzer: x3AnalyzerSpyForTest(t), health: newX3Health(),
		stats: &x3RuleStats{}, globalMaxAttempts: 1,
	})

	if recorder.Code != http.StatusOK || !bytes.Equal(recorder.Body.Bytes(), decoded) {
		t.Fatalf("status=%d body=%q want=%q", recorder.Code, recorder.Body.Bytes(), decoded)
	}
	for _, name := range []string{"Content-Encoding", "Content-Length", "ETag", "Digest"} {
		if got := recorder.Header().Get(name); got != "" {
			t.Errorf("downstream %s = %q, want absent", name, got)
		}
	}
	if recorder.Header().Get("Content-Type") != "text/event-stream" || !recorder.Flushed {
		t.Fatalf("downstream header=%#v flushed=%v", recorder.Header(), recorder.Flushed)
	}
	if body.CloseCount() != 1 || lease.ReleaseCount() != 1 {
		t.Fatalf("body closes=%d lease releases=%d", body.CloseCount(), lease.ReleaseCount())
	}
}

type x3TransportStep struct {
	statusCode int
	header     http.Header
	body       io.ReadCloser
	wireBytes  int
	disclosure upstreamtransport.RequestDisclosure
	err        error
	onFetch    func()
	onRequest  func(*http.Request)
}

func x3HTTPResponseStep(
	statusCode int,
	contentType string,
	contentEncoding string,
	body io.ReadCloser,
	wireBytes int,
) x3TransportStep {
	header := make(http.Header)
	if contentType != "" {
		header.Set("Content-Type", contentType)
	}
	if contentEncoding != "" {
		header.Set("Content-Encoding", contentEncoding)
	}
	return x3TransportStep{
		statusCode: statusCode, header: header, body: body, wireBytes: wireBytes,
		disclosure: upstreamtransport.RequestDisclosureConfirmed,
	}
}

type x3ScriptedTransport struct {
	mu     sync.Mutex
	steps  []x3TransportStep
	next   int
	events *x3EventLog
}

func (t *x3ScriptedTransport) FetchUpstream(
	ctx context.Context,
	request *http.Request,
	_ upstreamtransport.ExecutionPolicy,
) (*upstreamtransport.Response, upstreamtransport.RequestDisclosure, error) {
	if err := ctx.Err(); err != nil {
		return nil, upstreamtransport.RequestDisclosureNone, err
	}
	t.mu.Lock()
	if t.next >= len(t.steps) {
		t.mu.Unlock()
		return nil, upstreamtransport.RequestDisclosureUnknown, errors.New("unexpected upstream fetch")
	}
	step := t.steps[t.next]
	t.next++
	t.mu.Unlock()
	t.events.Add("fetch:" + request.URL.Host)
	if step.onRequest != nil {
		step.onRequest(request)
	}
	if step.onFetch != nil {
		step.onFetch()
	}
	if step.err != nil {
		return nil, step.disclosure, step.err
	}
	header := step.header.Clone()
	response, err := upstreamtransport.NewResponse(upstreamtransport.ResponseHead{
		StatusCode: step.statusCode, Protocol: "HTTP/1.1", SourceHeader: header.Clone(), Header: header,
		ContentLength: int64(step.wireBytes),
	}, step.body)
	return response, step.disclosure, err
}

func (t *x3ScriptedTransport) Count() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.next
}

type x3TrackedBody struct {
	reader *bytes.Reader
	label  string
	events *x3EventLog
	closes atomic.Int32
}

func x3NewTrackedBody(payload []byte, label string, events *x3EventLog) *x3TrackedBody {
	return &x3TrackedBody{reader: bytes.NewReader(payload), label: label, events: events}
}

func (b *x3TrackedBody) Read(payload []byte) (int, error) { return b.reader.Read(payload) }
func (b *x3TrackedBody) Close() error {
	b.closes.Add(1)
	b.events.Add(b.label)
	return nil
}
func (b *x3TrackedBody) CloseCount() int { return int(b.closes.Load()) }

type x3BlockingBody struct {
	mu        sync.Mutex
	payload   []byte
	offset    int
	closed    chan struct{}
	closeOnce sync.Once
	label     string
	events    *x3EventLog
	closes    atomic.Int32
}

func x3NewBlockingBody(payload []byte, label string, events *x3EventLog) *x3BlockingBody {
	return &x3BlockingBody{payload: append([]byte(nil), payload...), closed: make(chan struct{}), label: label, events: events}
}

func (b *x3BlockingBody) Read(target []byte) (int, error) {
	b.mu.Lock()
	if b.offset < len(b.payload) {
		count := copy(target, b.payload[b.offset:])
		b.offset += count
		b.mu.Unlock()
		return count, nil
	}
	closed := b.closed
	b.mu.Unlock()
	<-closed
	return 0, io.EOF
}

func (b *x3BlockingBody) Close() error {
	b.closes.Add(1)
	b.events.Add(b.label)
	b.closeOnce.Do(func() { close(b.closed) })
	return nil
}

func (b *x3BlockingBody) CloseCount() int { return int(b.closes.Load()) }

func x3Gzip(t *testing.T, payload []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	if _, err := writer.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
