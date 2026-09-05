package requestcapture

import (
	"math"
	"net/http"

	"github.com/doraemonkeys/switch-a/internal/requestcapture/redaction"
	"go.uber.org/zap"
)

func (s *sessionState) beginGateway(input GatewayStart) GatewayRecorder {
	if s == nil {
		return GatewayRecorder{}
	}
	requestIDShape := scanGatewayRequestID(input.GatewayRequestID)
	s.gate.RLock()
	defer s.gate.RUnlock()
	if !s.accepts(s, s.generation) {
		return GatewayRecorder{}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.accepting || s.releasing || s.manager.active.Load() != s {
		return GatewayRecorder{}
	}
	if s.activeTraces >= s.manager.cfg.maxActiveTraces {
		s.droppedTraceCount++
		return GatewayRecorder{}
	}
	if s.nextTraceSequence == math.MaxUint64 {
		s.droppedTraceCount++
		return GatewayRecorder{}
	}
	traceSequence := s.nextTraceSequence + 1
	candidateBytes, validCandidate := beginGatewayCandidateBytes(requestIDShape, s.generation, traceSequence)
	if !validCandidate {
		s.droppedTraceCount++
		return GatewayRecorder{}
	}
	plan := s.planBeginGatewayAllocationLocked(candidateBytes, 0)
	var allocation CaptureAllocation
	if !s.beginCaptureAllocationLocked(plan, &allocation) {
		s.droppedTraceCount++
		return GatewayRecorder{}
	}

	startedAt := input.StartedAt
	if startedAt.IsZero() {
		startedAt = s.manager.cfg.clock.WallNow()
	}
	candidate, failure := materializeBeginGatewayCandidate(
		s,
		&allocation,
		requestIDShape,
		traceSequence,
		startedAt,
	)
	if failure != gatewayMaterializationSucceeded {
		_ = candidate.rollbackLocked(&allocation)
		s.droppedTraceCount++
		if failure == gatewayMaterializationIDGenerationFailed {
			s.manager.cfg.logger.Warn("request capture gateway identifier generation failed",
				zap.String("session_id", s.id),
				zap.Uint64("generation", s.generation),
				zap.String("operation", "begin_gateway"),
				zap.String("reason", "id_generation_failed"),
			)
		}
		return GatewayRecorder{}
	}
	gateway := candidate.gateway
	if !s.commitBeginGatewayAllocationLocked(&allocation, &candidate) {
		_ = candidate.rollbackLocked(&allocation)
		s.droppedTraceCount++
		return GatewayRecorder{}
	}
	if requestIDShape.truncated {
		s.logMetadataTruncationLocked(gateway, nil, "gateway_request_id", maxRetainedIdentifierBytes)
	}
	return GatewayRecorder{
		manager:       s.manager,
		generation:    s.generation,
		traceSequence: gateway.traceSequence,
		handleSlot:    gateway.handleSlot,
	}
}

func (g *gatewayState) newMessageIDLocked() MessageLineage {
	if g == nil || g.session == nil || g.boundSession.Load() != g.session ||
		g.finished || !g.attached {
		return MessageLineage{}
	}
	session := g.session
	if g.pendingLineageCount >= maxPendingLineagesPerTrace || g.nextLineage == math.MaxUint64 {
		return MessageLineage{}
	}
	lineage := g.nextLineage + 1
	candidateBytes, validCandidate := pendingLineageCandidateBytes(
		session.generation,
		g.traceSequence,
		lineage,
	)
	if !validCandidate || candidateBytes > math.MaxInt64-g.charge {
		return MessageLineage{}
	}
	plan := session.planNewMessageIDAllocationLocked(candidateBytes)
	var allocation CaptureAllocation
	if !session.beginCaptureAllocationLocked(plan, &allocation) {
		return MessageLineage{}
	}
	candidate, materialized := materializePendingLineageCandidate(
		&allocation,
		session.generation,
		g.traceSequence,
		lineage,
	)
	if !materialized {
		_ = candidate.rollbackLocked(&allocation)
		return MessageLineage{}
	}
	if !g.commitPendingLineageAllocationLocked(&allocation, &candidate) {
		_ = candidate.rollbackLocked(&allocation)
		return MessageLineage{}
	}
	return MessageLineage{
		generation:    session.generation,
		traceSequence: g.traceSequence,
		lineage:       lineage,
	}
}

func (g *gatewayState) beginRecordLocked(
	protocol Protocol,
	attempt AttemptMetadata,
	raw RawRequest,
	targetInput redaction.Target,
) Recorder {
	if g == nil || g.session == nil || g.boundSession.Load() != g.session ||
		!g.session.accepting || g.finished || !g.attached {
		return Recorder{}
	}
	session := g.session
	normalizeAttempt(&attempt)
	g.nextExchange++
	exchangeIndex := g.nextExchange

	selectedProvider, selected := session.providers[attempt.Provider.ID]
	if !selected {
		return g.beginTransitionRecorderLocked(attempt, raw.CredentialEvidence, targetInput)
	}

	g.selectedProvider = true
	if session.activeRecords >= session.manager.cfg.maxActiveRecords {
		session.droppedExchangeCount++
		g.markHistoryTruncatedLocked(false, true)
		return Recorder{}
	}

	attempt.Provider = selectedProvider
	attempt, attemptTruncated := redaction.BoundedAttemptMetadata(attempt)
	requestResult := (redaction.Sanitizer{}).RequestDetailed(requestMetadata(raw), targetInput)
	provider, providerTruncated := redaction.SanitizedProvider(attempt, requestResult.Snapshot.URL)
	recordSequence := session.nextRecordSequence + 1
	recordIdentity := makeRecordIDValue(session.id, session.generation, recordSequence)
	recordID := recordIdentity.String()
	entrySequence := g.nextEntry + 1
	now := session.manager.cfg.clock.WallNow()
	summary := RecordSummary{
		SessionID:            session.id,
		RecordID:             recordID,
		GatewayTraceID:       g.id,
		GatewayRequestID:     g.requestID,
		ExchangeIndex:        exchangeIndex,
		RecordSequence:       recordSequence,
		Provider:             provider,
		Protocol:             protocol,
		SelectionMode:        attempt.SelectionMode,
		SelectionSource:      attempt.SelectionSource,
		ProviderAttemptIndex: attempt.ProviderAttemptIndex,
		CredentialPhase:      attempt.CredentialPhase,
		LifecycleState:       LifecycleStateActive,
		CaptureCompletion:    CaptureCompletionComplete,
		StartedAt:            now,
	}
	entrySnapshot := TraceEntry{
		Kind:                 TraceEntryRecord,
		EntryID:              makeTraceEntryID(session.generation, g.traceSequence, entrySequence),
		Sequence:             entrySequence,
		RecordID:             recordID,
		Provider:             provider,
		ProviderAttemptIndex: attempt.ProviderAttemptIndex,
		SelectionMode:        attempt.SelectionMode,
		SelectionSource:      attempt.SelectionSource,
		CredentialPhase:      attempt.CredentialPhase,
		MetadataTruncated:    attemptTruncated || providerTruncated,
	}
	recordCharge := estimateRecordCharge(requestResult.Snapshot, summary, requestResult.SensitiveNames)
	entryCharge := estimateTraceEntryCharge(entrySnapshot)
	if !session.reserveLocked(addRetainedCharge64(recordCharge, entryCharge), true) {
		session.droppedExchangeCount++
		g.markHistoryTruncatedLocked(false, true)
		return Recorder{}
	}
	session.nextRecordSequence = recordSequence
	g.nextEntry = entrySequence
	record := &recordState{
		session:              session,
		gateway:              g,
		id:                   recordID,
		generation:           session.generation,
		protocol:             protocol,
		charge:               recordCharge,
		request:              requestResult.Snapshot,
		credentialEvidence:   raw.CredentialEvidence,
		sensitiveHeaderNames: requestResult.SensitiveNames,
		redactAllHeaders:     requestResult.RedactAll,
		messageByID:          make(map[string]*messageState),
		summary:              summary,
	}
	record.boundSession.Store(session)
	if requestResult.Truncated || attemptTruncated || providerTruncated {
		record.markOverflowLocked()
		session.logMetadataTruncationLocked(g, record, "request", maxRetainedURLBytes)
	}

	if !g.sharedRequestInitialized {
		g.sharedRequestInitialized = true
		g.sharedRequestExpected = int64(len(raw.Body))
		g.sharedRequestComplete = len(raw.Body) == 0
		if len(raw.Body) > 0 {
			g.sharedRequest, g.sharedRequestComplete = newImmutableBlobLocked(session, raw.Body)
		}
	}
	if g.ingress != nil && protocol == ProtocolHTTP {
		record.request.Ingress = g.ingress
	}
	if (g.ingress == nil && int64(len(raw.Body)) != g.sharedRequestExpected) || !g.sharedRequestComplete || (g.ingress != nil && g.ingress.CaptureTruncated) {
		record.markOverflowLocked()
	}
	if g.sharedRequest != nil && retainBlobLocked(g.sharedRequest) {
		record.requestBody = g.sharedRequest
	}
	if _, claimed := session.claimRecordHandleSlotLocked(record); !claimed {
		session.releaseRecordLocked(record)
		session.releaseLocked(entryCharge)
		session.droppedExchangeCount++
		g.markHistoryTruncatedLocked(false, true)
		return Recorder{}
	}

	entry := &traceEntryState{snapshot: entrySnapshot, record: record, charge: entryCharge}
	record.traceEntry = entry
	g.appendEntryLocked(entry)
	g.liveRecords++
	session.appendRecordLocked(record)
	session.activeRecords++
	return Recorder{
		manager:        session.manager,
		generation:     session.generation,
		traceSequence:  g.traceSequence,
		recordSequence: recordSequence,
		entrySequence:  entrySequence,
		gatewaySlot:    g.handleSlot,
		recordSlot:     record.handleSlot,
		recordID:       recordIdentity,
		kind:           recorderKindRecord,
	}
}

func (g *gatewayState) beginTransitionRecorderLocked(attempt AttemptMetadata, evidence CredentialEvidence, targetInput redaction.Target) Recorder {
	session := g.session
	if g.activeTransition != nil {
		session.droppedExchangeCount++
		return Recorder{}
	}
	entry := g.appendTransitionTargetLocked(TransitionStart{
		Attempt:            attempt,
		CredentialEvidence: evidence,
	}, targetInput)
	if entry == nil {
		session.droppedExchangeCount++
		return Recorder{}
	}
	if !session.reserveLocked(transitionRecorderChargeBytes, true) {
		g.releaseEntryLocked(entry)
		g.markHistoryTruncatedLocked(false, true)
		session.droppedExchangeCount++
		return Recorder{}
	}
	entry.charge += transitionRecorderChargeBytes
	stub := &transitionRecorderState{
		session:            session,
		gateway:            g,
		entry:              entry,
		generation:         session.generation,
		credentialEvidence: evidence,
	}
	stub.boundSession.Store(session)
	entry.stubOwner = stub
	g.activeTransition = stub
	return Recorder{
		manager:       session.manager,
		generation:    session.generation,
		traceSequence: g.traceSequence,
		entrySequence: entry.snapshot.Sequence,
		gatewaySlot:   g.handleSlot,
		kind:          recorderKindTransition,
	}
}

func (g *gatewayState) appendTransitionTargetLocked(
	input TransitionStart,
	targetInput redaction.Target,
) *traceEntryState {
	session := g.session
	if g.transitionCount >= session.manager.cfg.maxTransitionsPerTrace {
		g.markHistoryTruncatedLocked(false, true)
		session.droppedTransitionCount++
		return nil
	}
	normalizeAttempt(&input.Attempt)
	if selected, exists := session.providers[input.Attempt.Provider.ID]; exists {
		input.Attempt.Provider = selected
	}
	var attemptTruncated bool
	input.Attempt, attemptTruncated = redaction.BoundedAttemptMetadata(input.Attempt)
	target := (redaction.Sanitizer{}).TargetWithEvidence(targetInput, input.CredentialEvidence).Target
	provider, providerTruncated := redaction.SanitizedProvider(input.Attempt, target.Value)
	failure, hasFailure := (redaction.Sanitizer{}).FailureDetailed(input.Failure, input.CredentialEvidence, false)
	termination := retainedTerminationReason(input.TerminationReason)
	entrySequence := g.nextEntry + 1
	entrySnapshot := TraceEntry{
		Kind:                 TraceEntryTransition,
		EntryID:              makeTraceEntryID(session.generation, g.traceSequence, entrySequence),
		Sequence:             entrySequence,
		Provider:             provider,
		ProviderAttemptIndex: input.Attempt.ProviderAttemptIndex,
		SelectionMode:        input.Attempt.SelectionMode,
		SelectionSource:      input.Attempt.SelectionSource,
		CredentialPhase:      input.Attempt.CredentialPhase,
		TerminationReason:    TerminationReason(termination.Value),
		Failure:              failure,
		HasFailure:           hasFailure,
		MetadataTruncated: attemptTruncated || providerTruncated || target.Truncated ||
			termination.Truncated || failure.Truncated,
	}
	charge := estimateTraceEntryCharge(entrySnapshot)
	if !session.reserveLocked(charge, true) {
		g.markHistoryTruncatedLocked(false, true)
		session.droppedTransitionCount++
		return nil
	}
	g.nextEntry = entrySequence
	entry := &traceEntryState{snapshot: entrySnapshot, charge: charge}
	g.appendEntryLocked(entry)
	if entrySnapshot.MetadataTruncated {
		session.logMetadataTruncationLocked(g, nil, "transition", maxRetainedErrorBytes)
	}
	return entry
}

func (g *gatewayState) finishLocked(outcome GatewayOutcome) {
	if g == nil || g.session == nil || g.boundSession.Load() != g.session ||
		g.finished || !g.attached {
		return
	}
	session := g.session
	g.finished = true
	if session.activeTraces > 0 {
		session.activeTraces--
	}
	fallbackReason := outcome.TerminationReason
	if fallbackReason == "" {
		fallbackReason = TerminationReasonGatewayFinished
	}
	for entry := g.entryFirst; entry != nil; entry = entry.after {
		if stub := entry.stubOwner; stub != nil && !stub.completed {
			stub.finishLocked(Outcome{
				SourceCompletion:   SourceCompletionPartial,
				TerminationReason:  fallbackReason,
				Failure:            outcome.Failure,
				CredentialEvidence: outcome.CredentialEvidence,
			})
		}
		record := entry.record
		if record == nil || record.completed || record.evicted {
			continue
		}
		session.logGatewayFinalizerLocked(g, record, fallbackReason)
		record.finishLocked(Outcome{
			SourceCompletion:   SourceCompletionPartial,
			TerminationReason:  fallbackReason,
			Failure:            outcome.Failure,
			CredentialEvidence: outcome.CredentialEvidence,
		}, false)
	}
	for providerID := range session.providerRecords {
		session.enforceProviderRetentionLocked(providerID)
	}
	if g.sharedRequest != nil {
		sharedRequest := g.sharedRequest
		g.sharedRequest = nil
		g.ingressBuilder = blobBuilder{}
		releaseBlobLocked(sharedRequest)
	}
	if !g.selectedProvider || g.liveRecords == 0 {
		session.releaseTraceLocked(g)
	}
}

func (r *recordState) observeClientWriteLocked(bytes int) {
	if r == nil || r.session == nil || bytes <= 0 || !r.mutableLocked() {
		return
	}
	if !r.responseObserved {
		r.stateFaultLocked("client_write_before_response")
		return
	}
	r.writtenBytes += int64(bytes)
	r.syncCountersLocked()
}

func (r *recordState) messageReadLocked(input MessageRead) MessageRef {
	if r == nil || r.session == nil || !r.mutableLocked() {
		return MessageRef{}
	}
	session := r.session
	if r.protocol != ProtocolWebSocket {
		r.stateFaultLocked("message_on_non_websocket_record")
		return MessageRef{}
	}
	if r.wsHandshake == nil || r.wsHandshake.StatusCode != http.StatusSwitchingProtocols {
		r.stateFaultLocked("message_before_successful_websocket_handshake")
		return MessageRef{}
	}
	direction, directionValid := canonicalMessageDirection(input.Direction)
	if !directionValid {
		r.stateFaultLocked("invalid_message_direction")
		return MessageRef{}
	}
	messageType, typeValid := canonicalMessageType(input.Type)
	if !typeValid {
		r.stateFaultLocked("invalid_message_type")
		return MessageRef{}
	}
	if input.Source == "" {
		input.Source = MessageSourceLive
	}
	source, sourceValid := canonicalMessageSource(input.Source)
	if !sourceValid {
		r.stateFaultLocked("invalid_message_source")
		return MessageRef{}
	}
	input.Direction = direction
	input.Type = messageType
	input.Source = source

	lineage := input.Lineage
	var pendingLineage *pendingLineageState
	if lineage.Valid() && lineage.generation == session.generation &&
		lineage.traceSequence == r.gateway.traceSequence &&
		!r.hasMessageLineageLocked(lineage.lineage) {
		pendingLineage = r.gateway.findPendingLineageLocked(lineage.lineage)
	}
	if r.gateway.nextMessageSequence == math.MaxUint64 {
		r.consumeDeniedMessageLineageLocked(pendingLineage)
		r.markOverflowLocked()
		return MessageRef{}
	}
	sequence := r.gateway.nextMessageSequence + 1
	useFallbackLineage := pendingLineage == nil
	if useFallbackLineage {
		if r.gateway.nextLineage == math.MaxUint64 {
			r.markOverflowLocked()
			return MessageRef{}
		}
		lineage = MessageLineage{
			generation:    session.generation,
			traceSequence: r.gateway.traceSequence,
			lineage:       r.gateway.nextLineage + 1,
		}
	}
	sourceLineage := input.SourceLineage
	if !sourceLineage.Valid() || sourceLineage.generation != session.generation ||
		sourceLineage.traceSequence != r.gateway.traceSequence {
		sourceLineage = MessageLineage{}
	}
	if input.Direction == MessageDirectionUpstreamToClient {
		r.observedBytes += int64(len(input.Payload))
	}
	messageIDBytes := messageIDEncodedBytes(lineage.generation, lineage.traceSequence, lineage.lineage)
	sourceMessageIDBytes := 0
	if sourceLineage.Valid() {
		sourceMessageIDBytes = messageIDEncodedBytes(
			sourceLineage.generation,
			sourceLineage.traceSequence,
			sourceLineage.lineage,
		)
	}
	charge := addRetainedCharge(
		messageBaseChargeBytes,
		messageIDBytes+sourceMessageIDBytes,
	)
	if !session.reserveLocked(charge, true) {
		r.consumeDeniedMessageLineageLocked(pendingLineage)
		r.markOverflowLocked()
		r.syncCountersLocked()
		return MessageRef{}
	}

	// Canonical strings are materialized only after the retained message has won
	// admission. Proxy replay buffers keep the numeric lineage value instead.
	messageID := materializeMessageID(lineage.generation, lineage.traceSequence, lineage.lineage)
	sourceMessageID := ""
	if sourceLineage.Valid() {
		sourceMessageID = materializeMessageID(
			sourceLineage.generation,
			sourceLineage.traceSequence,
			sourceLineage.lineage,
		)
	}
	ref := MessageRef{
		manager:        session.manager,
		generation:     session.generation,
		traceSequence:  r.gateway.traceSequence,
		recordSequence: r.summary.RecordSequence,
		sequence:       sequence,
		lineage:        lineage.lineage,
	}
	payload, complete := newImmutableBlobLocked(session, input.Payload)
	message := &messageState{
		id:              messageID,
		lineage:         lineage.lineage,
		sequence:        sequence,
		relativeMillis:  session.manager.cfg.clock.WallNow().Sub(r.summary.StartedAt).Milliseconds(),
		direction:       input.Direction,
		messageType:     input.Type,
		source:          input.Source,
		sourceMessageID: sourceMessageID,
		observedSize:    len(input.Payload),
		payload:         payload,
		charge:          charge,
	}
	r.gateway.nextMessageSequence = sequence
	if useFallbackLineage {
		r.gateway.nextLineage = lineage.lineage
	}
	r.messages = append(r.messages, message)
	r.messageByID[messageID] = message
	if !complete {
		r.markOverflowLocked()
	}
	r.syncCountersLocked()
	return ref
}

func (r *recordState) consumeDeniedMessageLineageLocked(pending *pendingLineageState) {
	if r == nil || r.gateway == nil || pending == nil {
		return
	}
	// A pending lineage is a one-shot allocation capability. Once the read has
	// reached admission, retaining that capability after denial would leave
	// charged graph ownership alive without any publishable message to consume it.
	r.gateway.removePendingLineageLocked(pending)
}

func (r *recordState) hasMessageLineageLocked(lineage uint64) bool {
	for _, message := range r.messages {
		if message != nil && message.lineage == lineage {
			return true
		}
	}
	return false
}

func (r *transitionRecorderState) finishLocked(outcome Outcome) {
	if r == nil || r.completed || r.entry == nil || r.entry.stubOwner != r {
		return
	}
	session := r.session
	termination := retainedTerminationReason(outcome.TerminationReason)
	failure, hasFailure := (redaction.Sanitizer{}).FailureDetailed(
		outcome.Failure, r.credentialEvidence, false,
	)
	charge := addRetainedCharge64(int64(len(termination.Value)), estimateFailureCharge(failure))
	if session.reserveLocked(charge, false) {
		r.entry.charge += charge
		r.entry.snapshot.TerminationReason = TerminationReason(termination.Value)
		r.entry.snapshot.Failure = failure
		r.entry.snapshot.HasFailure = hasFailure
	} else {
		termination.Truncated = termination.Truncated || len(termination.Value) > 0
		failure.Truncated = failure.Truncated || hasFailure
	}
	r.entry.snapshot.MetadataTruncated = r.entry.snapshot.MetadataTruncated ||
		termination.Truncated || failure.Truncated
	if r.entry.snapshot.MetadataTruncated {
		session.logMetadataTruncationLocked(r.gateway, nil, "transition_finish", maxRetainedErrorBytes)
	}
	if r.gateway != nil && r.gateway.activeTransition == r {
		r.gateway.activeTransition = nil
	}
	r.completed = true
}

func (r *recordState) mutableLocked() bool {
	return !r.completed && !r.disabled && !r.evicted &&
		r.session.accepting && r.session.manager.active.Load() == r.session
}

func (r *recordState) markOverflowLocked() {
	r.summary.CaptureCompletion = CaptureCompletionOverflowed
	if !r.overflowCounted {
		r.overflowCounted = true
		r.session.overflowedCount++
	}
}

func (r *recordState) syncCountersLocked() {
	r.summary.UpstreamObservedBytes = r.observedBytes
	r.summary.ApplicationWriteConfirmedBytes = r.writtenBytes
}
