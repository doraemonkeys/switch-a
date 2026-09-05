// Package disguise contains HTTP delivery adapters that preserve original
// protocol observations while deriving only the physical client response.
package disguise

import (
	"context"
	"io"
	"sync"

	"github.com/doraemonkeys/switch-a/internal/codex/clientdisguise/wire"
	"github.com/doraemonkeys/switch-a/internal/upstreamtransport"
)

type ResponseStream struct {
	input     *io.PipeWriter
	output    io.ReadCloser
	done      chan struct{}
	closeOnce sync.Once
	err       error
}

// NewResponseStream places conversion after analysis. The pipe bounds retained
// data and joins conversion before the attempt can report a terminal outcome.
func NewResponseStream(ctx context.Context, session *wire.Session, head upstreamtransport.ResponseHead, destination io.Writer) (upstreamtransport.ResponseHead, *ResponseStream, error) {
	reader, writer := io.Pipe()
	derived, body, err := session.RestoreResponse(ctx, head, reader)
	if err != nil {
		_ = reader.Close()
		_ = writer.Close()
		return head, nil, err
	}
	stream := &ResponseStream{input: writer, output: body, done: make(chan struct{})}
	go func() {
		_, stream.err = io.Copy(destination, body)
		_ = body.Close()
		_ = reader.CloseWithError(stream.err)
		close(stream.done)
	}()
	return derived, stream, nil
}

func (s *ResponseStream) Write(payload []byte) (int, error) { return s.input.Write(payload) }
func (s *ResponseStream) Close() error {
	s.closeOnce.Do(func() { _ = s.input.Close() })
	<-s.done
	return s.err
}
func (s *ResponseStream) Abort() {
	s.closeOnce.Do(func() { _ = s.input.CloseWithError(io.ErrClosedPipe) })
	_ = s.output.Close()
	<-s.done
}

type WriterFunc func([]byte) (int, error)

func (f WriterFunc) Write(payload []byte) (int, error) { return f(payload) }
