package requestcapture

type recordDetailQuerySnapshot struct {
	session         *sessionState
	detail          RecordDetail
	requestBody     blobView
	responseBody    blobView
	messagePayloads []blobView
}

func snapshotRecordDetailQueryLocked(
	record *recordState,
	previewBytes, eventLimit int,
) recordDetailQuerySnapshot {
	trace := record.session.traceSummaryLocked(record.gateway)
	snapshot := recordDetailQuerySnapshot{
		session: record.session,
		detail: RecordDetail{
			Summary:       cloneRecordSummary(record.summary),
			SnapshotState: SnapshotStateFinal,
			GatewayTrace:  &trace,
		},
		requestBody:  snapshotBlobPreviewLocked(record.requestBody, previewBytes),
		responseBody: snapshotBlobPreviewLocked(record.responseBody.value, previewBytes),
	}
	if !record.completed {
		snapshot.detail.SnapshotState = SnapshotStateActivePartial
	}
	if record.protocol == ProtocolHTTP {
		snapshot.detail.HTTP = &HTTPExchangeDetail{
			Request:  cloneRequestSnapshot(record.request),
			Response: cloneHTTPResponse(record.httpResponse),
		}
		return snapshot
	}

	messageCount := min(eventLimit, len(record.messages))
	websocket := &WebSocketExchangeDetail{
		Request:         cloneRequestSnapshot(record.request),
		Handshake:       cloneWebSocketHandshake(record.wsHandshake),
		Messages:        make([]MessageSnapshot, 0, messageCount),
		EventsTruncated: len(record.messages) > eventLimit,
		Close:           cloneWebSocketClose(record.wsClose),
	}
	snapshot.detail.WebSocket = websocket
	snapshot.messagePayloads = make([]blobView, 0, messageCount)
	for index := range messageCount {
		message := record.messages[index]
		websocket.Messages = append(websocket.Messages, MessageSnapshot{
			MessageID:       message.id,
			Sequence:        message.sequence,
			RelativeMillis:  message.relativeMillis,
			Direction:       message.direction,
			Type:            message.messageType,
			Source:          message.source,
			SourceMessageID: message.sourceMessageID,
			Disposition:     message.disposition,
			ClientVisible:   message.clientVisible,
			Failure:         message.failure,
			HasFailure:      message.hasFailure,
		})
		snapshot.messagePayloads = append(
			snapshot.messagePayloads,
			snapshotBlobPreviewLocked(message.payload, previewBytes),
		)
	}
	return snapshot
}

func snapshotBlobPreviewLocked(value *blob, previewBytes int) blobView {
	return snapshotBlobPrefixLocked(value, previewBytes)
}

func (snapshot *recordDetailQuerySnapshot) materialize(
	done <-chan struct{},
	owner *queryLease,
	previewBytes int,
) (RecordDetail, error) {
	if snapshot == nil || owner == nil || queryContextCanceled(done) || owner.canceled.Load() {
		return RecordDetail{}, ErrQueryCanceled
	}
	if err := snapshot.blobFailure(); err != nil {
		return RecordDetail{}, err
	}
	if snapshot.detail.HTTP != nil {
		snapshot.detail.HTTP.RequestBody = previewView(snapshot.requestBody, previewBytes)
		if queryContextCanceled(done) || owner.canceled.Load() {
			return RecordDetail{}, ErrQueryCanceled
		}
		snapshot.detail.HTTP.ResponseBody = previewView(snapshot.responseBody, previewBytes)
	} else if snapshot.detail.WebSocket != nil {
		snapshot.detail.WebSocket.RequestBody = previewView(snapshot.requestBody, previewBytes)
		if queryContextCanceled(done) || owner.canceled.Load() {
			return RecordDetail{}, ErrQueryCanceled
		}
		snapshot.detail.WebSocket.HandshakeBody = previewView(snapshot.responseBody, previewBytes)
		for index := range snapshot.detail.WebSocket.Messages {
			if queryContextCanceled(done) || owner.canceled.Load() {
				return RecordDetail{}, ErrQueryCanceled
			}
			snapshot.detail.WebSocket.Messages[index].Payload =
				previewView(snapshot.messagePayloads[index], previewBytes)
		}
	}
	if queryContextCanceled(done) || owner.canceled.Load() {
		return RecordDetail{}, ErrQueryCanceled
	}
	return snapshot.detail, nil
}

func (snapshot *recordDetailQuerySnapshot) blobFailure() error {
	if snapshot.requestBody.failure != nil {
		return snapshot.requestBody.failure
	}
	if snapshot.responseBody.failure != nil {
		return snapshot.responseBody.failure
	}
	for index := range snapshot.messagePayloads {
		if snapshot.messagePayloads[index].failure != nil {
			return snapshot.messagePayloads[index].failure
		}
	}
	return nil
}

func (snapshot *recordDetailQuerySnapshot) release() {
	if snapshot == nil || snapshot.session == nil {
		return
	}
	session := snapshot.session
	session.mu.Lock()
	releaseBlobViewLocked(&snapshot.requestBody)
	releaseBlobViewLocked(&snapshot.responseBody)
	for index := range snapshot.messagePayloads {
		releaseBlobViewLocked(&snapshot.messagePayloads[index])
	}
	snapshot.messagePayloads = nil
	snapshot.detail = RecordDetail{}
	snapshot.session = nil
	session.mu.Unlock()
}

type recordPageShape struct {
	watermark    uint64
	before       uint64
	lastSequence uint64
	recordCount  int
	traceCount   int
	more         bool
}

func (s *sessionState) estimateRecordPageQueryChargeLocked(
	query ListQuery,
) (int64, recordPageShape, error) {
	watermark, beforeSequence, err := s.resolvePagePositionLocked(query)
	if err != nil {
		return 0, recordPageShape{}, err
	}
	shape := recordPageShape{watermark: watermark, before: beforeSequence}
	sourceCharge := queryLeaseBaseChargeBytes + queryWriteChunkBytes
	var traces [DefaultMaxListLimit]*gatewayState
	for record := s.newestRecord; record != nil; record = record.older {
		sequence := record.summary.RecordSequence
		if record.evicted || sequence > watermark || sequence >= beforeSequence {
			continue
		}
		if shape.recordCount == query.Limit {
			shape.more = true
			break
		}
		sourceCharge = addRetainedCharge64(sourceCharge, estimateRecordSummaryQueryCharge(record.summary))
		shape.recordCount++
		shape.lastSequence = sequence
		seen := false
		for index := 0; index < shape.traceCount; index++ {
			if traces[index] == record.gateway {
				seen = true
				break
			}
		}
		if !seen {
			traces[shape.traceCount] = record.gateway
			shape.traceCount++
			sourceCharge = addRetainedCharge64(sourceCharge, estimateGatewayTraceQueryCharge(record.gateway))
		}
	}
	sourceCharge = addRetainedCharge(sourceCharge, maxCursorBytes*2)
	return sourceCharge, shape, nil
}

func estimateRecordDetailQueryChargeLocked(record *recordState, previewBytes, eventLimit int) int64 {
	sourceCharge := queryLeaseBaseChargeBytes + queryWriteChunkBytes + queryRecordCopyBaseBytes
	sourceCharge = addRetainedCharge64(sourceCharge, estimateRecordSummaryQueryCharge(record.summary))
	sourceCharge = addRetainedCharge64(sourceCharge, estimateGatewayTraceQueryCharge(record.gateway))
	sourceCharge = addRetainedCharge(sourceCharge,
		len(record.request.Method)+len(record.request.URL)+len(record.request.Host))
	sourceCharge = addRetainedCharge64(sourceCharge, estimateHeaderCharge(record.request.Headers))
	sourceCharge = addRetainedCharge64(sourceCharge, estimateHeaderCharge(record.request.Trailers))
	sourceCharge = addRetainedCharge64(sourceCharge, estimateBlobPreviewQueryCharge(record.requestBody, previewBytes))
	sourceCharge = addRetainedCharge64(sourceCharge, estimateBlobPreviewQueryCharge(record.responseBody.value, previewBytes))
	if record.httpResponse != nil {
		sourceCharge = addRetainedCharge(sourceCharge, len(record.httpResponse.Protocol))
		sourceCharge = addRetainedCharge64(sourceCharge, estimateHeaderCharge(record.httpResponse.Headers))
		sourceCharge = addRetainedCharge64(sourceCharge, estimateHeaderCharge(record.httpResponse.Trailers))
		sourceCharge = addRetainedCharge64(sourceCharge, estimateStringSliceCharge(record.httpResponse.DeclaredTrailerKeys))
	}
	if record.wsHandshake != nil {
		sourceCharge = addRetainedCharge(sourceCharge, len(record.wsHandshake.Protocol))
		sourceCharge = addRetainedCharge64(sourceCharge, estimateHeaderCharge(record.wsHandshake.Headers))
	}
	if record.wsClose != nil {
		sourceCharge = addRetainedCharge(sourceCharge, len(record.wsClose.Reason))
	}
	for index, message := range record.messages {
		if index == eventLimit {
			break
		}
		sourceCharge = addRetainedCharge64(sourceCharge, queryMessageCopyBaseBytes+sliceEntryChargeBytes)
		sourceCharge = addRetainedCharge(sourceCharge,
			len(message.id)+len(message.direction)+len(message.messageType)+len(message.source)+
				len(message.sourceMessageID)+len(message.disposition))
		sourceCharge = addRetainedCharge64(sourceCharge, estimateFailureCharge(message.failure))
		sourceCharge = addRetainedCharge64(sourceCharge, estimateBlobPreviewQueryCharge(message.payload, previewBytes))
	}
	return sourceCharge
}

func estimateRecordSummaryQueryCharge(summary RecordSummary) int64 {
	return addRetainedCharge(queryRecordCopyBaseBytes,
		len(summary.SessionID)+len(summary.RecordID)+len(summary.GatewayTraceID)+len(summary.GatewayRequestID)+
			len(summary.Provider.ID)+len(summary.Provider.Name)+len(summary.Provider.APIType)+len(summary.Provider.TargetURL)+
			len(summary.Protocol)+len(summary.SelectionMode)+len(summary.SelectionSource)+len(summary.CredentialPhase)+
			len(summary.SourceCompletion)+len(summary.CaptureCompletion)+len(summary.TerminationReason)+
			int(estimateFailureCharge(summary.Failure)))
}

func estimateGatewayTraceQueryCharge(gateway *gatewayState) int64 {
	total := addRetainedCharge(queryTraceCopyBaseBytes, len(gateway.id)+len(gateway.requestID))
	for entry := gateway.entryFirst; entry != nil; entry = entry.after {
		total = addRetainedCharge64(total, estimateTraceEntryCharge(entry.snapshot))
	}
	return total
}

func estimateBlobPreviewQueryCharge(value *blob, previewBytes int) int64 {
	if value == nil {
		return 256
	}
	previewSize := min64(value.size, int64(previewBytes))
	remaining := previewSize
	chunkCount := 0
	for chunk := value.first; chunk != nil && remaining > 0; chunk = chunk.next {
		remaining -= int64(len(chunk.data))
		chunkCount++
	}
	encodedBytes := (previewSize + 2) / 3 * 4
	charge := addRetainedCharge64(256, previewSize)
	charge = addRetainedCharge64(charge, encodedBytes)
	// EncodeToString retains the result and may also hold an encoded-size
	// destination during []byte-to-string conversion. Query admission owns the
	// peak, not merely the final DTO, so charge both lifetimes.
	charge = addRetainedCharge64(charge, encodedBytes)
	return addRetainedCharge64(charge, int64(chunkCount)*blobViewSegmentChargeBytes)
}

func normalizeListQuery(query ListQuery) (ListQuery, error) {
	if query.Limit == 0 {
		query.Limit = DefaultListLimit
	}
	if query.Limit < 1 || query.Limit > DefaultMaxListLimit {
		return ListQuery{}, &ValidationError{Field: "limit", Reason: "is outside the supported range"}
	}
	return query, nil
}

func (s *sessionState) listRecordsLocked(shape recordPageShape) RecordPage {
	page := RecordPage{
		SessionID:         s.id,
		SnapshotWatermark: encodeWatermark(s.generation, shape.watermark),
		Records:           make([]RecordSummary, 0, shape.recordCount),
		GatewayTraces:     make([]GatewayTraceSummary, 0, shape.traceCount),
	}
	var traces [DefaultMaxListLimit]*gatewayState
	traceCount := 0
	for record := s.newestRecord; record != nil && len(page.Records) < shape.recordCount; record = record.older {
		if record.evicted {
			continue
		}
		sequence := record.summary.RecordSequence
		if sequence > shape.watermark || sequence >= shape.before {
			continue
		}
		page.Records = append(page.Records, cloneRecordSummary(record.summary))
		seen := false
		for index := 0; index < traceCount; index++ {
			if traces[index] == record.gateway {
				seen = true
				break
			}
		}
		if !seen {
			traces[traceCount] = record.gateway
			traceCount++
			page.GatewayTraces = append(page.GatewayTraces, s.traceSummaryLocked(record.gateway))
		}
	}
	if shape.more && shape.lastSequence > 0 {
		page.NextCursor = encodeCursor(s.generation, shape.watermark, shape.lastSequence)
	}
	gapLower := uint64(1)
	if shape.more && shape.lastSequence > 0 {
		gapLower = shape.lastSequence
	}
	gapUpper := shape.watermark
	if shape.before != ^uint64(0) && shape.before > 0 && shape.before-1 < gapUpper {
		gapUpper = shape.before - 1
	}
	gapCount := s.evictionCountBetweenLocked(gapLower, gapUpper)
	page.EvictionGap.Detected = gapCount > 0
	if gapCount > uint64(^uint(0)>>1) {
		page.EvictionGap.RecordCount = int(^uint(0) >> 1)
	} else {
		page.EvictionGap.RecordCount = int(gapCount)
	}
	return page
}

func (s *sessionState) resolvePagePositionLocked(query ListQuery) (watermark, before uint64, err error) {
	if query.Cursor == "" {
		if query.SnapshotWatermark != "" {
			return 0, 0, ErrInvalidCursor
		}
		return s.nextRecordSequence, ^uint64(0), nil
	}
	if len(query.Cursor) > maxCursorBytes || len(query.SnapshotWatermark) > maxCursorBytes {
		return 0, 0, ErrInvalidCursor
	}
	cursor, err := decodeCursor(query.Cursor)
	if err != nil || cursor.Generation != s.generation {
		return 0, 0, ErrInvalidCursor
	}
	generation, watermark, err := decodeWatermark(query.SnapshotWatermark)
	if err != nil || generation != s.generation || watermark != cursor.Watermark ||
		watermark > s.nextRecordSequence {
		return 0, 0, ErrInvalidCursor
	}
	maximumBefore := watermark + 1
	if watermark == ^uint64(0) {
		maximumBefore = watermark
	}
	if cursor.Before == 0 || cursor.Before > maximumBefore {
		return 0, 0, ErrInvalidCursor
	}
	return watermark, cursor.Before, nil
}

func (s *sessionState) traceSummaryLocked(gateway *gatewayState) GatewayTraceSummary {
	summary := GatewayTraceSummary{
		GatewayTraceID:         gateway.id,
		GatewayRequestID:       gateway.requestID,
		Entries:                make([]TraceEntry, 0, gateway.entryCount),
		HistoryTruncatedBefore: gateway.historyBefore,
		HistoryTruncatedAfter:  gateway.historyAfter,
	}
	for entry := gateway.entryFirst; entry != nil; entry = entry.after {
		summary.Entries = append(summary.Entries, entry.snapshot)
	}
	return summary
}

func cloneRecordSummary(source RecordSummary) RecordSummary {
	result := source
	if source.CompletedAt != nil {
		completedAt := *source.CompletedAt
		result.CompletedAt = &completedAt
	}
	return result
}

func cloneRequestSnapshot(source RequestSnapshot) RequestSnapshot {
	result := source
	result.Headers = cloneHeaders(source.Headers)
	result.Trailers = cloneHeaders(source.Trailers)
	return result
}

func cloneHTTPResponse(source *HTTPResponseSnapshot) *HTTPResponseSnapshot {
	if source == nil {
		return nil
	}
	result := *source
	result.Headers = cloneHeaders(source.Headers)
	result.Trailers = cloneHeaders(source.Trailers)
	result.DeclaredTrailerKeys = append([]string(nil), source.DeclaredTrailerKeys...)
	return &result
}

func cloneWebSocketHandshake(source *WebSocketHandshakeSnapshot) *WebSocketHandshakeSnapshot {
	if source == nil {
		return nil
	}
	result := *source
	result.Headers = cloneHeaders(source.Headers)
	return &result
}

func cloneHeaders(source map[string][]string) map[string][]string {
	if len(source) == 0 {
		return map[string][]string{}
	}
	result := make(map[string][]string, len(source))
	for name, values := range source {
		result[name] = append([]string(nil), values...)
	}
	return result
}
