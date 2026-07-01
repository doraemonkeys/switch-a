package proxy

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync"
	"time"

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
	Model              string
	TokenUsage         *TokenUsage
	UpstreamError      *WebSocketUpstreamError
	SessionCommitted   bool
	CompletionObserved bool
	CommitEventType    string
	ParseDegraded      bool
}

// WebSocketUpstreamError captures a provider error that was delivered inside the
// WebSocket session after the HTTP upgrade already succeeded. Request logs need
// this semantic error because a bare 101 status is misleading when the first
// upstream event is actually an authorization/model/billing failure.
type WebSocketUpstreamError struct {
	EnvelopeType      string
	ProviderErrorType string
	EventType         string
	Code              string
	StatusCode        int
	Message           string
	ObservedAt        time.Time
	ResetAt           *time.Time
	Raw               string
}

func (e *WebSocketUpstreamError) Clone() *WebSocketUpstreamError {
	if e == nil {
		return nil
	}
	cloned := *e
	if e.ResetAt != nil {
		resetAt := *e.ResetAt
		cloned.ResetAt = &resetAt
	}
	return &cloned
}

func (e *WebSocketUpstreamError) IsAllowlistedProviderScoped() bool {
	return classifyWebSocketUpstreamError(e) == webSocketSemanticClassificationProviderScopedAllowlisted
}

func (e *WebSocketUpstreamError) IsSwitchableProviderScoped() bool {
	switch classifyWebSocketUpstreamError(e) {
	case webSocketSemanticClassificationProviderScoped,
		webSocketSemanticClassificationProviderScopedAllowlisted:
		return true
	default:
		return false
	}
}

func (e *WebSocketUpstreamError) SemanticErrorKey() string {
	if e == nil {
		return ""
	}
	if strings.TrimSpace(e.ProviderErrorType) != "" {
		return e.ProviderErrorType
	}
	if strings.TrimSpace(e.EventType) != "" {
		return e.EventType
	}
	return e.EnvelopeType
}

// WebSocketMessageObserver consumes relayed messages and reconstructs semantic fields that
// are not present in the initial HTTP upgrade request.
type WebSocketMessageObserver interface {
	ObserveClientMessage(messageType websocket.MessageType, data []byte)
	ObserveUpstreamMessage(messageType websocket.MessageType, data []byte)
	Snapshot() WebSocketObservation
	ParseDegraded() bool
	HasSemanticObservation() bool
}

type webSocketSemanticClassification uint8

const (
	webSocketSemanticClassificationUnknown webSocketSemanticClassification = iota
	webSocketSemanticClassificationClientScoped
	webSocketSemanticClassificationProviderScoped
	webSocketSemanticClassificationProviderScopedAllowlisted
)

type webSocketSemanticFrameDecision uint8

const (
	webSocketSemanticFrameDecisionForward webSocketSemanticFrameDecision = iota
	webSocketSemanticFrameDecisionSuppress
)

// webSocketSemanticDecision keeps semantic classification separate from relay action.
// The recovery loop can reason about provider scope while the relay only needs to know
// whether the current upstream frame should still be forwarded to the client.
type webSocketSemanticDecision struct {
	Classification webSocketSemanticClassification
	FrameDecision  webSocketSemanticFrameDecision
}

func newWebSocketMessageObserver(apiType, initialModel string, logger Logger, onUpdate func(WebSocketObservation), onCommit func(WebSocketObservation)) WebSocketMessageObserver {
	if apiType != APITypeCodex {
		return nil
	}
	return newCodexWebSocketMessageObserver(initialModel, logger, onUpdate, onCommit)
}

type codexWebSocketEventEnvelope struct {
	Type       string                     `json:"type"`
	EventID    string                     `json:"event_id"`
	Model      string                     `json:"model"`
	Status     int                        `json:"status"`
	StatusCode int                        `json:"status_code"`
	Error      *codexWebSocketEventError  `json:"error"`
	Session    *codexWebSocketSession     `json:"session"`
	Response   *codexWebSocketEventTarget `json:"response"`
}

type codexWebSocketEventError struct {
	Message    string `json:"message"`
	Type       string `json:"type"`
	Code       string `json:"code"`
	StatusCode int    `json:"status_code"`
	ResetsAt   int64  `json:"resets_at"`
}

type codexWebSocketSession struct {
	Model string `json:"model"`
}

type codexWebSocketEventTarget struct {
	ID    string `json:"id"`
	Model string `json:"model"`
}

type codexObserveState struct {
	observation   WebSocketObservation
	shouldPublish bool
	commitChanged bool
	needsUsage    bool
	usageKey      string
}

type codexWebSocketMessageObserver struct {
	mu                 sync.Mutex
	model              string
	modelSource        webSocketModelSource
	tokenUsage         *TokenUsage
	upstreamError      *WebSocketUpstreamError
	parseDegraded      bool
	sessionCommitted   bool
	completionObserved bool
	commitEventType    string
	seenUsageKeys      map[string]struct{}
	logger             Logger
	onUpdate           func(WebSocketObservation)
	onCommit           func(WebSocketObservation)
}

type webSocketModelSource uint8

const (
	webSocketModelSourceUnknown webSocketModelSource = iota
	webSocketModelSourceRequest
	webSocketModelSourceClient
	webSocketModelSourceUpstream
)

func newCodexWebSocketMessageObserver(initialModel string, logger Logger, onUpdate func(WebSocketObservation), onCommit func(WebSocketObservation)) *codexWebSocketMessageObserver {
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
		onCommit:      onCommit,
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
		Model:              o.model,
		TokenUsage:         o.tokenUsage.Clone(),
		UpstreamError:      o.upstreamError.Clone(),
		SessionCommitted:   o.sessionCommitted,
		CompletionObserved: o.completionObserved,
		CommitEventType:    o.commitEventType,
		ParseDegraded:      o.parseDegraded,
	}
}

func (o *codexWebSocketMessageObserver) ParseDegraded() bool {
	o.mu.Lock()
	defer o.mu.Unlock()

	return o.parseDegraded
}

func (o *codexWebSocketMessageObserver) HasSemanticObservation() bool {
	return true
}

// quickExtractEventType extracts the "type" field value from a JSON message by
// scanning the first few hundred bytes without a full json.Unmarshal. Realtime
// API events always place "type" near the start of the envelope, so a bounded
// scan is sufficient. Returns "" if the type cannot be determined.
const typeFieldScanLimit = 256
const errorFieldScanLimit = 1024

var typeFieldKey = []byte(`"type":"`)

var webSocketProviderScopedAllowlistedErrorKeys = map[string]struct{}{
	"auth_error":           {},
	"authentication_error": {},
	"billing_error":        {},
	"insufficient_quota":   {},
	"invalid_api_key":      {},
	"model_not_allowed":    {},
	"permission_denied":    {},
	"quota_exceeded":       {},
	"rate_limit_error":     {},
	"rate_limit_exceeded":  {},
}

var webSocketClientScopedErrorKeys = map[string]struct{}{
	"bad_request_error":       {},
	"content_filter_error":    {},
	"context_length_exceeded": {},
	"invalid_event":           {},
	"invalid_message":         {},
	"invalid_request_error":   {},
	"unprocessable_entity":    {},
	"unsupported_format":      {},
	"validation_error":        {},
}

func quickExtractEventType(data []byte) string {
	limit := min(len(data), typeFieldScanLimit)
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
	limit := min(len(data), errorFieldScanLimit)
	snippet := data[:limit]
	return bytes.Contains(snippet, []byte(`"error"`)) ||
		bytes.Contains(snippet, []byte(`"type":"error"`)) ||
		bytes.Contains(snippet, []byte(`"status_code":4`)) ||
		bytes.Contains(snippet, []byte(`"status_code":5`)) ||
		bytes.Contains(snippet, []byte(`"status":4`)) ||
		bytes.Contains(snippet, []byte(`"status":5`))
}

func (o *codexWebSocketMessageObserver) observe(messageType websocket.MessageType, data []byte, fromUpstream bool) {
	if shouldSkipCodexObservedPayload(messageType, data) {
		return
	}

	if shouldFastSkipCodexPayload(data) {
		return
	}

	event, ok := o.decodeCodexObservedEvent(data, fromUpstream)
	if !ok {
		return
	}

	state := o.captureCodexObserveState(&event, data, fromUpstream)
	if state.needsUsage {
		usage := parseTokenUsageWithLogger(data, o.logger)
		if usage != nil {
			state = o.captureCodexUsageState(state, usage)
		}
	}
	o.publishCodexObserveState(state)
}

func shouldSkipCodexObservedPayload(messageType websocket.MessageType, data []byte) bool {
	return messageType != websocket.MessageText || len(data) == 0
}

func shouldFastSkipCodexPayload(data []byte) bool {
	// Fast-path: extract event type from the first 256 bytes to avoid a full
	// json.Unmarshal on high-volume events (e.g. input_audio_buffer.append).
	// Fall through to full parse when extraction fails (defensive). Error frames
	// are exempt from the early return because nested `error.type` fields can
	// appear before the top-level `type`, so a raw substring scan cannot safely
	// distinguish "ignore this transport event" from "this is the terminal provider error".
	eventType := quickExtractEventType(data)
	return eventType != "" && !isObservableEventType(eventType) && !payloadMayContainError(data)
}

func (o *codexWebSocketMessageObserver) decodeCodexObservedEvent(data []byte, fromUpstream bool) (codexWebSocketEventEnvelope, bool) {
	var event codexWebSocketEventEnvelope
	if err := json.Unmarshal(data, &event); err != nil {
		if fromUpstream {
			o.markParseDegraded()
		}
		return codexWebSocketEventEnvelope{}, false
	}
	return event, true
}

func (o *codexWebSocketMessageObserver) markParseDegraded() {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.parseDegraded = true
}

func (o *codexWebSocketMessageObserver) captureCodexObserveState(event *codexWebSocketEventEnvelope, data []byte, fromUpstream bool) codexObserveState {
	o.mu.Lock()
	defer o.mu.Unlock()

	modelChanged := o.captureModelLocked(event, fromUpstream)
	errorChanged := o.captureUpstreamErrorLocked(event, data, fromUpstream)
	commitChanged := o.captureCommitLocked(event, fromUpstream)
	completionChanged := o.captureCompletionLocked(event, fromUpstream)
	semanticChanged := modelChanged || errorChanged || commitChanged || completionChanged
	if !fromUpstream || !isCodexUsageEvent(event.Type) {
		return o.newCodexObserveStateLocked(semanticChanged, commitChanged, "", false)
	}

	usageKey := codexUsageEventKey(event)
	if o.hasSeenUsageKeyLocked(usageKey) {
		return o.newCodexObserveStateLocked(semanticChanged, commitChanged, usageKey, false)
	}
	return o.newCodexObserveStateLocked(semanticChanged, commitChanged, usageKey, true)
}

func (o *codexWebSocketMessageObserver) hasSeenUsageKeyLocked(usageKey string) bool {
	if usageKey == "" {
		return false
	}
	_, exists := o.seenUsageKeys[usageKey]
	return exists
}

func (o *codexWebSocketMessageObserver) newCodexObserveStateLocked(changed, commitChanged bool, usageKey string, needsUsage bool) codexObserveState {
	observation, shouldPublish := o.snapshotForPublishLocked(changed)
	return codexObserveState{
		observation:   observation,
		shouldPublish: shouldPublish,
		commitChanged: commitChanged,
		needsUsage:    needsUsage,
		usageKey:      usageKey,
	}
}

func (o *codexWebSocketMessageObserver) captureCodexUsageState(state codexObserveState, usage *TokenUsage) codexObserveState {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.tokenUsage = o.tokenUsage.Merge(usage)
	if state.usageKey != "" {
		o.seenUsageKeys[state.usageKey] = struct{}{}
	}
	observation, shouldPublish := o.snapshotForPublishLocked(true)
	state.observation = observation
	state.shouldPublish = shouldPublish
	state.needsUsage = false
	return state
}

func (o *codexWebSocketMessageObserver) publishCodexObserveState(state codexObserveState) {
	if state.shouldPublish {
		o.publish(state.observation)
	}
	if state.commitChanged {
		o.publishCommit(state.observation)
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

	next := buildWebSocketUpstreamError(event, data, time.Now().UTC())

	if upstreamErrorsEqual(o.upstreamError, next) {
		return false
	}
	o.upstreamError = next
	return true
}

func (o *codexWebSocketMessageObserver) captureCommitLocked(event *codexWebSocketEventEnvelope, fromUpstream bool) bool {
	if !fromUpstream || o.sessionCommitted || event == nil || event.Type != webSocketEventResponseCreated {
		return false
	}
	if codexEventRepresentsError(event) {
		return false
	}
	o.sessionCommitted = true
	o.commitEventType = event.Type
	return true
}

func (o *codexWebSocketMessageObserver) captureCompletionLocked(event *codexWebSocketEventEnvelope, fromUpstream bool) bool {
	if !fromUpstream || o.completionObserved || event == nil || !isCodexUsageEvent(event.Type) {
		return false
	}
	if codexEventRepresentsError(event) {
		return false
	}
	o.completionObserved = true
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
	if codexEventStatusCode(event) >= 400 {
		return true
	}

	lowerType := strings.ToLower(event.Type)
	return strings.Contains(lowerType, "error") ||
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
		Model:              o.model,
		TokenUsage:         o.tokenUsage.Clone(),
		UpstreamError:      o.upstreamError.Clone(),
		SessionCommitted:   o.sessionCommitted,
		CompletionObserved: o.completionObserved,
		CommitEventType:    o.commitEventType,
		ParseDegraded:      o.parseDegraded,
	}, true
}

func (o *codexWebSocketMessageObserver) publish(observation WebSocketObservation) {
	if o.onUpdate == nil {
		return
	}
	o.onUpdate(observation)
}

func (o *codexWebSocketMessageObserver) publishCommit(observation WebSocketObservation) {
	if o.onCommit == nil {
		return
	}
	o.onCommit(observation)
}

func upstreamErrorsEqual(left, right *WebSocketUpstreamError) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.EnvelopeType == right.EnvelopeType &&
		left.ProviderErrorType == right.ProviderErrorType &&
		left.EventType == right.EventType &&
		left.Code == right.Code &&
		left.StatusCode == right.StatusCode &&
		left.Message == right.Message &&
		timesEqual(left.ResetAt, right.ResetAt) &&
		left.Raw == right.Raw
}
