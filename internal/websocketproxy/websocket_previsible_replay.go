package websocketproxy

import (
	"context"
	"errors"
	"sync"

	"github.com/doraemonkeys/switch-a/internal/requestcapture"

	"github.com/coder/websocket"
)

type webSocketReplayMessage struct {
	MessageType websocket.MessageType
	Data        []byte
	Lineage     requestcapture.MessageLineage
	Delivered   bool
	Decision    webSocketPreWriteDecision
}

// preVisibleClientMessageBuffer owns the complete replayability boundary for
// client frames. Keeping the decision beside immutable wire bytes prevents a
// replacement attempt from reclassifying connection-bound controls as replayable.
type preVisibleClientMessageBuffer struct {
	mu         sync.Mutex
	limitBytes int
	totalBytes int
	enabled    bool
	messages   []webSocketReplayMessage
}

type preVisibleClientMessageBufferSnapshot struct {
	Enabled    bool
	TotalBytes int
	Messages   []webSocketReplayMessage
}

const invalidWebSocketReplayMessageIndex = -1

func newPreVisibleClientMessageBuffer(limitBytes int) *preVisibleClientMessageBuffer {
	if limitBytes <= 0 {
		limitBytes = preVisibleClientReplayBufferLimitBytes
	}
	return &preVisibleClientMessageBuffer{
		limitBytes: limitBytes,
		enabled:    true,
	}
}

func (b *preVisibleClientMessageBuffer) Enabled() bool {
	if b == nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.enabled
}

func (b *preVisibleClientMessageBuffer) Disable() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.disableLocked()
}

func (b *preVisibleClientMessageBuffer) Record(messageType websocket.MessageType, data []byte, clientVisible bool) int {
	return b.RecordWithLineage(messageType, data, clientVisible, requestcapture.MessageLineage{})
}

func (b *preVisibleClientMessageBuffer) RecordWithLineage(
	messageType websocket.MessageType,
	data []byte,
	clientVisible bool,
	lineage requestcapture.MessageLineage,
) int {
	return b.RecordDecision(messageType, data, clientVisible, lineage, replayableClientFrameDecision())
}

func (b *preVisibleClientMessageBuffer) RecordDecision(
	messageType websocket.MessageType,
	data []byte,
	clientVisible bool,
	lineage requestcapture.MessageLineage,
	decision webSocketPreWriteDecision,
) int {
	if b == nil {
		return invalidWebSocketReplayMessageIndex
	}

	if clientVisible {
		b.Disable()
		return invalidWebSocketReplayMessageIndex
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.enabled {
		return invalidWebSocketReplayMessageIndex
	}
	if decision.Action != webSocketPreWriteActionForward || !decision.ReplayEligible {
		return invalidWebSocketReplayMessageIndex
	}
	if !isReplayableWebSocketMessageType(messageType) {
		b.disableLocked()
		return invalidWebSocketReplayMessageIndex
	}
	if len(b.messages) >= preVisibleClientReplayBufferLimitMessages {
		b.disableLocked()
		return invalidWebSocketReplayMessageIndex
	}
	nextTotalBytes := b.totalBytes + len(data)
	if nextTotalBytes > b.limitBytes {
		b.disableLocked()
		return invalidWebSocketReplayMessageIndex
	}

	payload := append([]byte(nil), data...)
	b.messages = append(b.messages, webSocketReplayMessage{
		MessageType: messageType,
		Data:        payload,
		Lineage:     lineage,
		Decision:    decision.forReplayStorage(),
	})
	b.totalBytes = nextTotalBytes
	return len(b.messages) - 1
}

func (b *preVisibleClientMessageBuffer) MarkDelivered(index int, lineage requestcapture.MessageLineage) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if index < 0 || index >= len(b.messages) {
		return
	}
	b.messages[index].Delivered = true
	if lineage.Valid() {
		b.messages[index].Lineage = lineage
	}
}

func (b *preVisibleClientMessageBuffer) Replay(ctx context.Context, upstreamConn *websocket.Conn) error {
	if b == nil {
		return nil
	}

	snapshot := b.Snapshot()
	if !snapshot.Enabled {
		return errors.New("pre-visible replay buffer disabled")
	}
	for _, message := range snapshot.Messages {
		if err := upstreamConn.Write(ctx, message.MessageType, message.Data); err != nil {
			return err
		}
	}
	return nil
}

func (b *preVisibleClientMessageBuffer) Snapshot() preVisibleClientMessageBufferSnapshot {
	if b == nil {
		return preVisibleClientMessageBufferSnapshot{}
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	snapshot := preVisibleClientMessageBufferSnapshot{
		Enabled:    b.enabled,
		TotalBytes: b.totalBytes,
		Messages:   make([]webSocketReplayMessage, 0, len(b.messages)),
	}
	for _, message := range b.messages {
		snapshot.Messages = append(snapshot.Messages, webSocketReplayMessage{
			MessageType: message.MessageType,
			Data:        append([]byte(nil), message.Data...),
			Lineage:     message.Lineage,
			Delivered:   message.Delivered,
			Decision:    message.Decision.forReplayStorage(),
		})
	}
	return snapshot
}

func (b *preVisibleClientMessageBuffer) disableLocked() {
	b.enabled = false
	b.totalBytes = 0
	b.messages = nil
}

func replayableClientFrameDecision() webSocketPreWriteDecision {
	return webSocketPreWriteDecision{
		Action:              webSocketPreWriteActionForward,
		ReplayEligible:      true,
		ReplacementEligible: true,
		PrepareReplay:       replayableClientFrameDecision,
	}
}

func (d webSocketPreWriteDecision) forReplayStorage() webSocketPreWriteDecision {
	d.OnWriteConfirmed = nil
	return d
}

func isReplayableWebSocketMessageType(messageType websocket.MessageType) bool {
	return messageType == websocket.MessageText || messageType == websocket.MessageBinary
}
