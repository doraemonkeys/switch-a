package requestcapture

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"hash"
	"math"
	"sync/atomic"
	"unsafe"

	"github.com/doraemonkeys/switch-a/internal/requestcapture/redaction"
	"go.uber.org/zap"
)

const (
	chunkMetadataChargeBytes   = int64(unsafe.Sizeof(blobChunk{}))
	blobViewSegmentChargeBytes = int64(unsafe.Sizeof(blobViewSegment{}))
)

type blobChunk struct {
	session *sessionState
	data    []byte
	next    *blobChunk
	charge  int64
	refs    atomic.Int64
	pins    atomic.Int64
	sealed  bool
}

type blob struct {
	session    *sessionState
	first      *blobChunk
	last       *blobChunk
	size       int64
	chunkCount int
	refs       int
	charge     int64
	hasher     hash.Hash
}

type blobBuilder struct {
	value      *blob
	overflowed bool
}

type blobViewSegment struct {
	owner *blobChunk
	data  []byte
}

type blobView struct {
	session  *sessionState
	segments []blobViewSegment
	size     int64
	checksum [sha256.Size]byte
	failure  error
}

func newBlobLocked(session *sessionState) (*blob, bool) {
	// hash.Hash hides a separately allocated digest implementation. Charging it
	// explicitly keeps many tiny blobs from bypassing the process ceiling.
	charge := blobBaseChargeBytes + checksumStateChargeBytes
	if !session.reserveLocked(charge, true) {
		return nil, false
	}
	return &blob{
		session: session,
		refs:    1,
		charge:  charge,
		hasher:  sha256.New(),
	}, true
}

func newImmutableBlobLocked(session *sessionState, payload []byte) (*blob, bool) {
	if len(payload) == 0 {
		return nil, true
	}
	value, ok := newBlobLocked(session)
	if !ok {
		return nil, false
	}
	builder := blobBuilder{value: value}
	captured := builder.appendLocked(session, payload)
	return value, captured == len(payload)
}

func (b *blobBuilder) appendLocked(session *sessionState, payload []byte) int {
	if b == nil || b.overflowed || len(payload) == 0 {
		return 0
	}
	if b.value == nil {
		value, ok := newBlobLocked(session)
		if !ok {
			b.overflowed = true
			return 0
		}
		b.value = value
	}

	captured := 0
	for len(payload) > 0 {
		tail := b.value.last
		if tail != nil && !tail.sealed && len(tail.data) < cap(tail.data) {
			partSize := min(len(payload), cap(tail.data)-len(tail.data))
			tail.data = append(tail.data, payload[:partSize]...)
			_, _ = b.value.hasher.Write(payload[:partSize])
			b.value.size += int64(partSize)
			captured += partSize
			payload = payload[partSize:]
			continue
		}

		charge := int64(session.manager.cfg.chunkBytes) + chunkMetadataChargeBytes
		if !session.reserveLocked(charge, true) {
			b.overflowed = true
			break
		}
		partSize := min(len(payload), session.manager.cfg.chunkBytes)
		data := make([]byte, partSize, session.manager.cfg.chunkBytes)
		copy(data, payload[:partSize])
		chunk := &blobChunk{
			session: session,
			data:    data,
			charge:  charge,
		}
		chunk.refs.Store(1)
		if tail == nil {
			b.value.first = chunk
		} else {
			tail.next = chunk
		}
		b.value.last = chunk
		b.value.chunkCount++
		_, _ = b.value.hasher.Write(data)
		b.value.size += int64(partSize)
		captured += partSize
		payload = payload[partSize:]
	}
	return captured
}

func retainBlobLocked(value *blob) bool {
	if value == nil {
		return true
	}
	if value.refs <= 0 {
		return false
	}
	value.refs++
	return true
}

func releaseBlobLocked(value *blob) {
	if value == nil || value.refs <= 0 {
		return
	}
	value.refs--
	if value.refs != 0 {
		return
	}
	for chunk := value.first; chunk != nil; {
		next := chunk.next
		releaseChunkLocked(chunk, false)
		chunk = next
	}
	value.first = nil
	value.last = nil
	value.chunkCount = 0
	value.hasher = nil
	value.session.releaseLocked(value.charge)
	value.charge = 0
}

func releaseChunkLocked(chunk *blobChunk, pinned bool) {
	if chunk == nil {
		return
	}
	if chunk.refs.Load() <= 0 {
		logBlobInvariant(chunk.session, "release_chunk", "missing_ref_owner")
		return
	}
	if pinned {
		pins, ok := decrementPositiveAtomic(&chunk.pins)
		if !ok {
			logBlobInvariant(chunk.session, "release_chunk", "missing_pin_owner")
			return
		}
		if pins == 0 {
			chunk.session.unpinLocked(chunk.charge)
		}
	}
	refs, ok := decrementPositiveAtomic(&chunk.refs)
	if !ok {
		logBlobInvariant(chunk.session, "release_chunk", "lost_ref_owner")
		return
	}
	if refs == 0 {
		chunk.session.releaseLocked(chunk.charge)
		chunk.charge = 0
		chunk.data = nil
		chunk.next = nil
	}
}

func decrementPositiveAtomic(counter *atomic.Int64) (int64, bool) {
	for {
		current := counter.Load()
		if current <= 0 {
			return current, false
		}
		if counter.CompareAndSwap(current, current-1) {
			return current - 1, true
		}
	}
}

func logBlobInvariant(session *sessionState, operation, reason string) {
	if session == nil || session.manager == nil {
		return
	}
	session.manager.cfg.logger.Error("request capture blob invariant failed",
		zap.String("session_id", session.id),
		zap.Uint64("generation", session.generation),
		zap.String("operation", operation),
		zap.String("reason", reason),
	)
}

func snapshotBlobLocked(value *blob) blobView {
	return snapshotBlobPrefixLocked(value, math.MaxInt)
}

func snapshotBlobPrefixLocked(value *blob, maximumBytes int) blobView {
	if value == nil {
		return blobView{checksum: sha256.Sum256(nil)}
	}
	if maximumBytes < 0 {
		maximumBytes = 0
	}
	remaining := min64(value.size, int64(maximumBytes))
	chunkCount := 0
	for chunk := value.first; chunk != nil && remaining > 0; chunk = chunk.next {
		remaining -= int64(len(chunk.data))
		chunkCount++
	}
	if maximumBytes == math.MaxInt {
		chunkCount = value.chunkCount
	}

	segments := make([]blobViewSegment, chunkCount)
	chunk := value.first
	for index := range segments {
		if chunk == nil {
			releaseBlobSegmentsLocked(segments[:index])
			return failedBlobViewLocked(value, "broken_chunk_chain")
		}
		refs := chunk.refs.Add(1)
		if refs <= 1 {
			chunk.refs.Add(-1)
			releaseBlobSegmentsLocked(segments[:index])
			return failedBlobViewLocked(value, "released_chunk")
		}
		pins := chunk.pins.Add(1)
		if pins <= 0 {
			chunk.pins.Add(-1)
			chunk.refs.Add(-1)
			releaseBlobSegmentsLocked(segments[:index])
			return failedBlobViewLocked(value, "corrupt_pin_owner")
		}
		if pins == 1 {
			value.session.pinLocked(chunk.charge)
		}
		segments[index] = blobViewSegment{
			owner: chunk,
			data:  chunk.data[:len(chunk.data):len(chunk.data)],
		}
		if index+1 < len(segments) {
			chunk = chunk.next
		}
	}

	view := blobView{
		session:  value.session,
		segments: segments,
		size:     value.size,
	}
	var checksum [sha256.Size]byte
	copy(view.checksum[:], value.hasher.Sum(checksum[:0]))
	return view
}

func releaseBlobSegmentsLocked(segments []blobViewSegment) {
	for _, segment := range segments {
		releaseChunkLocked(segment.owner, true)
	}
}

func failedBlobViewLocked(value *blob, reason string) blobView {
	logBlobInvariant(value.session, "snapshot_prefix", reason)
	return blobView{
		session: value.session,
		size:    value.size,
		failure: ErrInternalFailure,
	}
}

func (v *blobView) release() {
	if v == nil || v.session == nil {
		return
	}
	session := v.session
	session.mu.Lock()
	defer session.mu.Unlock()
	releaseBlobViewLocked(v)
}

func previewBlob(value *blob, limit int) BlobPreview {
	if value == nil {
		empty := sha256.Sum256(nil)
		return previewSegments(nil, 0, limit, empty)
	}
	var checksum [sha256.Size]byte
	copy(checksum[:], value.hasher.Sum(checksum[:0]))
	return previewChunkChain(value.first, value.size, limit, checksum)
}

func previewView(value blobView, limit int) BlobPreview {
	return previewSegments(value.segments, value.size, limit, value.checksum)
}

func previewChunkChain(first *blobChunk, size int64, limit int, checksum [sha256.Size]byte) BlobPreview {
	if limit < 0 {
		limit = 0
	}
	previewSize := min64(size, int64(limit))
	raw := make([]byte, 0, int(previewSize))
	for chunk := first; chunk != nil && int64(len(raw)) < previewSize; chunk = chunk.next {
		remaining := int(previewSize) - len(raw)
		take := min(remaining, len(chunk.data))
		raw = append(raw, chunk.data[:take]...)
	}
	return blobPreviewFromRaw(raw, size, checksum)
}

func previewSegments(segments []blobViewSegment, size int64, limit int, checksum [sha256.Size]byte) BlobPreview {
	if limit < 0 {
		limit = 0
	}
	previewSize := min64(size, int64(limit))
	raw := make([]byte, 0, int(previewSize))
	for _, segment := range segments {
		if int64(len(raw)) == previewSize {
			break
		}
		remaining := int(previewSize) - len(raw)
		take := min(remaining, len(segment.data))
		raw = append(raw, segment.data[:take]...)
	}
	return blobPreviewFromRaw(raw, size, checksum)
}

func blobPreviewFromRaw(raw []byte, size int64, checksum [sha256.Size]byte) BlobPreview {
	return BlobPreview{
		DataBase64:     base64.StdEncoding.EncodeToString(raw),
		PreviewBytes:   int64(len(raw)),
		CapturedBytes:  size,
		Truncated:      int64(len(raw)) < size,
		ChecksumSHA256: hex.EncodeToString(checksum[:]),
	}
}

func min64(left, right int64) int64 {
	if left < right {
		return left
	}
	return right
}

func (r *recordState) observeUpstreamLocked(payload []byte) {
	if r == nil || r.session == nil || len(payload) == 0 || !r.mutableLocked() {
		return
	}
	session := r.session
	if !r.responseObserved {
		r.stateFaultLocked("upstream_payload_before_response")
		return
	}
	r.observedBytes += int64(len(payload))
	captured := r.responseBody.appendLocked(session, payload)
	if captured != len(payload) {
		r.markOverflowLocked()
	}
	r.syncCountersLocked()
}

func normalizedRecordOutcome(outcome Outcome, captureDisabled bool) Outcome {
	if captureDisabled {
		outcome.SourceCompletion = SourceCompletionPartial
		outcome.TerminationReason = TerminationReasonCaptureFault
	}
	if outcome.SourceCompletion != "" {
		return outcome
	}
	switch outcome.TerminationReason {
	case TerminationReasonEOF, TerminationReasonWebSocketClose:
		outcome.SourceCompletion = SourceCompletionComplete
	default:
		outcome.SourceCompletion = SourceCompletionPartial
	}
	return outcome
}

func (r *recordState) retainResponseTrailersLocked(outcome Outcome, allowMetadataEviction bool) {
	if r.httpResponse == nil || len(outcome.ResponseTrailers) == 0 {
		return
	}
	evidence := outcome.CredentialEvidence
	trailers := (redaction.Sanitizer{}).HeadersWithEvidence(
		outcome.ResponseTrailers,
		r.sensitiveHeaderNames,
		evidence,
		r.redactAllHeaders || evidence.Overflowed(),
	)
	charge := estimateHeaderCharge(trailers.Value)
	if r.session.reserveLocked(charge, allowMetadataEviction) {
		r.charge += charge
		r.httpResponse.Trailers = trailers.Value
	} else {
		trailers.Truncated = true
	}
	if trailers.Truncated {
		r.markOverflowLocked()
		r.session.logMetadataTruncationLocked(r.gateway, r, "response_trailers", maxRetainedHeaderBytes)
	}
	// Trailer credentials can change the fail-closed policy even when their
	// optional snapshot could not be retained.
	r.redactAllHeaders = r.redactAllHeaders || trailers.RedactAll || trailers.Discovered
}

func (r *recordState) retainWebSocketCloseLocked(
	observation *WebSocketCloseObservation,
	evidence CredentialEvidence,
	allowMetadataEviction bool,
) {
	if observation == nil {
		return
	}
	if r.protocol != ProtocolWebSocket {
		r.stateFaultLocked("websocket_close_on_non_websocket_record")
		return
	}
	direction, valid := canonicalMessageDirection(observation.Direction)
	if !valid {
		r.stateFaultLocked("invalid_websocket_close_direction")
		return
	}
	observation.Direction = direction
	reason := redaction.TextSanitization{}
	if (r.redactAllHeaders || !evidence.Sealed() || evidence.Overflowed()) && observation.Reason != "" {
		reason = redaction.TextSanitization{Value: redaction.RedactedValue, Truncated: true}
	} else {
		reason = redaction.SanitizedTextWithEvidence(
			observation.Reason,
			evidence,
			maxRetainedCloseReasonBytes,
			"WEBSOCKET_CLOSE_REASON",
		)
	}
	snapshot := &WebSocketCloseSnapshot{
		Direction: direction,
		Code:      observation.Code,
		Clean:     observation.Clean,
	}
	if r.session.reserveLocked(int64(len(reason.Value)), allowMetadataEviction) {
		r.charge += int64(len(reason.Value))
		snapshot.Reason = reason.Value
		snapshot.ReasonTruncated = reason.Truncated
	} else {
		reason.Truncated = true
		snapshot.ReasonTruncated = true
	}
	r.wsClose = snapshot
	if reason.Truncated {
		r.markOverflowLocked()
		r.session.logMetadataTruncationLocked(r.gateway, r, "websocket_close_reason", maxRetainedCloseReasonBytes)
	}
}

func (r *recordState) finishLocked(outcome Outcome, enforceRetention bool) {
	session := r.session
	outcome = normalizedRecordOutcome(outcome, r.disabled)
	r.retainResponseTrailersLocked(outcome, enforceRetention)
	r.retainWebSocketCloseLocked(outcome.WebSocketClose, outcome.CredentialEvidence, enforceRetention)
	failure, hasFailure := (redaction.Sanitizer{}).FailureDetailed(
		outcome.Failure, outcome.CredentialEvidence, r.redactAllHeaders,
	)
	if hasFailure {
		charge := estimateFailureCharge(failure)
		if session.reserveLocked(charge, enforceRetention) {
			r.charge += charge
			r.summary.Failure = failure
			r.summary.HasFailure = true
		} else {
			failure.Truncated = true
		}
	}
	termination := retainedTerminationReason(outcome.TerminationReason)
	sourceCompletion := retainedSourceCompletion(outcome.SourceCompletion)
	if session.reserveLocked(int64(len(termination.Value)+len(sourceCompletion.Value)), enforceRetention) {
		r.charge += int64(len(termination.Value) + len(sourceCompletion.Value))
	} else {
		termination = redaction.TextSanitization{Value: string(TerminationReasonGatewayFinished), Truncated: true}
		sourceCompletion = redaction.TextSanitization{Value: string(SourceCompletionPartial), Truncated: true}
	}
	completedAt := outcome.CompletedAt
	if completedAt.IsZero() {
		completedAt = session.manager.cfg.clock.WallNow()
	}
	r.summary.LifecycleState = LifecycleStateCompleted
	r.summary.SourceCompletion = SourceCompletion(sourceCompletion.Value)
	r.summary.TerminationReason = TerminationReason(termination.Value)
	r.summary.CompletedAt = &completedAt
	if failure.Truncated || termination.Truncated || sourceCompletion.Truncated {
		r.markOverflowLocked()
		session.logMetadataTruncationLocked(r.gateway, r, "outcome", maxRetainedErrorBytes)
	}
	r.completed = true
	if r.responseBody.overflowed {
		r.markOverflowLocked()
	}
	r.syncCountersLocked()
	if session.activeRecords > 0 {
		session.activeRecords--
	}
	providerID := r.summary.Provider.ID
	session.appendProviderRecordLocked(providerID, r)
	if enforceRetention {
		session.enforceProviderRetentionLocked(providerID)
	}
}

func (s *sessionState) releaseRecordLocked(record *recordState) {
	if record == nil {
		return
	}
	_ = s.releaseRecordHandleSlotLocked(record)
	record.boundSession.Store(nil)

	requestBody := record.requestBody
	record.requestBody = nil
	responseBody := record.responseBody.value
	record.responseBody.value = nil
	messages := record.messages
	record.messages = nil
	record.messageByID = nil

	releaseBlobLocked(requestBody)
	releaseBlobLocked(responseBody)
	for _, message := range messages {
		if message == nil {
			continue
		}
		payload := message.payload
		message.payload = nil
		releaseBlobLocked(payload)
		messageCharge := message.charge
		message.id = ""
		message.lineage = 0
		message.sequence = 0
		message.relativeMillis = 0
		message.direction = ""
		message.messageType = ""
		message.source = ""
		message.sourceMessageID = ""
		message.disposition = ""
		message.clientVisible = false
		message.resultSet = false
		message.observedSize = 0
		message.failure = FailureObservation{}
		message.hasFailure = false
		message.charge = 0
		s.releaseLocked(messageCharge)
	}

	recordCharge := record.charge
	record.session = nil
	record.gateway = nil
	record.id = ""
	record.generation = 0
	record.protocol = ""
	record.charge = 0
	record.traceEntry = nil
	record.disabled = false
	record.completed = false
	record.evicted = false
	record.overflowCounted = false
	record.summary = RecordSummary{}
	record.request = RequestSnapshot{}
	record.responseBody = blobBuilder{}
	record.older = nil
	record.newer = nil
	record.providerBefore = nil
	record.providerAfter = nil
	record.httpResponse = nil
	record.wsHandshake = nil
	record.wsClose = nil
	record.sensitiveHeaderNames = nil
	record.redactAllHeaders = false
	record.responseObserved = false
	record.stateFaultLogged = false
	record.observedBytes = 0
	record.writtenBytes = 0
	s.releaseLocked(recordCharge)
}
