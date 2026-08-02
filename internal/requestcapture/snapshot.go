package requestcapture

import (
	"crypto/sha256"
	"fmt"
	"math"
)

func freezeBlobPrefixLocked(value *blob) (frozenBlobPrefix, error) {
	if value == nil {
		return frozenBlobPrefix{checksum: sha256.Sum256(nil)}, nil
	}
	if value.last != nil {
		// Export owns an immutable prefix. The live recorder therefore starts a
		// new chunk after this boundary instead of mutating shared tail bytes.
		value.last.sealed = true
	}

	totalPinCharge := int64(0)
	for chunk, index := value.first, 0; index < value.chunkCount; index++ {
		if chunk == nil || chunk.charge <= 0 ||
			chunk.refs.Load() <= 0 || chunk.pins.Load() < 0 {
			logBlobInvariant(value.session, "freeze_export_prefix", "invalid_chunk_owner")
			return frozenBlobPrefix{}, ErrInternalFailure
		}
		if chunk.charge > math.MaxInt64-totalPinCharge {
			return frozenBlobPrefix{}, ErrCapacityExceeded
		}
		totalPinCharge += chunk.charge
		if index+1 < value.chunkCount {
			chunk = chunk.next
		}
	}
	manager := value.session.manager
	manager.mu.Lock()
	if totalPinCharge > math.MaxInt64-manager.processPinned {
		manager.mu.Unlock()
		return frozenBlobPrefix{}, ErrCapacityExceeded
	}
	manager.pinAccountLocked(totalPinCharge)
	manager.mu.Unlock()

	source := frozenBlobPrefix{
		session:  value.session,
		segments: make([]blobViewSegment, value.chunkCount),
		size:     value.size,
	}
	newPinCharge := int64(0)
	chunk := value.first
	for index := range source.segments {
		if chunk == nil {
			return rollbackFrozenBlobPrefixLocked(
				value.session, &source, totalPinCharge, newPinCharge, "broken_chunk_chain",
			)
		}
		refs := chunk.refs.Add(1)
		if refs <= 1 {
			chunk.refs.Add(-1)
			return rollbackFrozenBlobPrefixLocked(
				value.session, &source, totalPinCharge, newPinCharge, "released_chunk",
			)
		}
		pins := chunk.pins.Add(1)
		if pins <= 0 {
			chunk.pins.Add(-1)
			chunk.refs.Add(-1)
			return rollbackFrozenBlobPrefixLocked(
				value.session, &source, totalPinCharge, newPinCharge, "corrupt_pin_owner",
			)
		}
		if pins == 1 {
			newPinCharge += chunk.charge
		}
		source.segments[index] = blobViewSegment{
			owner: chunk,
			data:  chunk.data[:len(chunk.data):len(chunk.data)],
		}
		if index+1 < len(source.segments) {
			chunk = chunk.next
		}
	}
	if err := refundFrozenBlobPins(value.session, totalPinCharge-newPinCharge); err != nil {
		releaseFrozenBlobPrefixLocked(&source)
		return frozenBlobPrefix{}, err
	}
	var checksum [sha256.Size]byte
	copy(source.checksum[:], value.hasher.Sum(checksum[:0]))
	return source, nil
}

func (source *frozenBlobPrefix) materialize(
	probe exportCancellationProbe,
) (blobView, error) {
	if err := probe.lockedError(); err != nil {
		return blobView{}, err
	}
	if source == nil {
		return blobView{checksum: sha256.Sum256(nil)}, nil
	}
	view := blobView{
		session:  source.session,
		segments: source.segments,
		size:     source.size,
		checksum: source.checksum,
	}
	source.segments = nil
	source.session = nil
	source.size = 0
	return view, nil
}

func rollbackFrozenBlobPrefixLocked(
	session *sessionState,
	source *frozenBlobPrefix,
	totalPinCharge, newPinCharge int64,
	reason string,
) (frozenBlobPrefix, error) {
	releaseFrozenBlobPrefixLocked(source)
	if err := refundFrozenBlobPins(session, totalPinCharge-newPinCharge); err != nil {
		return frozenBlobPrefix{}, err
	}
	logBlobInvariant(session, "freeze_export_prefix", reason)
	return frozenBlobPrefix{}, ErrInternalFailure
}

func refundFrozenBlobPins(session *sessionState, charge int64) error {
	if session == nil || charge <= 0 {
		return nil
	}
	manager := session.manager
	manager.mu.Lock()
	refunded := manager.unpinAccountLocked(charge)
	manager.mu.Unlock()
	if !refunded {
		logBlobInvariant(session, "freeze_export_prefix", "pin_reservation_underflow")
		return ErrInternalFailure
	}
	return nil
}

func releaseFrozenBlobPrefixLocked(source *frozenBlobPrefix) {
	if source == nil {
		return
	}
	for _, segment := range source.segments {
		releaseChunkLocked(segment.owner, true)
	}
	source.segments = nil
	source.session = nil
	source.size = 0
}

// freezeExportReadSourceLocked acquires immutable graph ownership without
// copying payload bytes. Dynamic metadata and exact blob-prefix descriptors are
// fully charged before publication, so session teardown can release the mutable
// record graph without invalidating or under-accounting the source.
func estimateExportRecordSourceCharge(record *recordState) (int64, int) {
	total := saturatedChargeAdd(
		exportReadSourceRecordChargeBytes,
		estimateRecordSummaryOwnedStringCharge(record.summary),
	)
	total = saturatedChargeAdd(total, int64(len(record.protocol)))
	total = saturatedChargeAdd(total, estimateRequestSnapshotOwnedCharge(record.request))
	total = saturatedChargeAdd(total, estimateBlobSourceDescriptorCharge(record.requestBody))
	total = saturatedChargeAdd(total, estimateBlobSourceDescriptorCharge(record.responseBody.value))

	if record.httpResponse != nil {
		total = saturatedChargeAdd(total, int64(len(record.httpResponse.Protocol)))
		total = saturatedChargeAdd(total, estimateHeaderCharge(record.httpResponse.Headers))
		total = saturatedChargeAdd(total, estimateHeaderCharge(record.httpResponse.Trailers))
		total = saturatedChargeAdd(total, estimateStringSliceCharge(record.httpResponse.DeclaredTrailerKeys))
	}
	if record.wsHandshake != nil {
		total = saturatedChargeAdd(total, int64(len(record.wsHandshake.Protocol)))
		total = saturatedChargeAdd(total, estimateHeaderCharge(record.wsHandshake.Headers))
	}
	if record.wsClose != nil {
		total = saturatedChargeAdd(total, int64(len(record.wsClose.Direction)+len(record.wsClose.Reason)))
	}

	messageCount := 0
	for _, message := range record.messages {
		if message == nil {
			continue
		}
		messageCount++
		total = saturatedChargeAdd(total, exportReadSourceMessageChargeBytes)
		total = saturatedChargeAdd(total, int64(
			len(message.id)+
				len(message.direction)+
				len(message.messageType)+
				len(message.source)+
				len(message.sourceMessageID)+
				len(message.disposition),
		))
		total = saturatedChargeAdd(
			total,
			estimatePresentFailureCharge(message.failure, message.hasFailure),
		)
		total = saturatedChargeAdd(total, estimateBlobSourceDescriptorCharge(message.payload))
	}
	return total, messageCount
}

func estimatePresentFailureCharge(
	observation FailureObservation,
	present bool,
) int64 {
	if !present {
		return 0
	}
	return estimateFailureCharge(observation)
}

func estimateExportTraceSourceCharge(gateway *gatewayState) int64 {
	total := saturatedChargeAdd(
		exportReadSourceTraceChargeBytes,
		int64(len(gateway.id)+len(gateway.requestID)),
	)
	for entry := gateway.entryFirst; entry != nil; entry = entry.after {
		total = saturatedChargeAdd(total, exportReadSourceEntryChargeBytes)
		total = saturatedChargeAdd(total, estimateTraceEntryOwnedStringCharge(entry.snapshot))
	}
	return total
}

func estimateBlobSourceDescriptorCharge(value *blob) int64 {
	if value == nil {
		return 0
	}
	if int64(value.chunkCount) > math.MaxInt64/blobViewSegmentChargeBytes {
		return math.MaxInt64
	}
	return int64(value.chunkCount) * blobViewSegmentChargeBytes
}

type exportReadSourcePlan struct {
	recordCount  int
	traceCount   int
	messageCount int
	entryCount   int
	charge       int64
}

func freezeExportReadSourceLocked(
	probe exportCancellationProbe,
	session *sessionState,
	selection *exportSelection,
	state *exportState,
) (*exportReadSource, error) {
	if err := probe.lockedError(); err != nil {
		return nil, err
	}
	if err := selection.resolveRecordsLocked(probe, session); err != nil {
		return nil, err
	}
	defer clearExportTraceMarksLocked(session, selection, state)

	plan, err := planExportReadSourceLocked(probe, session, selection, state)
	if err != nil {
		return nil, err
	}
	if selection.scope == ExportScopeRecords && plan.recordCount != selection.selectedRecordCount() {
		return nil, ErrSnapshotChanged
	}
	if plan.charge == math.MaxInt64 || !session.reserveLocked(plan.charge, false) {
		return nil, ErrCapacityExceeded
	}
	session.pinLocked(plan.charge)

	source := &exportReadSource{
		session:      session,
		sessionID:    session.id,
		records:      make([]exportRecordSource, 0, plan.recordCount),
		traces:       make([]exportTraceSource, 0, plan.traceCount),
		messages:     make([]exportMessageSource, 0, plan.messageCount),
		entries:      make([]TraceEntry, 0, plan.entryCount),
		chargedBytes: plan.charge,
	}
	for record := session.oldestRecord; record != nil; record = record.newer {
		if err := probe.lockedError(); err != nil {
			source.releaseLocked()
			return nil, err
		}
		if !selection.selectsRecord(record) {
			continue
		}
		if err := freezeSelectedExportRecordLocked(probe, source, record); err != nil {
			source.releaseLocked()
			return nil, err
		}
	}
	return source, nil
}

func planExportReadSourceLocked(
	probe exportCancellationProbe,
	session *sessionState,
	selection *exportSelection,
	state *exportState,
) (exportReadSourcePlan, error) {
	plan := exportReadSourcePlan{
		charge: saturatedChargeAdd(exportReadSourceBaseChargeBytes, int64(len(session.id))),
	}
	for record := session.oldestRecord; record != nil; record = record.newer {
		if err := probe.lockedError(); err != nil {
			return exportReadSourcePlan{}, err
		}
		if !selection.selectsRecord(record) {
			continue
		}
		if record.protocol != ProtocolHTTP && record.protocol != ProtocolWebSocket {
			return exportReadSourcePlan{}, fmt.Errorf(
				"snapshot record %s: unsupported protocol %q",
				record.id,
				record.protocol,
			)
		}
		if record.gateway == nil {
			return exportReadSourcePlan{}, fmt.Errorf(
				"snapshot record %s: missing gateway trace",
				record.id,
			)
		}
		recordCharge, retainedMessages := estimateExportRecordSourceCharge(record)
		plan.recordCount++
		plan.messageCount += retainedMessages
		plan.charge = saturatedChargeAdd(plan.charge, recordCharge)

		gateway := record.gateway
		if gateway.exportSnapshotOwner != state {
			gateway.exportSnapshotOwner = state
			gateway.exportSnapshotIndex = plan.traceCount
			gateway.exportSnapshotMaterialized = false
			plan.traceCount++
			plan.entryCount += gateway.entryCount
			plan.charge = saturatedChargeAdd(plan.charge, estimateExportTraceSourceCharge(gateway))
		}
	}
	return plan, nil
}

func freezeSelectedExportRecordLocked(
	probe exportCancellationProbe,
	source *exportReadSource,
	record *recordState,
) error {
	source.records = append(source.records, exportRecordSource{
		summary:       record.summary,
		snapshotState: SnapshotStateActivePartial,
		traceIndex:    record.gateway.exportSnapshotIndex,
		protocol:      record.protocol,
		request:       record.request,
		messageOffset: len(source.messages),
	})
	recordSource := &source.records[len(source.records)-1]
	if record.completed {
		recordSource.snapshotState = SnapshotStateFinal
	}
	if record.httpResponse != nil {
		recordSource.httpResponse = *record.httpResponse
		recordSource.hasHTTP = true
	}
	if record.wsHandshake != nil {
		recordSource.wsHandshake = *record.wsHandshake
		recordSource.hasWSHandshake = true
	}
	if record.wsClose != nil {
		recordSource.wsClose = *record.wsClose
		recordSource.hasWSClose = true
	}

	var err error
	recordSource.requestBody, err = freezeBlobPrefixLocked(record.requestBody)
	if err != nil {
		return err
	}
	recordSource.responseBody, err = freezeBlobPrefixLocked(record.responseBody.value)
	if err != nil {
		return err
	}
	for _, message := range record.messages {
		if err := probe.lockedError(); err != nil {
			return err
		}
		if message == nil {
			continue
		}
		payload, err := freezeBlobPrefixLocked(message.payload)
		if err != nil {
			return err
		}
		source.messages = append(source.messages, exportMessageSource{
			messageID:            message.id,
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
			observedPayloadBytes: int64(message.observedSize),
			payload:              payload,
		})
		recordSource.messageCount++
	}

	gateway := record.gateway
	if !gateway.exportSnapshotMaterialized {
		traceSource := exportTraceSource{
			gatewayTraceID:         gateway.id,
			gatewayRequestID:       gateway.requestID,
			historyTruncatedBefore: gateway.historyBefore,
			historyTruncatedAfter:  gateway.historyAfter,
			entryOffset:            len(source.entries),
			entryCount:             gateway.entryCount,
		}
		for entry := gateway.entryFirst; entry != nil; entry = entry.after {
			source.entries = append(source.entries, entry.snapshot)
		}
		source.traces = append(source.traces, traceSource)
		gateway.exportSnapshotMaterialized = true
	}
	return nil
}

func acquireExportSnapshot(
	probe exportCancellationProbe,
	source *exportReadSource,
) (*exportSnapshot, error) {
	if source == nil || source.session == nil {
		return nil, ErrNoActiveSession
	}
	chargedBytes, err := estimateExportSnapshotCharge(probe, source)
	if err != nil {
		source.release()
		return nil, err
	}
	session := source.session
	if err := lockExportSessionState(probe, session, probe.state); err != nil {
		source.release()
		return nil, err
	}
	if err := probe.lockedError(); err != nil {
		source.releaseLocked()
		session.mu.Unlock()
		return nil, err
	}
	if chargedBytes == math.MaxInt64 || !session.reserveLocked(chargedBytes, false) {
		source.releaseLocked()
		session.mu.Unlock()
		return nil, ErrCapacityExceeded
	}
	session.pinLocked(chargedBytes)
	session.mu.Unlock()

	snapshot := &exportSnapshot{
		session:      session,
		sessionID:    source.sessionID,
		traces:       make([]GatewayTraceSummary, 0, len(source.traces)),
		records:      make([]exportRecordSnapshot, 0, len(source.records)),
		chargedBytes: chargedBytes,
	}
	for _, traceSource := range source.traces {
		if err := probe.lockedError(); err != nil {
			snapshot.release()
			source.release()
			return nil, err
		}
		entries := make([]TraceEntry, traceSource.entryCount)
		copy(entries, source.entries[traceSource.entryOffset:traceSource.entryOffset+traceSource.entryCount])
		snapshot.traces = append(snapshot.traces, GatewayTraceSummary{
			GatewayTraceID:         traceSource.gatewayTraceID,
			GatewayRequestID:       traceSource.gatewayRequestID,
			Entries:                entries,
			HistoryTruncatedBefore: traceSource.historyTruncatedBefore,
			HistoryTruncatedAfter:  traceSource.historyTruncatedAfter,
		})
	}
	for index := range source.records {
		recordSnapshot, err := materializeExportRecordSnapshot(probe, source, index)
		// Appending before error handling transfers every completed blob view to
		// the snapshot's single cleanup owner.
		snapshot.records = append(snapshot.records, recordSnapshot)
		if err != nil {
			snapshot.release()
			source.release()
			return nil, err
		}
	}
	if err := probe.lockedError(); err != nil {
		snapshot.release()
		source.release()
		return nil, err
	}
	source.release()
	return snapshot, nil
}

func estimateExportSnapshotCharge(
	probe exportCancellationProbe,
	source *exportReadSource,
) (int64, error) {
	total := saturatedChargeAdd(exportSnapshotOwnedBaseChargeBytes, int64(len(source.sessionID)))
	for _, trace := range source.traces {
		if err := probe.lockedError(); err != nil {
			return 0, err
		}
		total = saturatedChargeAdd(total, exportSnapshotOwnedTraceChargeBytes)
		total = saturatedChargeAdd(total, int64(len(trace.gatewayTraceID)+len(trace.gatewayRequestID)))
		for _, entry := range source.entries[trace.entryOffset : trace.entryOffset+trace.entryCount] {
			if err := probe.lockedError(); err != nil {
				return 0, err
			}
			total = saturatedChargeAdd(total, exportSnapshotOwnedEntryChargeBytes)
			total = saturatedChargeAdd(total, estimateTraceEntryOwnedStringCharge(entry))
		}
	}
	for index := range source.records {
		if err := probe.lockedError(); err != nil {
			return 0, err
		}
		record := &source.records[index]
		total = saturatedChargeAdd(total, exportSnapshotOwnedRecordChargeBytes)
		total = saturatedChargeAdd(total, estimateRecordSummaryOwnedStringCharge(record.summary))
		total = saturatedChargeAdd(total, estimateRequestSnapshotOwnedCharge(record.request))
		total = saturatedChargeAdd(total, estimateFrozenBlobOwnedCharge(record.requestBody))
		total = saturatedChargeAdd(total, estimateFrozenBlobOwnedCharge(record.responseBody))
		total = saturatedChargeAdd(total, 2*exportSnapshotOwnedBlobChargeBytes)
		switch record.protocol {
		case ProtocolHTTP:
			total = saturatedChargeAdd(total, exportSnapshotOwnedHTTPChargeBytes)
			if record.hasHTTP {
				total = saturatedChargeAdd(total, int64(len(record.httpResponse.Protocol)))
				total = saturatedChargeAdd(total, estimateHeaderCharge(record.httpResponse.Headers))
				total = saturatedChargeAdd(total, estimateHeaderCharge(record.httpResponse.Trailers))
				total = saturatedChargeAdd(total, estimateStringSliceCharge(record.httpResponse.DeclaredTrailerKeys))
			}
		case ProtocolWebSocket:
			total = saturatedChargeAdd(total, exportSnapshotOwnedWebSocketChargeBytes)
			if record.hasWSHandshake {
				total = saturatedChargeAdd(total, int64(len(record.wsHandshake.Protocol)))
				total = saturatedChargeAdd(total, estimateHeaderCharge(record.wsHandshake.Headers))
			}
			if record.hasWSClose {
				total = saturatedChargeAdd(
					total,
					int64(len(record.wsClose.Direction)+len(record.wsClose.Reason)),
				)
			}
			for _, message := range source.messages[record.messageOffset : record.messageOffset+record.messageCount] {
				if err := probe.lockedError(); err != nil {
					return 0, err
				}
				total = saturatedChargeAdd(total, exportSnapshotOwnedMessageChargeBytes)
				total = saturatedChargeAdd(total, exportSnapshotOwnedBlobChargeBytes)
				total = saturatedChargeAdd(total, estimateFrozenBlobOwnedCharge(message.payload))
				total = saturatedChargeAdd(total, int64(
					len(message.messageID)+
						len(message.direction)+
						len(message.messageType)+
						len(message.source)+
						len(message.sourceMessageID)+
						len(message.disposition)+
						messagePayloadBlobIDSourceBytes(message),
				))
				total = saturatedChargeAdd(
					total,
					estimatePresentFailureCharge(message.failure, message.hasFailure),
				)
			}
		}
	}
	return total, nil
}

func estimateRecordSummaryOwnedStringCharge(summary RecordSummary) int64 {
	total := int64(
		len(summary.SessionID) +
			len(summary.RecordID) +
			len(summary.GatewayTraceID) +
			len(summary.GatewayRequestID) +
			len(summary.Provider.ID) +
			len(summary.Provider.Name) +
			len(summary.Provider.APIType) +
			len(summary.Provider.TargetURL) +
			len(summary.Protocol) +
			len(summary.SelectionMode) +
			len(summary.SelectionSource) +
			len(summary.CredentialPhase) +
			len(summary.LifecycleState) +
			len(summary.SourceCompletion) +
			len(summary.CaptureCompletion) +
			len(summary.TerminationReason),
	)
	return saturatedChargeAdd(
		total,
		estimatePresentFailureCharge(summary.Failure, summary.HasFailure),
	)
}

func estimateRequestSnapshotOwnedCharge(request RequestSnapshot) int64 {
	total := int64(len(request.Method) + len(request.URL) + len(request.Host))
	total = saturatedChargeAdd(total, estimateHeaderCharge(request.Headers))
	return saturatedChargeAdd(total, estimateHeaderCharge(request.Trailers))
}

func estimateTraceEntryOwnedStringCharge(entry TraceEntry) int64 {
	total := int64(
		len(entry.EntryID) +
			len(entry.Kind) +
			len(entry.RecordID) +
			len(entry.Provider.ID) +
			len(entry.Provider.Name) +
			len(entry.Provider.APIType) +
			len(entry.Provider.TargetURL) +
			len(entry.SelectionMode) +
			len(entry.SelectionSource) +
			len(entry.CredentialPhase) +
			len(entry.TerminationReason),
	)
	return saturatedChargeAdd(
		total,
		estimatePresentFailureCharge(entry.Failure, entry.HasFailure),
	)
}

func estimateFrozenBlobOwnedCharge(source frozenBlobPrefix) int64 {
	return int64(len(source.segments)) * blobViewSegmentChargeBytes
}
