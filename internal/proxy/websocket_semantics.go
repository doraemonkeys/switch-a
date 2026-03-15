package proxy

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync"

	"github.com/coder/websocket"
)

const (
	webSocketEventResponseDone                     = "response.done"
	webSocketEventResponseCompleted                = "response.completed"
	webSocketEventResponseCreated                  = "response.created"
	webSocketEventResponseCreate                   = "response.create"
	webSocketEventSessionCreated                   = "session.created"
	webSocketEventSessionUpdated                   = "session.updated"
	webSocketEventSessionUpdate                    = "session.update"
	webSocketEventInputAudioTranscriptionCompleted = "conversation.item.input_audio_transcription.completed"
)

// WebSocketObservation captures semantic data learned while relaying a WebSocket session.
// The transport layer emits bytes, but request logs need domain fields like model and usage.
type WebSocketObservation struct {
	Model         string
	TokenUsage    *TokenUsage
	UpstreamError *WebSocketUpstreamError
}

// WebSocketUpstreamError captures a provider error that was delivered inside the
// WebSocket session after the HTTP upgrade already succeeded. Request logs need
// this semantic error because a bare 101 status is misleading when the first
// upstream event is actually an authorization/model/billing failure.
type WebSocketUpstreamError struct {
	EventType  string
	StatusCode int
	Message    string
	Raw        string
}

func (e *WebSocketUpstreamError) Clone() *WebSocketUpstreamError {
	if e == nil {
		return nil
	}
	cloned := *e
	return &cloned
}

// WebSocketMessageObserver consumes relayed messages and reconstructs semantic fields that
// are not present in the initial HTTP upgrade request.
type WebSocketMessageObserver interface {
	ObserveClientMessage(messageType websocket.MessageType, data []byte)
	ObserveUpstreamMessage(messageType websocket.MessageType, data []byte)
	Snapshot() WebSocketObservation
}

func newWebSocketMessageObserver(apiType, initialModel string, logger Logger, onUpdate func(WebSocketObservation)) WebSocketMessageObserver {
	if apiType != APITypeCodex {
		return nil
	}
	return newCodexWebSocketMessageObserver(initialModel, logger, onUpdate)
}

type codexWebSocketEventEnvelope struct {
	Type     string                     `json:"type"`
	EventID  string                     `json:"event_id"`
	Model    string                     `json:"model"`
	Status   int                        `json:"status"`
	Error    *codexWebSocketEventError  `json:"error"`
	Session  *codexWebSocketSession     `json:"session"`
	Response *codexWebSocketEventTarget `json:"response"`
}

type codexWebSocketEventError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code"`
}

type codexWebSocketSession struct {
	Model string `json:"model"`
}

type codexWebSocketEventTarget struct {
	ID    string `json:"id"`
	Model string `json:"model"`
}

type codexWebSocketMessageObserver struct {
	mu            sync.Mutex
	model         string
	modelSource   webSocketModelSource
	tokenUsage    *TokenUsage
	upstreamError *WebSocketUpstreamError
	seenUsageKeys map[string]struct{}
	logger        Logger
	onUpdate      func(WebSocketObservation)
}

type webSocketModelSource uint8

const (
	webSocketModelSourceUnknown webSocketModelSource = iota
	webSocketModelSourceRequest
	webSocketModelSourceClient
	webSocketModelSourceUpstream
)

func newCodexWebSocketMessageObserver(initialModel string, logger Logger, onUpdate func(WebSocketObservation)) *codexWebSocketMessageObserver {
	model := ""
	modelSource := webSocketModelSourceUnknown
	if initialModel != "" && initialModel != ModelUnknown {
		model = initialModel
		modelSource = webSocketModelSourceRequest
	}
	return &codexWebSocketMessageObserver{
		model:         model,
		modelSource:   modelSource,
		seenUsageKeys: make(map[string]struct{}),
		logger:        logger,
		onUpdate:      onUpdate,
	}
}

func (o *codexWebSocketMessageObserver) ObserveClientMessage(messageType websocket.MessageType, data []byte) {
	o.observe(messageType, data, false)
}

func (o *codexWebSocketMessageObserver) ObserveUpstreamMessage(messageType websocket.MessageType, data []byte) {
	o.observe(messageType, data, true)
}

func (o *codexWebSocketMessageObserver) Snapshot() WebSocketObservation {
	o.mu.Lock()
	defer o.mu.Unlock()

	return WebSocketObservation{
		Model:         o.model,
		TokenUsage:    o.tokenUsage.Clone(),
		UpstreamError: o.upstreamError.Clone(),
	}
}

// quickExtractEventType extracts the "type" field value from a JSON message by
// scanning the first few hundred bytes without a full json.Unmarshal. Realtime
// API events always place "type" near the start of the envelope, so a bounded
// scan is sufficient. Returns "" if the type cannot be determined.
const typeFieldScanLimit = 256
const errorFieldScanLimit = 1024

var typeFieldKey = []byte(`"type":"`)

func quickExtractEventType(data []byte) string {
	limit := len(data)
	if limit > typeFieldScanLimit {
		limit = typeFieldScanLimit
	}
	idx := bytes.Index(data[:limit], typeFieldKey)
	if idx < 0 {
		return ""
	}
	valueStart := idx + len(typeFieldKey)
	if valueStart >= limit {
		return ""
	}
	for i := valueStart; i < limit; i++ {
		if data[i] == '"' {
			return string(data[valueStart:i])
		}
	}
	return ""
}

// isObservableEventType returns true for events that may carry model or usage
// data worth a full JSON parse. High-volume transport events like
// input_audio_buffer.append (base64 audio up to several MiB) are excluded to
// avoid parsing overhead on the relay hot path.
func isObservableEventType(eventType string) bool {
	switch eventType {
	case webSocketEventSessionCreated, webSocketEventSessionUpdated, webSocketEventSessionUpdate,
		webSocketEventResponseDone, webSocketEventResponseCompleted,
		webSocketEventResponseCreated, webSocketEventResponseCreate:
		return true
	default:
		return false
	}
}

func payloadMayContainError(data []byte) bool {
	limit := len(data)
	if limit > errorFieldScanLimit {
		limit = errorFieldScanLimit
	}
	snippet := data[:limit]
	return bytes.Contains(snippet, []byte(`"error"`)) ||
		bytes.Contains(snippet, []byte(`"type":"error"`)) ||
		bytes.Contains(snippet, []byte(`"status":4`)) ||
		bytes.Contains(snippet, []byte(`"status":5`))
}

func (o *codexWebSocketMessageObserver) observe(messageType websocket.MessageType, data []byte, fromUpstream bool) {
	if messageType != websocket.MessageText || len(data) == 0 {
		return
	}

	// Fast-path: extract event type from the first 256 bytes to avoid a full
	// json.Unmarshal on high-volume events (e.g. input_audio_buffer.append).
	// Fall through to full parse when extraction fails (defensive). Error frames
	// are exempt from the early return because nested `error.type` fields can
	// appear before the top-level `type`, so a raw substring scan cannot safely
	// distinguish "ignore this transport event" from "this is the terminal provider error".
	if eventType := quickExtractEventType(data); eventType != "" && !isObservableEventType(eventType) && !payloadMayContainError(data) {
		return
	}

	var event codexWebSocketEventEnvelope
	if err := json.Unmarshal(data, &event); err != nil {
		return
	}

	o.mu.Lock()
	modelChanged := o.captureModelLocked(&event, fromUpstream)
	errorChanged := o.captureUpstreamErrorLocked(&event, data, fromUpstream)
	semanticChanged := modelChanged || errorChanged
	if !fromUpstream || !isCodexUsageEvent(event.Type) {
		observation, shouldPublish := o.snapshotForPublishLocked(semanticChanged)
		o.mu.Unlock()
		if shouldPublish {
			o.publish(observation)
		}
		return
	}

	usageKey := codexUsageEventKey(&event)
	if usageKey != "" {
		if _, exists := o.seenUsageKeys[usageKey]; exists {
			observation, shouldPublish := o.snapshotForPublishLocked(semanticChanged)
			o.mu.Unlock()
			if shouldPublish {
				o.publish(observation)
			}
			return
		}
	}

	usage := parseTokenUsageWithLogger(data, o.logger)
	if usage == nil {
		observation, shouldPublish := o.snapshotForPublishLocked(semanticChanged)
		o.mu.Unlock()
		if shouldPublish {
			o.publish(observation)
		}
		return
	}
	o.tokenUsage = o.tokenUsage.Merge(usage)
	if usageKey != "" {
		o.seenUsageKeys[usageKey] = struct{}{}
	}
	observation, shouldPublish := o.snapshotForPublishLocked(true)
	o.mu.Unlock()
	if shouldPublish {
		o.publish(observation)
	}
}

func (o *codexWebSocketMessageObserver) captureModelLocked(event *codexWebSocketEventEnvelope, fromUpstream bool) bool {
	source := webSocketModelSourceClient
	if fromUpstream {
		source = webSocketModelSourceUpstream
	}

	var model string
	switch {
	case event.Session != nil && event.Session.Model != "":
		model = event.Session.Model
	case event.Response != nil && event.Response.Model != "":
		model = event.Response.Model
	case event.Model != "":
		model = event.Model
	}

	if model == "" || source < o.modelSource {
		return false
	}
	// Equal-priority updates intentionally replace the previous value. Codex sessions may
	// first expose an alias during setup and later emit the canonical billed model from
	// another upstream event, so "first writer wins" would freeze the less accurate value.
	if model == o.model && source == o.modelSource {
		return false
	}
	o.model = model
	o.modelSource = source
	return true
}

func (o *codexWebSocketMessageObserver) captureUpstreamErrorLocked(event *codexWebSocketEventEnvelope, data []byte, fromUpstream bool) bool {
	if !fromUpstream || !codexEventRepresentsError(event) {
		return false
	}

	next := &WebSocketUpstreamError{
		EventType:  event.Type,
		StatusCode: event.Status,
		Raw:        string(data),
	}
	if event.Error != nil {
		if event.Error.Type != "" {
			next.EventType = event.Error.Type
		}
		next.Message = strings.TrimSpace(event.Error.Message)
	}
	if next.Message == "" {
		next.Message = next.EventType
	}

	if upstreamErrorsEqual(o.upstreamError, next) {
		return false
	}
	o.upstreamError = next
	return true
}

func isCodexUsageEvent(eventType string) bool {
	// Transcription events (input_audio_transcription.completed) are intentionally
	// excluded: OpenAI bills them under a separate ASR model, so merging their
	// tokens into the realtime model total produces incorrect per-model cost data.
	switch eventType {
	case webSocketEventResponseDone, webSocketEventResponseCompleted:
		return true
	default:
		return false
	}
}

func codexEventRepresentsError(event *codexWebSocketEventEnvelope) bool {
	if event == nil {
		return false
	}
	if event.Error != nil {
		return true
	}
	if event.Status >= 400 {
		return true
	}

	lowerType := strings.ToLower(event.Type)
	return lowerType == "error" ||
		strings.Contains(lowerType, "error") ||
		strings.Contains(lowerType, "fail")
}

func codexUsageEventKey(event *codexWebSocketEventEnvelope) string {
	if event.Response != nil && event.Response.ID != "" {
		return "response:" + event.Response.ID
	}
	if event.EventID != "" {
		return "event:" + event.EventID
	}
	return ""
}

func (o *codexWebSocketMessageObserver) snapshotForPublishLocked(changed bool) (WebSocketObservation, bool) {
	if !changed {
		return WebSocketObservation{}, false
	}
	return WebSocketObservation{
		Model:         o.model,
		TokenUsage:    o.tokenUsage.Clone(),
		UpstreamError: o.upstreamError.Clone(),
	}, true
}

func (o *codexWebSocketMessageObserver) publish(observation WebSocketObservation) {
	if o.onUpdate == nil {
		return
	}
	o.onUpdate(observation)
}

func upstreamErrorsEqual(left, right *WebSocketUpstreamError) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.EventType == right.EventType &&
		left.StatusCode == right.StatusCode &&
		left.Message == right.Message &&
		left.Raw == right.Raw
}
