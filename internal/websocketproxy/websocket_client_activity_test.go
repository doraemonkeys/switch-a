package websocketproxy

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

type observedClientRequest struct {
	clientID string
	headers  http.Header
	at       time.Time
}

type observingDisguiseRepository struct {
	*testDisguiseRepository
	mu       sync.Mutex
	requests []observedClientRequest
	err      error
}

func (r *observingDisguiseRepository) ObserveClient(_ context.Context, clientID string, headers http.Header, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, observedClientRequest{clientID: clientID, headers: headers.Clone(), at: at})
	return r.err
}

func (r *observingDisguiseRepository) observedRequests() []observedClientRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]observedClientRequest(nil), r.requests...)
}

func TestReferenceClientObserverTracksNewTurnsWithOriginalHeaders(t *testing.T) {
	repository := &observingDisguiseRepository{}
	core, traces := observer.New(zap.DebugLevel)
	gateway := &Gateway{disguise: repository, logger: zap.New(core)}
	headers := http.Header{"User-Agent": {"original-client"}}
	observe := gateway.referenceClientObserver(headers, "client", "operation")
	headers.Set("User-Agent", "changed-by-target")
	orchestrator := &WebSocketSessionOrchestrator{
		handler: gateway, codexOperation: testCodexOperation(t), observeClientRequest: observe,
	}
	ctx := context.Background()
	for _, frame := range []struct {
		kind websocket.MessageType
		data string
	}{
		{websocket.MessageText, `{"type":"response.create","input":[]}`},
		{websocket.MessageText, `{"type":"session.update"}`},
		{websocket.MessageBinary, `{"type":"response.create"}`},
		{websocket.MessageText, `{"type":"response.create","input":[]}`},
	} {
		orchestrator.classifyClientFrame(ctx, frame.kind, []byte(frame.data))
	}
	requests := repository.observedRequests()
	if len(requests) != 2 {
		t.Fatalf("non-request messages counted as activity: %+v", requests)
	}
	for _, request := range requests {
		if request.clientID != "client" || request.headers.Get("User-Agent") != "original-client" || request.at.IsZero() {
			t.Fatalf("observation=%+v", request)
		}
	}
	if len(traces.FilterMessage("websocket.client_request_observed").All()) != 2 {
		t.Fatal("missing activity traces")
	}
	repository.err = errors.New("observation unavailable")
	observe(ctx)
	failures := traces.FilterMessage("websocket.client_request_observation_failed").All()
	if len(failures) != 1 || failures[0].ContextMap()["operation_id"] != "operation" || failures[0].ContextMap()["client_identity_id"] != "client" {
		t.Fatalf("failures=%+v", failures)
	}
	if gateway.referenceClientObserver(nil, "", "operation") != nil || (&Gateway{}).referenceClientObserver(nil, "client", "operation") != nil {
		t.Fatal("unavailable observers should be skipped")
	}
}
