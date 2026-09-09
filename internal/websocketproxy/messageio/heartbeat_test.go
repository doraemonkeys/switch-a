package messageio

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
)

type slowUploadConn struct{ net.Conn }

func (c slowUploadConn) Write(p []byte) (int, error) {
	// Delay each physical upload write, reproducing backpressure without
	// relying on external servers, proxies or the machine's socket buffer size.
	time.Sleep(5 * time.Millisecond)
	return c.Conn.Write(p)
}

func TestHeartbeatDuringSlowLargeMessage(t *testing.T) {
	for _, fragmented := range []bool{false, true} {
		name := "single_frame_control"
		if fragmented {
			name = "fragmented"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			accepted := make(chan *websocket.Conn, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := websocket.Accept(w, r, nil)
				if err != nil {
					t.Error(err)
					return
				}
				conn.SetReadLimit(8 * 1024 * 1024)
				accepted <- conn
				<-ctx.Done()
				_ = conn.CloseNow()
			}))
			t.Cleanup(server.Close)
			transport := &http.Transport{DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
				if err != nil {
					return nil, err
				}
				return slowUploadConn{conn}, nil
			}}
			t.Cleanup(transport.CloseIdleConnections)
			client, _, err := websocket.Dial(ctx, server.URL, &websocket.DialOptions{HTTPClient: &http.Client{Transport: transport}})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = client.CloseNow() }()
			client.CloseRead(ctx)
			var upstream *websocket.Conn
			select {
			case upstream = <-accepted:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			payload := bytes.Repeat([]byte("payload-"), 512*1024)
			started := make(chan struct{})
			readDone := make(chan error, 1)
			go func() {
				typ, reader, readErr := upstream.Reader(ctx)
				close(started)
				if readErr != nil {
					readDone <- readErr
					return
				}
				got, readErr := io.ReadAll(reader)
				if readErr == nil && (typ != websocket.MessageBinary || !bytes.Equal(got, payload)) {
					readErr = io.ErrUnexpectedEOF
				}
				readDone <- readErr
			}()
			writeDone := make(chan error, 1)
			go func() {
				if fragmented {
					writeDone <- Write(ctx, client, websocket.MessageBinary, payload)
				} else {
					writeDone <- client.Write(ctx, websocket.MessageBinary, payload)
				}
			}()
			select {
			case <-started:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			pingContext, stopPing := context.WithTimeout(ctx, time.Second)
			pingErr := upstream.Ping(pingContext)
			stopPing()
			if !fragmented {
				if pingErr == nil {
					t.Fatal("negative control unexpectedly answered Ping during a single large frame")
				}
				cancel()
				return
			}
			if pingErr != nil {
				t.Fatalf("heartbeat blocked by large upload: %v", pingErr)
			}
			select {
			case <-writeDone:
				t.Fatal("heartbeat only completed after the large message")
			default:
			}
			if err := <-writeDone; err != nil {
				t.Fatal(err)
			}
			if err := <-readDone; err != nil {
				t.Fatal(err)
			}
		})
	}
}
