//go:build !js

package websocket

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

func TestControlPhysicalWriteStillTimesOut(t *testing.T) {
	local, remote := net.Pipe()
	defer remote.Close()
	conn := newConn(connConfig{rwc: local, br: bufio.NewReader(local), bw: bufio.NewWriter(local)})
	defer conn.CloseNow()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := time.Now()
	err := conn.writeControl(ctx, opPong, []byte("ping"))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("stalled control write error = %v", err)
	}
	if ctx.Err() != nil {
		t.Fatal("physical control deadline canceled its caller")
	}
	if elapsed := time.Since(started); elapsed < controlFrameIOTimeout || elapsed > 2*controlFrameIOTimeout {
		t.Fatalf("control write deadline duration = %s", elapsed)
	}
}

func TestControlCallerDeadlineBoundsFrameWait(t *testing.T) {
	wire := &stalledFrameWriter{entered: make(chan struct{}), release: make(chan struct{})}
	conn := newConn(connConfig{rwc: wire, br: bufio.NewReader(bytes.NewReader(nil)), bw: bufio.NewWriter(wire)})
	defer conn.CloseNow()
	dataDone := make(chan error, 1)
	go func() {
		_, err := conn.writeFrame(context.Background(), false, false, opBinary, bytes.Repeat([]byte("x"), 16*1024))
		dataDone <- err
	}()
	<-wire.entered
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := conn.writeControl(ctx, opPong, []byte("ping"))
	close(wire.release)
	if dataErr := <-dataDone; dataErr != nil {
		t.Fatal(dataErr)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("caller deadline not honored: %v", err)
	}
}
