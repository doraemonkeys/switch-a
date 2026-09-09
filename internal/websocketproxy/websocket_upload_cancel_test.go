package websocketproxy

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

type blockedUploadConnection struct {
	net.Conn
	armed       chan struct{}
	entered     chan struct{}
	closed      chan struct{}
	once        sync.Once
	enteredOnce sync.Once
}

func (c *blockedUploadConnection) Write(p []byte) (int, error) {
	select {
	case <-c.armed:
		c.enteredOnce.Do(func() { close(c.entered) })
		<-c.closed
		return 0, net.ErrClosed
	default:
		return c.Conn.Write(p)
	}
}
func (c *blockedUploadConnection) Close() error {
	c.once.Do(func() { close(c.closed) })
	return c.Conn.Close()
}

func TestPreVisibleReadFailureCancelsBlockedUpload(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
	defer cancel()
	accepted := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		accepted <- conn
	}))
	defer server.Close()
	var wire *blockedUploadConnection
	transport := &http.Transport{DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
		if err != nil {
			return nil, err
		}
		wire = &blockedUploadConnection{Conn: conn, armed: make(chan struct{}), entered: make(chan struct{}), closed: make(chan struct{})}
		return wire, nil
	}}
	defer transport.CloseIdleConnections()
	upstream, _, err := websocket.Dial(ctx, server.URL, &websocket.DialOptions{HTTPClient: &http.Client{Transport: transport}})
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.CloseNow()
	peer := <-accepted
	defer peer.CloseNow()
	close(wire.armed)
	clientReads := make(chan webSocketInitialReadResult, 1)
	clientReads <- webSocketInitialReadResult{messageType: websocket.MessageBinary, data: bytes.Repeat([]byte("x"), 64*1024)}
	upstreamReads := make(chan webSocketInitialReadResult, 1)
	original := errors.New("upstream read failed before upload finished")
	done := make(chan webSocketPreVisibleRelayProgress, 1)
	lifecycle := newWebSocketLifecycleState()
	lifecycle.MarkClientAccepted()
	go func() {
		done <- (&WebSocketForwarder{}).relayPreVisibleWindow(ctx, ctx, nil, upstream,
			(webSocketRelayOptions{PreVisibleReplayBuffer: newPreVisibleClientMessageBuffer(128 * 1024)}).withCaptureHooks(),
			lifecycle, newWebSocketClientReadHandoff(clientReads), upstreamReads, nil, nil, nil, newWebSocketCommitState())
	}()
	select {
	case <-wire.entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	upstreamReads <- webSocketInitialReadResult{err: original}
	select {
	case progress := <-done:
		if progress.Result == nil || !errors.Is(progress.Result.Err, original) || progress.Result.FailureOperation != webSocketRelayFailureOperationRead {
			t.Fatalf("lost initial upstream read failure: %#v", progress.Result)
		}
		if progress.BytesClientToUpstream != 0 {
			t.Fatal("partial upload counted as complete")
		}
		if ctx.Err() != nil {
			t.Fatal("upload cancellation escaped into the client session")
		}
	case <-time.After(time.Second):
		cancel()
		<-done
		t.Fatal("upstream read failure did not cancel the blocked upload promptly")
	}
}
