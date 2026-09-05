package websocketproxy

import (
	"context"
	"fmt"
	"sync"
	"time"
	"unsafe"

	"github.com/coder/websocket"
	"github.com/doraemonkeys/switch-a/internal/requestcapture"
)

type webSocketReplayMessage struct {
	MessageType websocket.MessageType
	Data        []byte
	Lineage     requestcapture.MessageLineage
	Delivered   bool
	Decision    webSocketPreWriteDecision
	payload     *webSocketReplayPayload
}
type webSocketReplayPayload struct {
	data       []byte
	references int
}

type webSocketReplayState string

const (
	webSocketReplayable                webSocketReplayState = "replayable"
	webSocketReplayVisibilityClosed    webSocketReplayState = "visibility_closed"
	webSocketReplayBudgetExhausted     webSocketReplayState = "budget_exhausted"
	webSocketReplayNonReplayableFrame  webSocketReplayState = "non_replayable_frame"
	webSocketReplayParseDegraded       webSocketReplayState = "parse_degraded"
	invalidWebSocketReplayMessageIndex                      = -1
	webSocketReplayDescriptorBytes                          = int(unsafe.Sizeof(webSocketReplayMessage{}))
	// Admission reserves room for one descriptor-only snapshot as well as the
	// original 128-message, 4 MiB payload envelope.
	legacyWebSocketReplayPayloadBytes      = 4 * 1024 * 1024
	legacyWebSocketReplayMessageCount      = 128
	preVisibleClientReplayBufferLimitBytes = legacyWebSocketReplayPayloadBytes + 2*legacyWebSocketReplayMessageCount*webSocketReplayDescriptorBytes
)

// Payloads become immutable at admission. The buffer and snapshot leases share
// them, while each descriptor allocation is accounted independently.
type preVisibleClientMessageBuffer struct {
	mu            sync.Mutex
	limitBytes    int
	totalBytes    int
	messageCount  int
	firstRecorded time.Time
	lastRecorded  time.Time
	now           func() time.Time
	snapshotBytes int
	state         webSocketReplayState
	messages      []webSocketReplayMessage
	onTransition  func(webSocketReplayStatus)
}
type webSocketReplayStatus struct {
	State              webSocketReplayState `json:"state"`
	MessageCount       int                  `json:"message_count"`
	RetainedBytes      int                  `json:"retained_bytes"`
	PayloadBytes       int                  `json:"payload_bytes"`
	SnapshotBytes      int                  `json:"snapshot_bytes"`
	CoverageDurationMs int64                `json:"coverage_duration_ms"`
}
type preVisibleClientMessageBufferSnapshot struct {
	Enabled  bool
	Messages []webSocketReplayMessage
	lease    *webSocketReplaySnapshotLease
}
type webSocketReplaySnapshotLease struct {
	once     sync.Once
	buffer   *preVisibleClientMessageBuffer
	messages []webSocketReplayMessage
}

func (s *preVisibleClientMessageBufferSnapshot) Release() {
	if s == nil || s.lease == nil {
		return
	}
	s.lease.once.Do(func() {
		b := s.lease.buffer
		b.mu.Lock()
		defer b.mu.Unlock()
		b.snapshotBytes -= cap(s.lease.messages) * webSocketReplayDescriptorBytes
		for _, message := range s.lease.messages {
			b.releasePayloadLocked(message.payload)
		}
		s.lease.messages = nil
	})
	s.Messages = nil
}
func newPreVisibleClientMessageBuffer(limitBytes int) *preVisibleClientMessageBuffer {
	if limitBytes <= 0 {
		limitBytes = preVisibleClientReplayBufferLimitBytes
	}
	return &preVisibleClientMessageBuffer{limitBytes: limitBytes, state: webSocketReplayable, now: time.Now}
}
func (b *preVisibleClientMessageBuffer) statusLocked() webSocketReplayStatus {
	return webSocketReplayStatus{State: b.state, MessageCount: b.messageCount,
		RetainedBytes: b.totalBytes + cap(b.messages)*webSocketReplayDescriptorBytes + b.snapshotBytes,
		PayloadBytes:  b.totalBytes, SnapshotBytes: b.snapshotBytes, CoverageDurationMs: b.lastRecorded.Sub(b.firstRecorded).Milliseconds()}
}
func (b *preVisibleClientMessageBuffer) Status() webSocketReplayStatus {
	if b == nil {
		return webSocketReplayStatus{}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.statusLocked()
}
func (b *preVisibleClientMessageBuffer) Enabled() bool {
	return b.Status().State == webSocketReplayable
}
func (b *preVisibleClientMessageBuffer) Disable() { b.CloseReplay(webSocketReplayNonReplayableFrame) }
func (b *preVisibleClientMessageBuffer) CloseReplay(state webSocketReplayState) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closeLocked(state)
}
func (b *preVisibleClientMessageBuffer) Record(messageType websocket.MessageType, data []byte, clientVisible bool) int {
	return b.RecordWithLineage(messageType, data, clientVisible, requestcapture.MessageLineage{})
}
func (b *preVisibleClientMessageBuffer) RecordWithLineage(messageType websocket.MessageType, data []byte, clientVisible bool, lineage requestcapture.MessageLineage) int {
	return b.RecordDecision(messageType, data, clientVisible, lineage, replayableClientFrameDecision())
}
func (b *preVisibleClientMessageBuffer) RecordDecision(messageType websocket.MessageType, data []byte, clientVisible bool, lineage requestcapture.MessageLineage, decision webSocketPreWriteDecision) int {
	if b == nil {
		return invalidWebSocketReplayMessageIndex
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if clientVisible {
		b.closeLocked(webSocketReplayVisibilityClosed)
		return invalidWebSocketReplayMessageIndex
	}
	if b.state != webSocketReplayable {
		return invalidWebSocketReplayMessageIndex
	}
	if decision.Action != webSocketPreWriteActionForward || !decision.ReplayEligible {
		return invalidWebSocketReplayMessageIndex
	}
	if !isReplayableWebSocketMessageType(messageType) {
		b.closeLocked(webSocketReplayNonReplayableFrame)
		return invalidWebSocketReplayMessageIndex
	}
	nextCapacity := cap(b.messages)
	if len(b.messages) == nextCapacity {
		nextCapacity = max(1, 2*nextCapacity)
	}
	// The next replay snapshot must fit too: accepting a prefix that cannot be
	// snapshotted would promise replacement that cannot actually be performed.
	nextCost := b.totalBytes + len(data) + nextCapacity*webSocketReplayDescriptorBytes + b.snapshotBytes + (len(b.messages)+1)*webSocketReplayDescriptorBytes
	if nextCost > b.limitBytes {
		b.closeLocked(webSocketReplayBudgetExhausted)
		return invalidWebSocketReplayMessageIndex
	}
	if nextCapacity != cap(b.messages) {
		messages := make([]webSocketReplayMessage, len(b.messages), nextCapacity)
		copy(messages, b.messages)
		b.messages = messages
	}
	dataCopy := make([]byte, len(data))
	copy(dataCopy, data)
	payload := &webSocketReplayPayload{data: dataCopy, references: 1}
	b.messages = append(b.messages, webSocketReplayMessage{MessageType: messageType, Data: dataCopy, Lineage: lineage, Decision: decision.forReplayStorage(), payload: payload})
	b.totalBytes += cap(dataCopy)
	b.messageCount++
	b.lastRecorded = b.now()
	if b.firstRecorded.IsZero() {
		b.firstRecorded = b.lastRecorded
	}
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
	defer snapshot.Release()
	if !snapshot.Enabled {
		return fmt.Errorf("pre-visible replay unavailable: %s", b.Status().State)
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
	if b.state != webSocketReplayable {
		return preVisibleClientMessageBufferSnapshot{}
	}
	cost := len(b.messages) * webSocketReplayDescriptorBytes
	if b.statusLocked().RetainedBytes+cost > b.limitBytes {
		b.closeLocked(webSocketReplayBudgetExhausted)
		return preVisibleClientMessageBufferSnapshot{}
	}
	messages := make([]webSocketReplayMessage, len(b.messages))
	copy(messages, b.messages)
	for _, message := range messages {
		message.payload.references++
	}
	b.snapshotBytes += cap(messages) * webSocketReplayDescriptorBytes
	return preVisibleClientMessageBufferSnapshot{Enabled: true, Messages: messages,
		lease: &webSocketReplaySnapshotLease{buffer: b, messages: messages}}
}
func (b *preVisibleClientMessageBuffer) releasePayloadLocked(payload *webSocketReplayPayload) {
	if payload == nil {
		return
	}
	payload.references--
	if payload.references == 0 {
		b.totalBytes -= cap(payload.data)
	}
}
func (b *preVisibleClientMessageBuffer) closeLocked(state webSocketReplayState) {
	if b.state != webSocketReplayable {
		return
	}
	b.state = state
	status := b.statusLocked()
	for _, message := range b.messages {
		b.releasePayloadLocked(message.payload)
	}
	b.messages = nil
	// The first reason survives later visibility and parser transitions.
	if b.onTransition != nil {
		b.onTransition(status)
	}
}
func replayableClientFrameDecision() webSocketPreWriteDecision {
	return webSocketPreWriteDecision{Action: webSocketPreWriteActionForward, ReplayEligible: true, ReplacementEligible: true, PrepareReplay: replayableClientFrameDecision}
}
func (d webSocketPreWriteDecision) forReplayStorage() webSocketPreWriteDecision {
	d.OnWriteConfirmed = nil
	return d
}
func isReplayableWebSocketMessageType(messageType websocket.MessageType) bool {
	return messageType == websocket.MessageText || messageType == websocket.MessageBinary
}

// A first-delivery owner outlives replay closure. Sharing this reference keeps
// payload accounting accurate until the queued frame is physically delivered.
func (b *preVisibleClientMessageBuffer) retainForDelivery(index int) (webSocketReplayMessage, bool) {
	if b == nil {
		return webSocketReplayMessage{}, false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if index < 0 || index >= len(b.messages) {
		return webSocketReplayMessage{}, false
	}
	message := b.messages[index]
	message.payload.references++
	return message, true
}
func (b *preVisibleClientMessageBuffer) releaseDelivery(message webSocketReplayMessage) {
	if b == nil || message.payload == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.releasePayloadLocked(message.payload)
}
