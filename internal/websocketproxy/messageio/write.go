// Package messageio preserves message boundaries while bounding data-frame size.
package messageio

import (
	"context"
	"fmt"
	"io"

	"github.com/coder/websocket"
)

// Bounded fragments create scheduling boundaries for prioritized control frames.
// They cannot interrupt a frame whose physical transport write is still blocked.
const maxFragmentBytes = 16 * 1024

type Connection interface {
	Write(context.Context, websocket.MessageType, []byte) error
	Writer(context.Context, websocket.MessageType) (io.WriteCloser, error)
}

// Write preserves the message's payload, type and boundary. Fragmentation is
// internal to the transport; only Close confirms that the whole message was sent.
func Write(ctx context.Context, connection Connection, messageType websocket.MessageType, payload []byte) (err error) {
	if len(payload) <= maxFragmentBytes {
		return connection.Write(ctx, messageType, payload)
	}

	writeContext, cancel := context.WithCancel(ctx)
	defer cancel()
	writer, err := connection.Writer(writeContext, messageType)
	if err != nil {
		return fmt.Errorf("open websocket message writer: %w", err)
	}
	for len(payload) > 0 {
		size := min(len(payload), maxFragmentBytes)
		n, writeErr := writer.Write(payload[:size])
		if writeErr == nil && n != size {
			writeErr = io.ErrShortWrite
		}
		if writeErr != nil {
			// A failed fragment must not be closed as a successful truncated
			// message. Cancel before Close so the transport cannot emit its FIN.
			cancel()
			_ = writer.Close()
			return fmt.Errorf("write websocket message fragment: %w", writeErr)
		}
		payload = payload[size:]
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("finish websocket message: %w", err)
	}
	return nil
}
