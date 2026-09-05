package clientconnection

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type failedListener struct{}

func (failedListener) Accept() (net.Conn, error) { return nil, net.ErrClosed }
func (failedListener) Close() error              { return nil }
func (failedListener) Addr() net.Addr            { return nil }

type nestedConnection struct{ net.Conn }

func (c nestedConnection) NetConn() net.Conn { return c.Conn }

func TestConnectionDeadlineAndContextBoundaries(t *testing.T) {
	local, peer := net.Pipe()
	observed := Wrap(local)
	defer peer.Close()
	defer observed.Close()
	if n, err := observed.Read(nil); n != 0 || err != nil {
		t.Fatalf("empty read=%d,%v", n, err)
	}
	if err := observed.SetDeadline(time.Now().Add(time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, err := observed.Read(make([]byte, 1)); err == nil {
		t.Fatal("read did not time out")
	}
	var timeout readDeadlineError
	if timeout.Error() == "" || !timeout.Timeout() || !timeout.Temporary() || !errors.Is(timeout, context.DeadlineExceeded) {
		t.Fatal("deadline classification")
	}
	if errors.Is(timeout, net.ErrClosed) {
		t.Fatal("deadline classified as closed")
	}
	ctx := Context(t.Context(), nestedConnection{Conn: observed})
	if FromContext(ctx) != observed {
		t.Fatal("nested connection context missing")
	}
	if FromContext(Context(t.Context(), peer)) != nil {
		t.Fatal("ordinary connection acquired observer")
	}
	if err := observed.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := observed.Read(make([]byte, 1)); err == nil {
		t.Fatal("closed reader succeeded")
	}
	if _, err := Listen(failedListener{}).Accept(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("accept=%v", err)
	}
}

func TestOperationCancellationCauseAndClose(t *testing.T) {
	local, peer := net.Pipe()
	observed := Wrap(local)
	defer observed.Close()
	defer peer.Close()
	ctx := Context(t.Context(), observed)
	request := httptest.NewRequest(http.MethodPost, "http://localhost", nil).WithContext(ctx)
	operationContext, operation := Begin(request, httptest.NewRecorder())
	cause := errors.New("gateway source failure")
	operation.Cancel(cause)
	if context.Cause(operationContext) != cause {
		t.Fatal("source cause lost")
	}
	operation.Close()
	operation.Close()
}
