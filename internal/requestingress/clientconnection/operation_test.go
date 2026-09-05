package clientconnection

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func await(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("operation did not settle")
	}
}

func TestInterruptedInputKeepsResponseAndLaterDisconnectObservable(t *testing.T) {
	ready := make(chan struct{})
	stopped := make(chan struct{})
	canceled := make(chan struct{})
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, operation := Begin(r, w)
		defer operation.Close()
		if err := operation.EnableDuplex(); err != nil {
			t.Error(err)
			return
		}
		readDone := make(chan struct{})
		go func() { _, _ = io.Copy(io.Discard, r.Body); close(readDone) }()
		close(ready)
		operation.Interrupt(errors.New("upstream stopped reading"))
		await(t, readDone)
		_ = r.Body.Close()
		if ctx.Err() != nil {
			t.Errorf("deliberate stop canceled response: %v", ctx.Err())
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: ready\n\n")
		_ = http.NewResponseController(w).Flush()
		close(stopped)
		// There are deliberately no operation/upstream timeouts or later writes.
		<-ctx.Done()
		close(canceled)
	}))
	server.Listener = Listen(server.Listener)
	server.Config.ConnContext = Context
	server.Start()
	defer server.Close()
	conn, err := net.Dial("tcp", server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.WriteString(conn, "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 100000\r\n\r\nprefix")
	if err != nil {
		t.Fatal(err)
	}
	await(t, ready)
	await(t, stopped)
	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, len("data: ready\n\n"))
	if _, err = io.ReadFull(response.Body, buffer); err != nil {
		t.Fatal(err)
	}
	if string(buffer) != "data: ready\n\n" {
		t.Fatalf("response = %q", buffer)
	}
	_ = conn.Close()
	await(t, canceled)
}

func TestKeepAliveAndExpectContinue(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		_, _ = w.Write(body)
	}))
	server.Listener = Listen(server.Listener)
	server.Config.ConnContext = Context
	server.Start()
	defer server.Close()
	conn, err := net.Dial("tcp", server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	reader := bufio.NewReader(conn)
	for i := 0; i < 2; i++ {
		_, _ = io.WriteString(conn, "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 4\r\nExpect: 100-continue\r\n\r\n")
		interim, err := http.ReadResponse(reader, nil)
		if err != nil {
			t.Fatal(err)
		}
		if interim.StatusCode != 100 {
			t.Fatalf("status = %d", interim.StatusCode)
		}
		_, _ = io.WriteString(conn, "body")
		response, err := http.ReadResponse(reader, nil)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		if err != nil || string(body) != "body" {
			t.Fatalf("body=%q err=%v", body, err)
		}
		_ = response.Body.Close()
	}
}

func TestReadDeadlineDoesNotStopSocketObserver(t *testing.T) {
	local, peer := net.Pipe()
	observed := Wrap(local)
	defer observed.Close()
	defer peer.Close()
	_ = observed.SetReadDeadline(time.Now())
	_, err := observed.Read(make([]byte, 1))
	var timeout net.Error
	if !errors.As(err, &timeout) || !timeout.Timeout() {
		t.Fatalf("deadline = %v", err)
	}
	_ = observed.SetReadDeadline(time.Time{})
	go func() { _, _ = io.Copy(peer, strings.NewReader("x")) }()
	b := make([]byte, 1)
	if _, err := observed.Read(b); err != nil || string(b) != "x" {
		t.Fatalf("read=%q %v", b, err)
	}
	_ = peer.Close()
	await(t, observed.Done())
}

func TestHTTP2OperationKeepsStreamCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodPost, "http://localhost", nil).WithContext(ctx)
	request.ProtoMajor = 2
	operationContext, operation := Begin(request, httptest.NewRecorder())
	defer operation.Close()
	if err := operation.EnableDuplex(); err != nil {
		t.Fatal(err)
	}
	operation.Interrupt(errors.New("stop input"))
	if operationContext.Err() != nil {
		t.Fatal("interrupt canceled stream")
	}
	cancel()
	await(t, operationContext.Done())
}
