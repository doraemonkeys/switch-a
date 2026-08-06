package requestcapture

import (
	"sync/atomic"
	"time"

	"github.com/doraemonkeys/switch-a/internal/requestcapture/redaction"
)

// GatewayRecorder is a lightweight value bound to one immutable session
// generation. Its zero value is the disabled-path recorder and is always safe.
type GatewayRecorder struct {
	manager       *Manager
	generation    uint64
	traceSequence uint64
	handleSlot    uint32
}

type gatewayState struct {
	session                    *sessionState
	boundSession               atomic.Pointer[sessionState]
	before                     *gatewayState
	after                      *gatewayState
	attached                   bool
	id                         string
	requestID                  string
	traceSequence              uint64
	handleSlot                 uint32
	startedAt                  time.Time
	nextExchange               uint64
	nextEntry                  uint64
	nextLineage                uint64
	nextMessageSequence        uint64
	pendingLineageFirst        *pendingLineageState
	pendingLineageLast         *pendingLineageState
	pendingLineageCount        int
	activeTransition           *transitionRecorderState
	finished                   bool
	selectedProvider           bool
	truncationCounted          bool
	historyBefore              bool
	historyAfter               bool
	exportSnapshotOwner        *exportState
	exportSnapshotIndex        int
	exportSnapshotMaterialized bool
	liveRecords                int
	entryCount                 int
	transitionCount            int
	charge                     int64
	entryFirst                 *traceEntryState
	entryLast                  *traceEntryState
	sharedRequest              *blob
	sharedRequestInitialized   bool
	sharedRequestComplete      bool
	sharedRequestExpected      int64
}

type traceEntryState struct {
	snapshot  TraceEntry
	record    *recordState
	stubOwner *transitionRecorderState
	before    *traceEntryState
	after     *traceEntryState
	charge    int64
}

type pendingLineageState struct {
	sequence uint64
	before   *pendingLineageState
	after    *pendingLineageState
	attached bool
	charge   int64
}

// Recorder captures one real HTTP request or WebSocket dial. It never surfaces
// failures to the proxy; capture degradation is represented in record metadata.
type recorderKind uint8

const (
	recorderKindRecord recorderKind = iota + 1
	recorderKindTransition
)

type Recorder struct {
	manager        *Manager
	generation     uint64
	traceSequence  uint64
	recordSequence uint64
	entrySequence  uint64
	gatewaySlot    uint32
	recordSlot     uint32
	recordID       recordIDValue
	kind           recorderKind
}

type transitionRecorderState struct {
	session            *sessionState
	boundSession       atomic.Pointer[sessionState]
	gateway            *gatewayState
	entry              *traceEntryState
	generation         uint64
	credentialEvidence CredentialEvidence
	completed          bool
}

type recordState struct {
	session      *sessionState
	boundSession atomic.Pointer[sessionState]
	gateway      *gatewayState
	id           string
	generation   uint64
	handleSlot   uint32
	protocol     Protocol
	charge       int64
	traceEntry   *traceEntryState

	disabled, completed, evicted, overflowCounted bool

	summary                                              RecordSummary
	request                                              RequestSnapshot
	requestBody                                          *blob
	responseBody                                         blobBuilder
	older                                                *recordState
	newer                                                *recordState
	providerBefore                                       *recordState
	providerAfter                                        *recordState
	httpResponse                                         *HTTPResponseSnapshot
	wsHandshake                                          *WebSocketHandshakeSnapshot
	wsClose                                              *WebSocketCloseSnapshot
	credentialEvidence                                   CredentialEvidence
	sensitiveHeaderNames                                 []string
	redactAllHeaders, responseObserved, stateFaultLogged bool
	messages                                             []*messageState
	messageByID                                          map[string]*messageState
	observedBytes                                        int64
	writtenBytes                                         int64
}

type messageState struct {
	id              string
	lineage         uint64
	sequence        uint64
	relativeMillis  int64
	direction       MessageDirection
	messageType     MessageType
	source          MessageSource
	sourceMessageID string
	disposition     MessageDisposition
	clientVisible   bool
	resultSet       bool
	observedSize    int
	failure         FailureObservation
	hasFailure      bool
	payload         *blob
	charge          int64
}

type gatewayAccess struct {
	session *sessionState
	gateway *gatewayState
}

func (access *gatewayAccess) release() {
	if access == nil || access.session == nil {
		return
	}
	session := access.session
	access.session = nil
	access.gateway = nil
	session.mu.Unlock()
	session.gate.RUnlock()
	_ = session.releaseOwner()
}

func (r GatewayRecorder) acquire() gatewayAccess {
	if r.manager == nil || r.generation == 0 || r.traceSequence == 0 || r.handleSlot == 0 {
		return gatewayAccess{}
	}
	session := r.manager.retainActive(r.generation)
	if session == nil {
		return gatewayAccess{}
	}
	releaseOwner := true
	defer func() {
		if releaseOwner {
			_ = session.releaseOwner()
		}
	}()
	if r.manager.active.Load() != session || session.generation != r.generation {
		return gatewayAccess{}
	}
	session.gate.RLock()
	if r.manager.active.Load() != session || session.generation != r.generation {
		session.gate.RUnlock()
		return gatewayAccess{}
	}
	session.mu.Lock()
	if !session.accepting || session.releasing || r.manager.active.Load() != session {
		session.mu.Unlock()
		session.gate.RUnlock()
		return gatewayAccess{}
	}
	gateway := session.gatewayHandleLocked(r.handleSlot, r.traceSequence)
	if gateway != nil && gateway.attached && gateway.boundSession.Load() == session {
		releaseOwner = false
		return gatewayAccess{session: session, gateway: gateway}
	}
	session.mu.Unlock()
	session.gate.RUnlock()
	return gatewayAccess{}
}

func (r GatewayRecorder) Valid() bool {
	access := r.acquire()
	valid := access.gateway != nil
	access.release()
	return valid
}

// NewMessageID assigns lineage before provider selection so replay events can
// refer to the original client message without retaining its payload.
func (r GatewayRecorder) NewMessageID() MessageLineage {
	access := r.acquire()
	defer access.release()
	if access.gateway == nil {
		return MessageLineage{}
	}
	return access.gateway.newMessageIDLocked()
}

func (r GatewayRecorder) BeginHTTP(input RawHTTPStart) Recorder {
	access := r.acquire()
	defer access.release()
	if access.gateway == nil {
		return Recorder{}
	}
	return access.gateway.beginRecordLocked(
		ProtocolHTTP,
		input.Attempt,
		input.Request,
		redaction.BorrowedHTTPTarget(input.URL),
	)
}

func (r GatewayRecorder) BeginWebSocket(input RawWebSocketStart) Recorder {
	access := r.acquire()
	defer access.release()
	if access.gateway == nil {
		return Recorder{}
	}
	return access.gateway.beginRecordLocked(
		ProtocolWebSocket,
		input.Attempt,
		input.Request,
		redaction.BorrowedWebSocketTarget(input.TargetURL),
	)
}

func (r GatewayRecorder) Transition(input TransitionStart) {
	access := r.acquire()
	defer access.release()
	if access.gateway != nil {
		access.gateway.transitionLocked(input)
	}
}

func (r GatewayRecorder) Finish(outcome GatewayOutcome) {
	access := r.acquire()
	defer access.release()
	if access.gateway != nil {
		access.gateway.finishLocked(outcome)
	}
}

type recorderAccess struct {
	session    *sessionState
	record     *recordState
	transition *transitionRecorderState
}

func (access *recorderAccess) release() {
	if access == nil || access.session == nil {
		return
	}
	session := access.session
	access.session = nil
	access.record = nil
	access.transition = nil
	session.mu.Unlock()
	session.gate.RUnlock()
	_ = session.releaseOwner()
}

func (r Recorder) validShape() bool {
	if r.manager == nil || r.generation == 0 || r.traceSequence == 0 || r.gatewaySlot == 0 {
		return false
	}
	switch r.kind {
	case recorderKindRecord:
		return r.recordSequence != 0 && r.recordSlot != 0
	case recorderKindTransition:
		return r.entrySequence != 0
	default:
		return false
	}
}

func (r Recorder) acquire() recorderAccess {
	if !r.validShape() {
		return recorderAccess{}
	}
	session := r.manager.retainActive(r.generation)
	if session == nil {
		return recorderAccess{}
	}
	releaseOwner := true
	defer func() {
		if releaseOwner {
			_ = session.releaseOwner()
		}
	}()
	if r.manager.active.Load() != session || session.generation != r.generation {
		return recorderAccess{}
	}
	session.gate.RLock()
	if r.manager.active.Load() != session || session.generation != r.generation {
		session.gate.RUnlock()
		return recorderAccess{}
	}
	session.mu.Lock()
	if !session.accepting || session.releasing || r.manager.active.Load() != session {
		session.mu.Unlock()
		session.gate.RUnlock()
		return recorderAccess{}
	}
	record, transition := r.resolveLocked(session)
	if record == nil && transition == nil {
		session.mu.Unlock()
		session.gate.RUnlock()
		return recorderAccess{}
	}
	releaseOwner = false
	return recorderAccess{session: session, record: record, transition: transition}
}

func (r Recorder) resolveLocked(session *sessionState) (*recordState, *transitionRecorderState) {
	switch r.kind {
	case recorderKindRecord:
		record := session.recordHandleLocked(r.recordSlot, r.recordSequence)
		gateway := session.gatewayHandleLocked(r.gatewaySlot, r.traceSequence)
		if record != nil && gateway != nil && record.generation == r.generation &&
			record.gateway == gateway && record.boundSession.Load() == session {
			return record, nil
		}
	case recorderKindTransition:
		gateway := session.gatewayHandleLocked(r.gatewaySlot, r.traceSequence)
		if gateway != nil && gateway.attached && gateway.boundSession.Load() == session {
			stub := gateway.activeTransition
			if stub != nil && stub.entry != nil && stub.entry.snapshot.Sequence == r.entrySequence &&
				!stub.completed && stub.boundSession.Load() == session {
				return nil, stub
			}
		}
	}
	return nil, nil
}

func (r Recorder) Valid() bool {
	access := r.acquire()
	valid := access.record != nil || access.transition != nil
	access.release()
	return valid
}

// CapturesPayload distinguishes selected records from transition-only attempts
// without resolving the session graph, keeping unselected data paths cold.
func (r Recorder) CapturesPayload() bool {
	return r.kind == recorderKindRecord && r.recordID.Valid()
}

func (r Recorder) ID() string {
	if r.kind != recorderKindRecord {
		return ""
	}
	return r.recordID.String()
}

func (r Recorder) ObserveHTTPResponse(head HTTPResponseHead) {
	access := r.acquire()
	defer access.release()
	if access.record != nil {
		access.record.observeHTTPResponseLocked(head)
	}
}

// ObserveResponse is the concise HTTP integration spelling.
func (r Recorder) ObserveResponse(head HTTPResponseHead) {
	r.ObserveHTTPResponse(head)
}

func (r Recorder) ObserveWebSocketHandshake(handshake WebSocketHandshake) {
	access := r.acquire()
	defer access.release()
	if access.record != nil {
		access.record.observeWebSocketHandshakeLocked(handshake)
	}
}

func (r Recorder) ObserveUpstream(payload []byte) {
	if len(payload) == 0 {
		return
	}
	access := r.acquire()
	defer access.release()
	if access.record != nil {
		access.record.observeUpstreamLocked(payload)
	}
}

func (r Recorder) ObserveClientWrite(bytes int) {
	if bytes <= 0 {
		return
	}
	access := r.acquire()
	defer access.release()
	if access.record != nil {
		access.record.observeClientWriteLocked(bytes)
	}
}

func (r Recorder) MessageRead(input MessageRead) MessageRef {
	access := r.acquire()
	defer access.release()
	if access.record != nil {
		return access.record.messageReadLocked(input)
	}
	if access.transition != nil {
		return access.transition.messageLineageLocked(input)
	}
	return MessageRef{}
}

func (r *transitionRecorderState) messageLineageLocked(input MessageRead) MessageRef {
	if r == nil || r.session == nil || r.gateway == nil || r.completed ||
		r.boundSession.Load() != r.session || !r.gateway.attached || r.gateway.finished {
		return MessageRef{}
	}
	direction, directionValid := canonicalMessageDirection(input.Direction)
	messageType, typeValid := canonicalMessageType(input.Type)
	source := input.Source
	if source == "" {
		source = MessageSourceLive
	}
	canonicalSource, sourceValid := canonicalMessageSource(source)
	if !directionValid || !typeValid || !sourceValid ||
		direction != MessageDirectionClientToUpstream ||
		canonicalSource != MessageSourceLive ||
		messageType == "" {
		return MessageRef{}
	}
	lineage := input.Lineage
	if !lineage.Valid() ||
		lineage.generation != r.session.generation ||
		lineage.traceSequence != r.gateway.traceSequence ||
		r.gateway.findPendingLineageLocked(lineage.lineage) == nil {
		return MessageRef{}
	}
	return MessageRef{
		manager:       r.session.manager,
		generation:    lineage.generation,
		traceSequence: lineage.traceSequence,
		lineage:       lineage.lineage,
	}
}

func (r Recorder) MessageResult(ref MessageRef, result MessageResult) {
	if !ref.belongsTo(r) {
		return
	}
	access := r.acquire()
	defer access.release()
	if access.record != nil {
		access.record.messageResultLocked(ref, result)
	}
}

func (r Recorder) Finish(outcome Outcome) {
	access := r.acquire()
	defer access.release()
	if access.record != nil {
		if !access.record.completed && !access.record.evicted {
			access.record.finishLocked(outcome, true)
		}
		return
	}
	if access.transition != nil {
		access.transition.finishLocked(outcome)
	}
}

func (r *recordState) observeHTTPResponseLocked(head HTTPResponseHead) {
	if r == nil || r.session == nil || !r.mutableLocked() {
		return
	}
	session := r.session
	if r.protocol != ProtocolHTTP {
		r.stateFaultLocked("http_response_on_non_http_record")
		return
	}
	if r.responseObserved {
		r.stateFaultLocked("duplicate_http_response")
		return
	}
	r.responseObserved = true
	metadata := responseMetadata(head)
	// The injected key belongs to the upstream attempt, not an individual
	// response phase. Keeping the start-time evidence authoritative prevents a
	// sparse failure/fallback event from accidentally changing capture policy.
	metadata.CredentialEvidence = r.credentialEvidence
	result := (redaction.Sanitizer{}).HTTPResponseDetailed(
		metadata,
		r.sensitiveHeaderNames,
		r.redactAllHeaders,
	)
	// Discovery can affect later failure and close sanitization even when this
	// optional response snapshot cannot be retained.
	r.redactAllHeaders = r.redactAllHeaders || result.RedactAll
	nameCharge := estimateStringSliceCharge(result.SensitiveNames) - estimateStringSliceCharge(r.sensitiveHeaderNames)
	if nameCharge < 0 {
		nameCharge = 0
	}
	charge := addRetainedCharge64(estimateHTTPResponseCharge(result.Snapshot, nil), nameCharge)
	if !session.reserveLocked(charge, true) {
		r.markOverflowLocked()
		r.httpResponse = &HTTPResponseSnapshot{
			StatusCode:    head.StatusCode,
			ContentLength: head.ContentLength,
		}
		return
	}
	r.charge += charge
	r.sensitiveHeaderNames = result.SensitiveNames
	r.redactAllHeaders = result.RedactAll
	r.httpResponse = &result.Snapshot
	if result.Truncated {
		r.markOverflowLocked()
		session.logMetadataTruncationLocked(r.gateway, r, "http_response", maxRetainedHeaderBytes)
	}
}

func (r *recordState) observeWebSocketHandshakeLocked(handshake WebSocketHandshake) {
	if r == nil || r.session == nil || !r.mutableLocked() {
		return
	}
	session := r.session
	if r.protocol != ProtocolWebSocket {
		r.stateFaultLocked("websocket_handshake_on_non_websocket_record")
		return
	}
	if r.responseObserved {
		r.stateFaultLocked("duplicate_websocket_handshake")
		return
	}
	r.responseObserved = true
	metadata := handshakeMetadata(handshake)
	metadata.CredentialEvidence = r.credentialEvidence
	result := (redaction.Sanitizer{}).WebSocketHandshakeDetailed(
		metadata,
		r.sensitiveHeaderNames,
		r.redactAllHeaders,
	)
	// The safety decision survives metadata admission failure.
	r.redactAllHeaders = r.redactAllHeaders || result.RedactAll
	nameCharge := estimateStringSliceCharge(result.SensitiveNames) - estimateStringSliceCharge(r.sensitiveHeaderNames)
	if nameCharge < 0 {
		nameCharge = 0
	}
	charge := addRetainedCharge64(estimateWebSocketHandshakeCharge(result.Snapshot, nil), nameCharge)
	if !session.reserveLocked(charge, true) {
		r.markOverflowLocked()
		r.wsHandshake = &WebSocketHandshakeSnapshot{StatusCode: handshake.StatusCode}
		return
	}
	r.charge += charge
	r.sensitiveHeaderNames = result.SensitiveNames
	r.redactAllHeaders = result.RedactAll
	r.wsHandshake = &result.Snapshot
	if result.Truncated {
		r.markOverflowLocked()
		session.logMetadataTruncationLocked(r.gateway, r, "websocket_handshake", maxRetainedHeaderBytes)
	}
}

func (r *recordState) messageResultLocked(ref MessageRef, result MessageResult) {
	if r == nil || r.session == nil ||
		ref.manager != r.session.manager ||
		ref.traceSequence == 0 ||
		!r.mutableLocked() ||
		ref.generation != r.generation ||
		r.gateway == nil ||
		ref.traceSequence != r.gateway.traceSequence ||
		ref.recordSequence != r.summary.RecordSequence {
		return
	}
	session := r.session
	var message *messageState
	for _, candidate := range r.messages {
		if candidate != nil && candidate.sequence == ref.sequence &&
			candidate.lineage == ref.lineage {
			message = candidate
			break
		}
	}
	if message == nil || message.resultSet {
		return
	}
	disposition, dispositionValid := canonicalMessageDisposition(result.Disposition)
	if !dispositionValid {
		r.stateFaultLocked("invalid_message_disposition")
		return
	}
	result.Disposition = disposition
	if result.WriteConfirmed && result.Disposition != MessageDispositionForwarded {
		r.stateFaultLocked("write_confirmed_without_forwarded_disposition")
		return
	}
	failure, hasFailure := (redaction.Sanitizer{}).FailureDetailed(
		result.Failure, r.credentialEvidence, r.redactAllHeaders,
	)
	if hasFailure {
		charge := estimateFailureCharge(failure)
		if session.reserveLocked(charge, true) {
			message.charge += charge
			message.failure = failure
			message.hasFailure = true
		} else {
			failure.Truncated = true
			r.markOverflowLocked()
		}
	}
	message.resultSet = true
	message.disposition = result.Disposition
	if failure.Truncated {
		r.markOverflowLocked()
		session.logMetadataTruncationLocked(r.gateway, r, "message_failure", maxRetainedErrorBytes)
	}
	if result.WriteConfirmed {
		r.writtenBytes += int64(message.observedSize)
		message.clientVisible = message.direction == MessageDirectionUpstreamToClient &&
			message.disposition == MessageDispositionForwarded
		if message.direction == MessageDirectionClientToUpstream {
			if pending := r.gateway.findPendingLineageLocked(message.lineage); pending != nil {
				r.gateway.removePendingLineageLocked(pending)
			}
		}
	}
	r.syncCountersLocked()
}
