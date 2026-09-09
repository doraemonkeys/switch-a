package messageio

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/coder/websocket"
)

type testConnection struct {
	small func(context.Context, websocket.MessageType, []byte) error
	open  func(context.Context, websocket.MessageType) (io.WriteCloser, error)
}

func (c testConnection) Write(ctx context.Context, typ websocket.MessageType, p []byte) error {
	return c.small(ctx, typ, p)
}
func (c testConnection) Writer(ctx context.Context, typ websocket.MessageType) (io.WriteCloser, error) {
	return c.open(ctx, typ)
}

type testWriter struct {
	write func([]byte) (int, error)
	close func() error
}

func (w testWriter) Write(p []byte) (int, error) { return w.write(p) }
func (w testWriter) Close() error                { return w.close() }

func TestWritePreservesSmallMessages(t *testing.T) {
	for _, size := range []int{0, 1, maxFragmentBytes} {
		payload := bytes.Repeat([]byte("a"), size)
		sentinel := errors.New("write failed")
		connection := testConnection{small: func(ctx context.Context, typ websocket.MessageType, p []byte) error {
			if ctx != t.Context() || typ != websocket.MessageText || !bytes.Equal(p, payload) {
				t.Fatal("small message changed")
			}
			return sentinel
		}}
		if err := Write(t.Context(), connection, websocket.MessageText, payload); !errors.Is(err, sentinel) {
			t.Fatalf("write error = %v", err)
		}
	}
}

func TestWritePreservesLargeMessageAndClosesOnce(t *testing.T) {
	for _, size := range []int{maxFragmentBytes + 1, maxFragmentBytes * 2, maxFragmentBytes*3 + 7} {
		payload := bytes.Repeat([]byte("b"), size)
		var received bytes.Buffer
		closed := 0
		connection := testConnection{open: func(ctx context.Context, typ websocket.MessageType) (io.WriteCloser, error) {
			if ctx.Err() != nil || typ != websocket.MessageBinary {
				t.Fatal("invalid message context or type")
			}
			return testWriter{
				write: func(p []byte) (int, error) {
					if len(p) > maxFragmentBytes {
						t.Fatal("unbounded frame")
					}
					return received.Write(p)
				},
				close: func() error { closed++; return nil },
			}, nil
		}}
		if err := Write(t.Context(), connection, websocket.MessageBinary, payload); err != nil {
			t.Fatal(err)
		}
		if closed != 1 || !bytes.Equal(received.Bytes(), payload) {
			t.Fatal("message payload or boundary changed")
		}
	}
}

func TestWriteFailureDoesNotCommitPartialMessage(t *testing.T) {
	sentinel := errors.New("transport failed")
	for _, stage := range []string{"open", "write", "short", "close"} {
		t.Run(stage, func(t *testing.T) {
			closes := 0
			connection := testConnection{open: func(ctx context.Context, _ websocket.MessageType) (io.WriteCloser, error) {
				if stage == "open" {
					return nil, sentinel
				}
				return testWriter{
					write: func(p []byte) (int, error) {
						if stage == "write" {
							return 0, sentinel
						}
						if stage == "short" {
							return len(p) - 1, nil
						}
						return len(p), nil
					},
					close: func() error {
						closes++
						if stage != "close" && !errors.Is(ctx.Err(), context.Canceled) {
							t.Error("failed payload must be canceled before closing its writer")
						}
						return sentinel
					},
				}, nil
			}}
			err := Write(t.Context(), connection, websocket.MessageText, make([]byte, maxFragmentBytes+1))
			want := sentinel
			if stage == "short" {
				want = io.ErrShortWrite
			}
			if !errors.Is(err, want) {
				t.Fatalf("error = %v, want %v", err, want)
			}
			if stage != "open" && closes != 1 {
				t.Fatalf("close count = %d", closes)
			}
		})
	}
}
