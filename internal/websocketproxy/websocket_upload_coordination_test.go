package websocketproxy

import (
	"context"
	"errors"
	"github.com/coder/websocket"
	"io"
	"testing"
	"time"
)

func TestConcurrentReadRetainsResponseWithoutCancelingUpload(t *testing.T) {
	reads := make(chan webSocketInitialReadResult)
	started := make(chan struct{})
	finished := make(chan struct{})
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	go func() {
		<-started
		reads <- webSocketInitialReadResult{messageType: websocket.MessageText, data: []byte("response")}
		close(finished)
	}()
	result, read := withWebSocketConcurrentRead(ctx, reads, "test", func(writeCtx context.Context) error {
		close(started)
		<-finished
		return writeCtx.Err()
	})
	if result != nil || read == nil || string(read.data) != "response" {
		t.Fatalf("upload=%v read=%#v", result, read)
	}
	retained := retainedWebSocketRead(*read)
	if got := <-retained; string(got.data) != "response" {
		t.Fatalf("handoff=%#v", got)
	}
}

func TestConcurrentReadPreservesWriteFailureAndUnreadResponse(t *testing.T) {
	reads := make(chan webSocketInitialReadResult, 1)
	original := errors.New("write failed")
	result, read := withWebSocketConcurrentRead(t.Context(), reads, "test", func(context.Context) error { return original })
	if !errors.Is(result, original) || read != nil {
		t.Fatalf("upload=%v read=%#v", result, read)
	}
	reads <- webSocketInitialReadResult{data: []byte("later response")}
	if string((<-reads).data) != "later response" {
		t.Fatal("read ownership changed")
	}
}

func TestConcurrentReadClosedChannelCancelsAndJoinsUpload(t *testing.T) {
	reads := make(chan webSocketInitialReadResult)
	close(reads)
	stopped := false
	result, read := withWebSocketConcurrentRead(t.Context(), reads, "test", func(ctx context.Context) error {
		<-ctx.Done()
		stopped = true
		return ctx.Err()
	})
	if !stopped || !errors.Is(result, context.Canceled) || read == nil || !errors.Is(read.err, io.ErrUnexpectedEOF) {
		t.Fatalf("upload=%v read=%#v stopped=%v", result, read, stopped)
	}
}

func TestConcurrentReadParentCancellationStopsUpload(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	result, read := withWebSocketConcurrentRead(ctx, nil, "test", func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})
	if !errors.Is(result, context.Canceled) || read != nil {
		t.Fatalf("upload=%v read=%#v", result, read)
	}
}
