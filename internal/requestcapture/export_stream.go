package requestcapture

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"time"

	"github.com/doraemonkeys/switch-a/internal/requestcapture/exportwire"
	"go.uber.org/zap"
)

type exportStreamWriter = exportwire.Writer
type streamDigest = exportwire.Digest

type metadataChunkStream struct {
	stream *exportwire.MetadataStream
}

func newMetadataChunkStream(
	writer *exportStreamWriter,
	manifest bool,
	recordIndex int,
) *metadataChunkStream {
	return &metadataChunkStream{
		stream: exportwire.NewMetadataStream(writer, manifest, recordIndex),
	}
}

func (stream *metadataChunkStream) writeByte(value byte) error {
	return stream.stream.WriteByte(value)
}

func (stream *metadataChunkStream) writeString(value string) error {
	return stream.stream.WriteString(value)
}

func (stream *metadataChunkStream) writeBytes(value []byte) error {
	return stream.stream.WriteBytes(value)
}

func (stream *metadataChunkStream) finish() (streamDigest, error) {
	return stream.stream.Finish()
}

func newExportStreamWriter(
	destination io.Writer,
	lineStorage []byte,
	metadataBuffer []byte,
	check func() error,
) exportStreamWriter {
	return exportwire.NewWriter(
		destination,
		lineStorage,
		metadataBuffer,
		RawPayloadRiskNotice,
		check,
	)
}

func exportBlobRawFragmentBytes(lineBytes int) int {
	return exportwire.BlobRawFragmentBytes(lineBytes)
}

func minimumExportLineBytes() int {
	return exportwire.MinimumLineBytes()
}

func exportWorkspaceSizing(lineBytes int) (int, int64, bool) {
	return exportwire.WorkspaceSizing(lineBytes)
}

func (m *Manager) writeDownload(
	slot int,
	epoch uint64,
	ctx context.Context,
	destination io.Writer,
) (result error) {
	if m == nil {
		return ErrDownloadUnavailable
	}
	if ctx == nil {
		ctx = context.Background()
	}
	probe := newExportCancellationProbe(ctx, nil)
	state, workspace, lineBytes, err := m.beginDownloadStream(slot, epoch)
	if err != nil {
		return err
	}
	defer func() {
		if finishErr := state.finishDownload(result); finishErr != nil {
			result = finishErr
		}
	}()

	if destination == nil || lineBytes <= 0 || len(workspace) <= lineBytes {
		return ErrDownloadUnavailable
	}
	stream := newExportStreamWriter(
		destination,
		workspace[:lineBytes],
		workspace[lineBytes:],
		func() error {
			if err := probe.lockedError(); err != nil {
				return resolveExportCancellation(ctx, err)
			}
			if state.canceled.Load() || state.manager.active.Load() != state.session {
				return ErrExportCanceled
			}
			return nil
		},
	)
	return state.writeSnapshot(&stream)
}

func (m *Manager) beginDownloadStream(
	slot int,
	epoch uint64,
) (*exportState, []byte, int, error) {
	m.exportMu.Lock()
	_, state, occupied := m.exports.Entry(slot)
	if !occupied || state == nil || epoch == 0 || state.downloadEpoch != epoch ||
		!state.downloadSlotOwned || state.phase != exportPhaseClaimed {
		m.exportMu.Unlock()
		return nil, nil, 0, ErrDownloadUnavailable
	}
	if state.canceled.Load() {
		if !state.expiryOwner {
			m.removeExportLocked(state.registryKey, state)
		}
		state.phase = exportPhaseReleased
		m.exportMu.Unlock()
		state.release("download_start_canceled")
		return nil, nil, 0, ErrDownloadUnavailable
	}
	state.phase = exportPhaseStreaming
	workspace := state.workspace
	lineBytes := state.lineBytes
	m.exportMu.Unlock()
	return state, workspace, lineBytes, nil
}

func (m *Manager) closeDownload(slot int, epoch uint64) {
	if m == nil {
		return
	}
	m.exportMu.Lock()
	_, state, occupied := m.exports.Entry(slot)
	if !occupied || state == nil || epoch == 0 || state.downloadEpoch != epoch ||
		!state.downloadSlotOwned {
		m.exportMu.Unlock()
		return
	}
	switch state.phase {
	case exportPhaseClaimed:
		if !state.expiryOwner {
			m.removeExportLocked(state.registryKey, state)
		}
		state.cancelLocked(nil)
		state.phase = exportPhaseReleased
		m.exportMu.Unlock()
		state.release("download_abandoned")
	case exportPhaseStreaming:
		// The stack executing writeDownload is the physical release owner.
		state.cancelLocked(nil)
		m.exportMu.Unlock()
	default:
		m.exportMu.Unlock()
	}
}

const exportSessionLockRetryInterval = time.Millisecond

// cancelLocked requires manager.exportMu. The first cancellation is immutable:
// acquisition readers synchronize through canceled and therefore never race a
// later cause replacement.
func (state *exportState) cancelLocked(cause error) {
	if state == nil || state.canceled.Load() {
		return
	}
	state.acquisitionErr = cause
	state.canceled.Store(true)
	if state.done != nil {
		state.cancellationOnce.Do(func() {
			close(state.done)
		})
	}
}

func (state *exportState) invariantFactsLocked(fault exportInvariantFault) exportInvariantFacts {
	if state == nil {
		return exportInvariantFacts{fault: fault}
	}
	return exportInvariantFacts{
		fault:                 fault,
		sessionID:             state.sessionID,
		exportID:              state.id,
		registryKey:           state.registryKey,
		phase:                 state.phase,
		selectionMaterialized: state.selectionMaterialized.Load(),
	}
}

func (state *exportState) failInvariant(fault exportInvariantFault) {
	if state == nil || state.manager == nil || fault == exportInvariantNone {
		return
	}
	manager := state.manager
	manager.exportMu.Lock()
	state.cancelLocked(ErrInternalFailure)
	facts := state.invariantFactsLocked(fault)
	manager.exportMu.Unlock()
	manager.logExportInvariant(facts)
}

func (state *exportState) failInvariantWithFacts(
	fault exportInvariantFault,
	facts exportInvariantFacts,
) {
	if state == nil || state.manager == nil || fault == exportInvariantNone {
		return
	}
	manager := state.manager
	manager.exportMu.Lock()
	state.cancelLocked(ErrInternalFailure)
	manager.exportMu.Unlock()
	facts.fault = fault
	manager.logExportInvariant(facts)
}

func (m *Manager) logExportInvariant(facts exportInvariantFacts) {
	operation, reason := exportInvariantDescription(facts.fault)
	m.cfg.logger.Error("request capture export invariant failed",
		zap.String("session_id", facts.sessionID),
		zap.String("export_id", facts.exportID),
		zap.String("registry_key", facts.registryKey),
		zap.Int("phase", int(facts.phase)),
		zap.Bool("selection_materialized", facts.selectionMaterialized),
		zap.String("operation", operation),
		zap.String("reason", reason),
	)
}

func exportInvariantDescription(fault exportInvariantFault) (string, string) {
	switch fault {
	case exportInvariantAttachProcessAccount:
		return "attach_lease", "process_lease_account_mismatch"
	case exportInvariantAttachSessionAccount:
		return "attach_lease", "session_lease_account_mismatch"
	case exportInvariantSelectionAccount:
		return "release_selection", "selection_charge_mismatch"
	case exportInvariantSelectionPin:
		return "release_selection", "selection_pin_mismatch"
	case exportInvariantSelectionTemporary:
		return "release_selection", "selection_temporary_mismatch"
	case exportInvariantSelectionUnpin:
		return "release_selection", "selection_pin_release_failed"
	case exportInvariantAcquisitionAccount:
		return "release_acquisition", "acquisition_charge_mismatch"
	case exportInvariantAcquisitionPin:
		return "release_acquisition", "acquisition_pin_mismatch"
	case exportInvariantAcquisitionTemporary:
		return "release_acquisition", "acquisition_temporary_mismatch"
	case exportInvariantAcquisitionUnpin:
		return "release_acquisition", "acquisition_pin_release_failed"
	case exportInvariantDownloadSlot:
		return "release_accounting", "download_slot_mismatch"
	case exportInvariantDownloadAccount:
		return "release_accounting", "download_account_mismatch"
	case exportInvariantLeaseAccount:
		return "release_accounting", "lease_account_mismatch"
	case exportInvariantAttachedLeasePin:
		return "release_accounting", "attached_pin_account_mismatch"
	case exportInvariantAcquiringLeaseAccount:
		return "release_accounting", "acquiring_lease_account_mismatch"
	case exportInvariantSessionOwner:
		return "release_owner", "session_owner_release_failed"
	case exportInvariantUnexpectedStreamPhase:
		return "finish_download", "unexpected_stream_phase"
	default:
		return "unknown", "unknown_export_invariant"
	}
}

const (
	requestBodyBlobID   = "request_body"
	responseBodyBlobID  = "response_body"
	handshakeBodyBlobID = "handshake_body"
	messageBlobPrefix   = "message_payload:"
)

type exportSnapshot struct {
	session      *sessionState
	sessionID    string
	traces       []GatewayTraceSummary
	records      []exportRecordSnapshot
	chargedBytes int64
}

type exportRecordSnapshot struct {
	summary       RecordSummary
	snapshotState SnapshotState
	traceIndex    int
	request       RequestSnapshot
	http          *exportHTTPSnapshot
	websocket     *exportWebSocketSnapshot
	blobs         []exportBlobSnapshot
}

type exportHTTPSnapshot struct {
	response *HTTPResponseSnapshot
}

type exportWebSocketSnapshot struct {
	handshake *WebSocketHandshakeSnapshot
	close     *WebSocketCloseSnapshot
	messages  []exportMessageSnapshot
}

type exportMessageSnapshot struct {
	messageID            string
	sequence             uint64
	relativeMillis       int64
	direction            MessageDirection
	messageType          MessageType
	source               MessageSource
	sourceMessageID      string
	disposition          MessageDisposition
	clientVisible        bool
	failure              FailureObservation
	hasFailure           bool
	observedPayloadBytes int64
	payloadBlobIndex     int
	payloadBlobID        string
}

type exportBlobSnapshot struct {
	id   string
	view blobView
}

const (
	exportSnapshotOwnedBaseChargeBytes      int64 = 512
	exportSnapshotOwnedRecordChargeBytes    int64 = 768
	exportSnapshotOwnedTraceChargeBytes     int64 = 256
	exportSnapshotOwnedEntryChargeBytes     int64 = 512
	exportSnapshotOwnedBlobChargeBytes      int64 = 128
	exportSnapshotOwnedHTTPChargeBytes      int64 = 256
	exportSnapshotOwnedWebSocketChargeBytes int64 = 256
	exportSnapshotOwnedMessageChargeBytes   int64 = 256
)

func materializeExportRecordSnapshot(
	probe exportCancellationProbe,
	source *exportReadSource,
	recordIndex int,
) (exportRecordSnapshot, error) {
	record := &source.records[recordIndex]
	result := exportRecordSnapshot{
		summary:       cloneRecordSummary(record.summary),
		snapshotState: record.snapshotState,
		traceIndex:    record.traceIndex,
		request:       cloneRequestSnapshot(record.request),
	}
	requestBody, err := record.requestBody.materialize(probe)
	if err != nil {
		return result, err
	}
	result.blobs = append(result.blobs, exportBlobSnapshot{id: requestBodyBlobID, view: requestBody})

	switch record.protocol {
	case ProtocolHTTP:
		result.http = &exportHTTPSnapshot{}
		if record.hasHTTP {
			response := cloneHTTPResponse(&record.httpResponse)
			result.http.response = response
		}
		responseBody, err := record.responseBody.materialize(probe)
		if err != nil {
			return result, err
		}
		result.blobs = append(result.blobs, exportBlobSnapshot{id: responseBodyBlobID, view: responseBody})
	case ProtocolWebSocket:
		websocket := &exportWebSocketSnapshot{
			messages: make([]exportMessageSnapshot, 0, record.messageCount),
		}
		if record.hasWSHandshake {
			websocket.handshake = cloneWebSocketHandshake(&record.wsHandshake)
		}
		if record.hasWSClose {
			websocket.close = cloneWebSocketClose(&record.wsClose)
		}
		result.websocket = websocket
		handshakeBody, err := record.responseBody.materialize(probe)
		if err != nil {
			return result, err
		}
		result.blobs = append(result.blobs, exportBlobSnapshot{id: handshakeBodyBlobID, view: handshakeBody})
		messageEnd := record.messageOffset + record.messageCount
		for messageIndex := record.messageOffset; messageIndex < messageEnd; messageIndex++ {
			if err := probe.lockedError(); err != nil {
				return result, err
			}
			message := &source.messages[messageIndex]
			payload, err := message.payload.materialize(probe)
			if err != nil {
				return result, err
			}
			blobID := messagePayloadBlobIDFromSource(*message)
			blobIndex := len(result.blobs)
			websocket.messages = append(websocket.messages, exportMessageSnapshot{
				messageID:            message.messageID,
				sequence:             message.sequence,
				relativeMillis:       message.relativeMillis,
				direction:            message.direction,
				messageType:          message.messageType,
				source:               message.source,
				sourceMessageID:      message.sourceMessageID,
				disposition:          message.disposition,
				clientVisible:        message.clientVisible,
				failure:              message.failure,
				hasFailure:           message.hasFailure,
				observedPayloadBytes: message.observedPayloadBytes,
				payloadBlobIndex:     blobIndex,
				payloadBlobID:        blobID,
			})
			result.blobs = append(result.blobs, exportBlobSnapshot{id: blobID, view: payload})
		}
	}
	if err := probe.lockedError(); err != nil {
		return result, err
	}
	return result, nil
}

func messagePayloadBlobIDFromSource(message exportMessageSource) string {
	id := message.messageID
	if id == "" {
		id = strconv.FormatUint(message.sequence, 10)
	}
	return messageBlobPrefix + id
}

func messagePayloadBlobIDSourceBytes(message exportMessageSource) int {
	if message.messageID != "" {
		return len(messageBlobPrefix) + len(message.messageID)
	}
	digits := 1
	for value := message.sequence; value >= 10; value /= 10 {
		digits++
	}
	return len(messageBlobPrefix) + digits
}

func clearExportTraceMarksLocked(
	session *sessionState,
	selection *exportSelection,
	state *exportState,
) {
	for record := session.oldestRecord; record != nil; record = record.newer {
		if !selection.selectsRecord(record) || record.gateway == nil {
			continue
		}
		if record.gateway.exportSnapshotOwner == state {
			record.gateway.exportSnapshotOwner = nil
			record.gateway.exportSnapshotIndex = 0
			record.gateway.exportSnapshotMaterialized = false
		}
	}
}

func cloneWebSocketClose(source *WebSocketCloseSnapshot) *WebSocketCloseSnapshot {
	if source == nil {
		return nil
	}
	cloned := *source
	return &cloned
}

func saturatedChargeAdd(total, addition int64) int64 {
	if addition <= 0 {
		return total
	}
	if total > math.MaxInt64-addition {
		return math.MaxInt64
	}
	return total + addition
}

func (snapshot *exportSnapshot) release() {
	if snapshot == nil || snapshot.session == nil {
		return
	}
	session := snapshot.session
	session.mu.Lock()
	snapshot.releaseLocked()
	session.mu.Unlock()
}

func (snapshot *exportSnapshot) releaseLocked() {
	if snapshot == nil || snapshot.session == nil {
		return
	}
	session := snapshot.session
	for index := range snapshot.records {
		record := &snapshot.records[index]
		for blobIndex := range record.blobs {
			releaseBlobViewLocked(&record.blobs[blobIndex].view)
		}
		record.blobs = nil
		record.websocket = nil
		record.http = nil
	}
	snapshot.records = nil
	snapshot.traces = nil
	session.unpinLocked(snapshot.chargedBytes)
	session.releaseLocked(snapshot.chargedBytes)
	snapshot.chargedBytes = 0
	snapshot.session = nil
}

func (source *exportReadSource) release() {
	if source == nil || source.session == nil {
		return
	}
	session := source.session
	session.mu.Lock()
	defer session.mu.Unlock()
	source.releaseLocked()
}

func (source *exportReadSource) releaseLocked() {
	if source == nil || source.session == nil {
		return
	}
	session := source.session
	for index := range source.records {
		recordSource := &source.records[index]
		releaseFrozenBlobPrefixLocked(&recordSource.requestBody)
		releaseFrozenBlobPrefixLocked(&recordSource.responseBody)
	}
	for index := range source.messages {
		releaseFrozenBlobPrefixLocked(&source.messages[index].payload)
	}
	source.records = nil
	source.traces = nil
	source.messages = nil
	source.entries = nil
	session.unpinLocked(source.chargedBytes)
	session.releaseLocked(source.chargedBytes)
	source.chargedBytes = 0
	source.session = nil
}

func classifyExportStreamRelease(canceled bool, streamErr error) (reason string) {
	if canceled {
		return "session_stopped"
	}
	if streamErr == nil {
		return "completed"
	}
	reason = "stream_failed"
	defer func() {
		if recover() != nil {
			reason = "stream_failed"
		}
	}()
	if errors.Is(streamErr, ErrExportCanceled) {
		return "session_stopped"
	}
	if errors.Is(streamErr, context.Canceled) || errors.Is(streamErr, context.DeadlineExceeded) {
		return "request_canceled"
	}
	return reason
}

func (state *exportState) writeBlob(
	writer *exportStreamWriter,
	recordIndex int,
	blobIndex int,
	blob *exportBlobSnapshot,
) (streamDigest, error) {
	hasher := sha256.New()
	rawOffset := int64(0)
	chunkCount := 0
	fragmentBytes := exportBlobRawFragmentBytes(writer.LineBytes())
	if fragmentBytes <= 0 {
		return streamDigest{}, errExportLineTooLarge
	}
	for _, segment := range blob.view.segments {
		if segment.owner == nil {
			continue
		}
		for remaining := segment.data; len(remaining) > 0; {
			raw := remaining[:min(len(remaining), fragmentBytes)]
			if _, err := hasher.Write(raw); err != nil {
				return streamDigest{}, err
			}
			checksum := exportwire.CurrentSHA256(hasher)
			if err := writer.WriteBlobData(
				recordIndex,
				blobIndex,
				chunkCount,
				rawOffset,
				blob.view.size,
				raw,
				checksum,
			); err != nil {
				return streamDigest{}, err
			}
			rawOffset += int64(len(raw))
			chunkCount++
			remaining = remaining[len(raw):]
		}
	}
	if rawOffset != blob.view.size {
		return streamDigest{}, fmt.Errorf(
			"stream request capture blob %s: snapshot size changed from %d to %d",
			blob.id,
			blob.view.size,
			rawOffset,
		)
	}
	summary := streamDigest{
		RawSize:    rawOffset,
		ChunkCount: chunkCount,
		Checksum:   exportwire.CurrentSHA256(hasher),
	}
	if err := writer.WriteBlobEnd(recordIndex, blobIndex, summary); err != nil {
		return streamDigest{}, err
	}
	return summary, nil
}

func releaseBlobViewLocked(view *blobView) {
	if view == nil {
		return
	}
	for _, segment := range view.segments {
		releaseChunkLocked(segment.owner, true)
	}
	view.segments = nil
	view.size = 0
	view.session = nil
}
