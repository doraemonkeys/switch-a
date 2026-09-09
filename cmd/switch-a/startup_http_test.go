package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

const (
	composedServerReadyTimeout = 5 * time.Second
	composedServerPollInterval = 10 * time.Millisecond
	composedRequestTimeout     = time.Second
)

type composedHTTPServer interface {
	Start() error
	Shutdown(context.Context) error
	Addr() string
}

type composedHTTPClient struct {
	baseURL string
	client  *http.Client
}

type composedHTTPResponse struct {
	status int
	body   []byte
}

func startComposedHTTPServer(t *testing.T, server composedHTTPServer) *composedHTTPClient {
	t.Helper()
	client := &composedHTTPClient{
		client: &http.Client{
			Transport: http.DefaultTransport.(*http.Transport).Clone(),
			Timeout:   composedRequestTimeout,
		},
	}
	errCh := make(chan error, 1)
	go func() { errCh <- server.Start() }()
	t.Cleanup(func() {
		// The fixture owns its pool, including unused speculative connections.
		// Release them before Shutdown, which otherwise waits for StateNew sockets.
		client.client.CloseIdleConnections()
		ctx, cancel := context.WithTimeout(context.Background(), composedServerReadyTimeout)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			t.Errorf("shutdown composed HTTP server %s: %v", server.Addr(), err)
		}
		select {
		case err := <-errCh:
			if err != nil {
				t.Errorf("composed HTTP server %s stopped: %v", server.Addr(), err)
			}
		case <-time.After(composedServerReadyTimeout):
			t.Errorf("composed HTTP server %s did not stop", server.Addr())
		}
	})

	deadline := time.Now().Add(composedServerReadyTimeout)
	for time.Now().Before(deadline) {
		_, port, err := net.SplitHostPort(server.Addr())
		if err == nil && port != "0" {
			client.baseURL = "http://127.0.0.1:" + port
			response, requestErr := client.client.Get(client.baseURL + "/health")
			if requestErr == nil {
				// Reading to EOF lets the next request reuse the health connection.
				_, readErr := io.Copy(io.Discard, response.Body)
				closeErr := response.Body.Close()
				if readErr == nil && closeErr == nil && response.StatusCode == http.StatusOK {
					return client
				}
			}
		}
		time.Sleep(composedServerPollInterval)
	}
	t.Fatal("composed HTTP server did not become ready")
	return nil
}

func (c *composedHTTPClient) request(t *testing.T, method, path, token string, payload any) composedHTTPResponse {
	t.Helper()
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(t.Context(), method, c.baseURL+path, body)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return composedHTTPResponse{status: response.StatusCode, body: responseBody}
}

func TestComposedHTTPClientClosesConnectionsBeforeServerShutdown(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	const probePath = "/probe"
	probeConnections := make(chan string, 1)
	closedConnections := make(chan string, 4)
	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == probePath {
				probeConnections <- r.RemoteAddr
			}
			_, _ = w.Write([]byte("ready"))
		}),
		ConnState: func(conn net.Conn, state http.ConnState) {
			if state == http.StateClosed {
				closedConnections <- conn.RemoteAddr().String()
			}
		},
	}
	t.Cleanup(func() { _ = server.Close() })
	fixture := &composedHTTPShutdownProbe{
		server: server, listener: listener,
		beforeShutdown: func(ctx context.Context) error {
			var probeConnection string
			select {
			case probeConnection = <-probeConnections:
			case <-ctx.Done():
				return fmt.Errorf("probe request was not observed: %w", ctx.Err())
			}
			for {
				select {
				case closed := <-closedConnections:
					if closed == probeConnection {
						return nil
					}
				case <-ctx.Done():
					return fmt.Errorf("test client connection remained open before server shutdown: %w", ctx.Err())
				}
			}
		},
	}
	client := startComposedHTTPServer(t, fixture)
	response := client.request(t, http.MethodGet, probePath, "", nil)
	if response.status != http.StatusOK || string(response.body) != "ready" {
		t.Fatalf("probe response = %+v", response)
	}
}

type composedHTTPShutdownProbe struct {
	server         *http.Server
	listener       net.Listener
	beforeShutdown func(context.Context) error
}

func (s *composedHTTPShutdownProbe) Start() error {
	err := s.server.Serve(s.listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *composedHTTPShutdownProbe) Shutdown(ctx context.Context) error {
	return errors.Join(s.beforeShutdown(ctx), s.server.Shutdown(ctx))
}

func (s *composedHTTPShutdownProbe) Addr() string { return s.listener.Addr().String() }
