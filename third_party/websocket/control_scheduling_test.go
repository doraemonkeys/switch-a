//go:build !js

package websocket

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"sync"
	"testing"
	"time"
)

type stalledFrameWriter struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
	bytes.Buffer
}

func (w *stalledFrameWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.entered); <-w.release })
	return w.Buffer.Write(p)
}
func (w *stalledFrameWriter) Read([]byte) (int, error) { return 0, io.EOF }
func (w *stalledFrameWriter) Close() error             { return nil }

func TestPongWaitDoesNotConsumeSendBudget(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	wire := &stalledFrameWriter{entered: make(chan struct{}), release: make(chan struct{})}
	conn := newConn(connConfig{rwc: wire, br: bufio.NewReader(bytes.NewReader([]byte("p"))), bw: bufio.NewWriter(wire)})
	defer conn.CloseNow()
	dataDone := make(chan error, 1)
	go func() {
		_, err := conn.writeFrame(ctx, false, false, opBinary, bytes.Repeat([]byte("x"), 16*1024))
		dataDone <- err
	}()
	<-wire.entered
	pongDone := make(chan error, 1)
	go func() { pongDone <- conn.handleControl(ctx, header{fin: true, opcode: opPing, payloadLength: 1}) }()
	// A data frame cannot be preempted once its header has gone onto the wire.
	// Waiting for its boundary must not exhaust the Pong's own write budget.
	var early error
	select {
	case early = <-pongDone:
	case <-time.After(5200 * time.Millisecond):
	}
	close(wire.release)
	if err := <-dataDone; err != nil {
		t.Fatal(err)
	}
	if early != nil {
		t.Fatalf("Pong failed solely while waiting for a data frame: %v", early)
	}
	select {
	case err := <-pongDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if !bytes.HasSuffix(wire.Bytes(), []byte{0x8a, 1, 'p'}) {
		t.Fatal("Pong payload was not written intact")
	}
}
