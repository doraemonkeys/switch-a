package querywire

import (
	"io"

	"github.com/doraemonkeys/switch-a/internal/requestcapture/capturevalue"
)

const ChunkBytes = 32 << 10

type Check func() error

type stream struct {
	check   Check
	dst     io.Writer
	storage [ChunkBytes]byte
	length  int
}

func (stream *stream) writeByte(value byte) error {
	if err := stream.ready(); err != nil {
		return err
	}
	if stream.length == len(stream.storage) {
		if err := stream.flush(); err != nil {
			return err
		}
	}
	stream.storage[stream.length] = value
	stream.length++
	return nil
}

func (stream *stream) writeString(value string) error {
	if err := stream.ready(); err != nil {
		return err
	}
	for len(value) > 0 {
		if stream.length == len(stream.storage) {
			if err := stream.flush(); err != nil {
				return err
			}
		}
		copied := copy(stream.storage[stream.length:], value)
		stream.length += copied
		value = value[copied:]
		if err := stream.ready(); err != nil {
			return err
		}
	}
	return nil
}

func (stream *stream) writeBytes(value []byte) error {
	if err := stream.ready(); err != nil {
		return err
	}
	for len(value) > 0 {
		if stream.length == len(stream.storage) {
			if err := stream.flush(); err != nil {
				return err
			}
		}
		copied := copy(stream.storage[stream.length:], value)
		stream.length += copied
		value = value[copied:]
		if err := stream.ready(); err != nil {
			return err
		}
	}
	return nil
}

func (stream *stream) ready() error {
	return stream.check()
}

func (stream *stream) flush() error {
	if stream.length == 0 {
		return stream.ready()
	}
	if err := stream.ready(); err != nil {
		return err
	}
	length := stream.length
	n, err := stream.dst.Write(stream.storage[:length])
	if err != nil {
		return err
	}
	if n != length {
		return io.ErrShortWrite
	}
	stream.length = 0
	// Stop must not wait for an external writer. A write already admitted before
	// cancellation may return afterward, but it cannot authorize another chunk.
	return stream.ready()
}

func (stream *stream) finish() error {
	if err := stream.writeByte('\n'); err != nil {
		return err
	}
	return stream.flush()
}

func WriteRecordPage(dst io.Writer, value capturevalue.RecordPage, check Check) error {
	if dst == nil || check == nil {
		return io.ErrClosedPipe
	}
	stream := stream{check: check, dst: dst}
	writer := documentWriter{sink: &stream}
	writeRecordPageJSON(&writer, value)
	if writer.err != nil {
		return writer.err
	}
	return stream.finish()
}

func WriteRecordDetail(dst io.Writer, value capturevalue.RecordDetail, check Check) error {
	if dst == nil || check == nil {
		return io.ErrClosedPipe
	}
	stream := stream{check: check, dst: dst}
	writer := documentWriter{sink: &stream}
	writeRecordDetailJSON(&writer, value)
	if writer.err != nil {
		return writer.err
	}
	return stream.finish()
}

func writeRecordPageJSON(writer *jsonDocumentWriter, page capturevalue.RecordPage) {
	first := true
	writer.beginObject()
	writer.field("session_id", &first)
	writer.string(page.SessionID)
	writer.field("snapshot_watermark", &first)
	writer.string(page.SnapshotWatermark)
	writer.field("records", &first)
	if page.Records == nil {
		writer.raw("null")
	} else {
		writer.beginArray()
		for index := range page.Records {
			if index > 0 {
				writer.raw(",")
			}
			writeQueryRecordSummaryJSON(writer, page.Records[index])
		}
		writer.endArray()
	}
	writer.field("gateway_traces", &first)
	if page.GatewayTraces == nil {
		writer.raw("null")
	} else {
		writer.beginArray()
		for index := range page.GatewayTraces {
			if index > 0 {
				writer.raw(",")
			}
			writeQueryGatewayTraceJSON(writer, page.GatewayTraces[index])
		}
		writer.endArray()
	}
	if page.NextCursor != "" {
		writer.field("next_cursor", &first)
		writer.string(page.NextCursor)
	}
	writer.field("eviction_gap", &first)
	writeEvictionGapJSON(writer, page.EvictionGap)
	writer.endObject()
}

func writeEvictionGapJSON(writer *jsonDocumentWriter, gap capturevalue.EvictionGap) {
	first := true
	writer.beginObject()
	writer.field("detected", &first)
	writer.boolean(gap.Detected)
	writer.field("record_count", &first)
	writer.int(gap.RecordCount)
	writer.endObject()
}

func writeRecordDetailJSON(writer *jsonDocumentWriter, detail capturevalue.RecordDetail) {
	first := true
	writer.beginObject()
	writer.field("summary", &first)
	writeQueryRecordSummaryJSON(writer, detail.Summary)
	writer.field("snapshot_state", &first)
	writer.string(string(detail.SnapshotState))
	writer.field("gateway_trace", &first)
	if detail.GatewayTrace == nil {
		writer.raw("null")
	} else {
		writeQueryGatewayTraceJSON(writer, *detail.GatewayTrace)
	}
	if detail.HTTP != nil {
		writer.field("http", &first)
		writeHTTPExchangeDetailJSON(writer, detail.HTTP)
	}
	if detail.WebSocket != nil {
		writer.field("websocket", &first)
		writeWebSocketExchangeDetailJSON(writer, detail.WebSocket)
	}
	writer.endObject()
}

func writeHTTPExchangeDetailJSON(writer *jsonDocumentWriter, detail *capturevalue.HTTPExchangeDetail) {
	first := true
	writer.beginObject()
	writer.field("request", &first)
	writeQueryRequestSnapshotJSON(writer, detail.Request)
	writer.field("request_body", &first)
	writeBlobPreviewJSON(writer, detail.RequestBody)
	if detail.Response != nil {
		writer.field("response", &first)
		writeQueryHTTPResponseJSON(writer, detail.Response)
	}
	writer.field("response_body", &first)
	writeBlobPreviewJSON(writer, detail.ResponseBody)
	writer.endObject()
}

func writeWebSocketExchangeDetailJSON(writer *jsonDocumentWriter, detail *capturevalue.WebSocketExchangeDetail) {
	first := true
	writer.beginObject()
	writer.field("request", &first)
	writeQueryRequestSnapshotJSON(writer, detail.Request)
	writer.field("request_body", &first)
	writeBlobPreviewJSON(writer, detail.RequestBody)
	if detail.Handshake != nil {
		writer.field("handshake", &first)
		writeWebSocketHandshakeJSON(writer, detail.Handshake)
	}
	writer.field("handshake_body", &first)
	writeBlobPreviewJSON(writer, detail.HandshakeBody)
	writer.field("messages", &first)
	if detail.Messages == nil {
		writer.raw("null")
	} else {
		writer.beginArray()
		for index := range detail.Messages {
			if index > 0 {
				writer.raw(",")
			}
			writeMessageSnapshotJSON(writer, detail.Messages[index])
		}
		writer.endArray()
	}
	writer.field("events_truncated", &first)
	writer.boolean(detail.EventsTruncated)
	if detail.Close != nil {
		writer.field("close", &first)
		writeQueryWebSocketCloseJSON(writer, detail.Close)
	}
	writer.endObject()
}

func writeMessageSnapshotJSON(writer *jsonDocumentWriter, message capturevalue.MessageSnapshot) {
	first := true
	writer.beginObject()
	writer.field("message_id", &first)
	writer.string(message.MessageID)
	writer.field("sequence", &first)
	writer.uint64(message.Sequence)
	writer.field("relative_millis", &first)
	writer.int64(message.RelativeMillis)
	writer.field("direction", &first)
	writer.string(string(message.Direction))
	writer.field("message_type", &first)
	writer.string(string(message.Type))
	writer.field("source", &first)
	writer.string(string(message.Source))
	if message.SourceMessageID != "" {
		writer.field("source_message_id", &first)
		writer.string(message.SourceMessageID)
	}
	if message.Disposition != "" {
		writer.field("disposition", &first)
		writer.string(string(message.Disposition))
	}
	writer.field("client_visible", &first)
	writer.boolean(message.ClientVisible)
	writer.field("failure", &first)
	writeQueryFailureObservationJSON(writer, message.Failure)
	writer.field("has_failure", &first)
	writer.boolean(message.HasFailure)
	writer.field("payload", &first)
	writeBlobPreviewJSON(writer, message.Payload)
	writer.endObject()
}

func writeQueryRecordSummaryJSON(writer *jsonDocumentWriter, summary capturevalue.RecordSummary) {
	first := true
	writer.beginObject()
	writer.field("session_id", &first)
	writer.string(summary.SessionID)
	writer.field("record_id", &first)
	writer.string(summary.RecordID)
	writer.field("gateway_trace_id", &first)
	writer.string(summary.GatewayTraceID)
	writer.field("gateway_request_id", &first)
	writer.string(summary.GatewayRequestID)
	writer.field("exchange_index", &first)
	writer.uint64(summary.ExchangeIndex)
	writer.field("record_sequence", &first)
	writer.uint64(summary.RecordSequence)
	writer.field("provider", &first)
	writeProviderSnapshotJSON(writer, summary.Provider)
	writer.field("protocol", &first)
	writer.string(string(summary.Protocol))
	writer.field("selection_mode", &first)
	writer.string(string(summary.SelectionMode))
	writer.field("selection_source", &first)
	writer.string(string(summary.SelectionSource))
	writer.field("provider_attempt_index", &first)
	writer.int(summary.ProviderAttemptIndex)
	writer.field("credential_phase", &first)
	writer.string(string(summary.CredentialPhase))
	writer.field("lifecycle_state", &first)
	writer.string(string(summary.LifecycleState))
	if summary.SourceCompletion != "" {
		writer.field("source_completion", &first)
		writer.string(string(summary.SourceCompletion))
	}
	writer.field("capture_completion", &first)
	writer.string(string(summary.CaptureCompletion))
	writer.field("started_at", &first)
	writer.time(summary.StartedAt)
	if summary.CompletedAt != nil {
		writer.field("completed_at", &first)
		writer.time(*summary.CompletedAt)
	}
	if summary.TerminationReason != "" {
		writer.field("termination_reason", &first)
		writer.string(string(summary.TerminationReason))
	}
	writer.field("failure", &first)
	writeQueryFailureObservationJSON(writer, summary.Failure)
	writer.field("has_failure", &first)
	writer.boolean(summary.HasFailure)
	writer.field("upstream_observed_bytes", &first)
	writer.int64(summary.UpstreamObservedBytes)
	writer.field("application_write_confirmed_bytes", &first)
	writer.int64(summary.ApplicationWriteConfirmedBytes)
	writer.endObject()
}

func writeQueryGatewayTraceJSON(writer *jsonDocumentWriter, trace capturevalue.GatewayTraceSummary) {
	first := true
	writer.beginObject()
	writer.field("gateway_trace_id", &first)
	writer.string(trace.GatewayTraceID)
	writer.field("gateway_request_id", &first)
	writer.string(trace.GatewayRequestID)
	writer.field("entries", &first)
	if trace.Entries == nil {
		writer.raw("null")
	} else {
		writer.beginArray()
		for index := range trace.Entries {
			if index > 0 {
				writer.raw(",")
			}
			writeQueryTraceEntryJSON(writer, trace.Entries[index])
		}
		writer.endArray()
	}
	writer.field("history_truncated_before", &first)
	writer.boolean(trace.HistoryTruncatedBefore)
	writer.field("history_truncated_after", &first)
	writer.boolean(trace.HistoryTruncatedAfter)
	writer.endObject()
}

func writeQueryTraceEntryJSON(writer *jsonDocumentWriter, entry capturevalue.TraceEntry) {
	first := true
	writer.beginObject()
	writer.field("kind", &first)
	writer.string(string(entry.Kind))
	writer.field("entry_id", &first)
	writer.string(entry.EntryID)
	writer.field("sequence", &first)
	writer.uint64(entry.Sequence)
	if entry.RecordID != "" {
		writer.field("record_id", &first)
		writer.string(entry.RecordID)
	}
	writer.field("provider", &first)
	writeProviderSnapshotJSON(writer, entry.Provider)
	writer.field("provider_attempt_index", &first)
	writer.int(entry.ProviderAttemptIndex)
	writer.field("selection_mode", &first)
	writer.string(string(entry.SelectionMode))
	writer.field("selection_source", &first)
	writer.string(string(entry.SelectionSource))
	writer.field("credential_phase", &first)
	writer.string(string(entry.CredentialPhase))
	if entry.TerminationReason != "" {
		writer.field("termination_reason", &first)
		writer.string(string(entry.TerminationReason))
	}
	writer.field("failure", &first)
	writeQueryFailureObservationJSON(writer, entry.Failure)
	writer.field("has_failure", &first)
	writer.boolean(entry.HasFailure)
	writer.field("metadata_truncated", &first)
	writer.boolean(entry.MetadataTruncated)
	writer.endObject()
}

func writeQueryFailureObservationJSON(writer *jsonDocumentWriter, failure capturevalue.FailureObservation) {
	first := true
	writer.beginObject()
	writer.field("primary", &first)
	writeQueryFailureFactJSON(writer, failure.Primary)
	writer.field("has_secondary", &first)
	writer.boolean(failure.HasSecondary)
	if failure.HasSecondary {
		writer.field("secondary", &first)
		writeQueryFailureFactJSON(writer, failure.Secondary)
	}
	writer.field("truncated", &first)
	writer.boolean(failure.Truncated)
	writer.endObject()
}

func writeQueryFailureFactJSON(writer *jsonDocumentWriter, fact capturevalue.FailureFact) {
	first := true
	writer.beginObject()
	writer.field("site", &first)
	writer.string(string(fact.Site))
	writer.field("peer", &first)
	writer.string(string(fact.Peer))
	writer.field("class", &first)
	writer.string(string(fact.Class))
	writer.field("code", &first)
	writer.string(string(fact.Code))
	if fact.HTTPStatusCode != 0 {
		writer.field("http_status_code", &first)
		writer.int(fact.HTTPStatusCode)
	}
	if fact.WebSocketCloseCode != 0 {
		writer.field("websocket_close_code", &first)
		writer.int(fact.WebSocketCloseCode)
	}
	if fact.SystemErrorCode != 0 {
		writer.field("system_error_code", &first)
		writer.int64(fact.SystemErrorCode)
	}
	if fact.ProviderErrorType != "" {
		writer.field("provider_error_type", &first)
		writer.string(fact.ProviderErrorType)
	}
	if fact.ProviderErrorCode != "" {
		writer.field("provider_error_code", &first)
		writer.string(fact.ProviderErrorCode)
	}
	if fact.Message != "" {
		writer.field("message", &first)
		writer.string(fact.Message)
	}
	writer.endObject()
}

func writeQueryRequestSnapshotJSON(writer *jsonDocumentWriter, request capturevalue.RequestSnapshot) {
	first := true
	writer.beginObject()
	writer.field("method", &first)
	writer.string(request.Method)
	writer.field("url", &first)
	writer.string(request.URL)
	writer.field("host", &first)
	writer.string(request.Host)
	writer.field("headers", &first)
	writeHeadersJSON(writer, request.Headers)
	writer.field("content_length", &first)
	writer.int64(request.ContentLength)
	if request.Ingress != nil {
		writer.field("ingress", &first)
		writeIngressSnapshotJSON(writer, request.Ingress)
	}
	if len(request.Trailers) > 0 {
		writer.field("trailers", &first)
		writeHeadersJSON(writer, request.Trailers)
	}
	writer.endObject()
}

func writeIngressSnapshotJSON(writer *jsonDocumentWriter, ingress *capturevalue.IngressSnapshot) {
	first := true
	writer.beginObject()
	writer.field("protocol", &first)
	writer.string(ingress.Protocol)
	writer.field("content_length", &first)
	writer.int64(ingress.ContentLength)
	writer.field("transfer_encoding", &first)
	writeStringsJSON(writer, ingress.TransferEncoding)
	writer.field("declared_trailer_keys", &first)
	writeStringsJSON(writer, ingress.DeclaredTrailerKeys)
	writer.field("state", &first)
	writer.string(ingress.State)
	writer.field("received_bytes", &first)
	writer.int64(ingress.ReceivedBytes)
	if len(ingress.Trailers) > 0 {
		writer.field("trailers", &first)
		writeHeadersJSON(writer, ingress.Trailers)
	}
	if ingress.Reason != "" {
		writer.field("reason", &first)
		writer.string(ingress.Reason)
	}
	writer.field("capture_truncated", &first)
	writer.boolean(ingress.CaptureTruncated)
	if ingress.SourceFailure != nil {
		writer.field("source_failure", &first)
		writer.beginObject()
		failureFirst := true
		writer.field("kind", &failureFirst)
		writer.string(string(ingress.SourceFailure.Kind))
		writer.field("reason", &failureFirst)
		writer.string(ingress.SourceFailure.Reason)
		writer.endObject()
	}
	writer.endObject()
}

func writeQueryHTTPResponseJSON(writer *jsonDocumentWriter, response *capturevalue.HTTPResponseSnapshot) {
	if response == nil {
		writer.raw("null")
		return
	}
	first := true
	writer.beginObject()
	writer.field("status_code", &first)
	writer.int(response.StatusCode)
	writer.field("protocol", &first)
	writer.string(response.Protocol)
	writer.field("headers", &first)
	writeHeadersJSON(writer, response.Headers)
	writer.field("content_length", &first)
	writer.int64(response.ContentLength)
	if len(response.DeclaredTrailerKeys) > 0 {
		writer.field("declared_trailer_keys", &first)
		writeStringsJSON(writer, response.DeclaredTrailerKeys)
	}
	if len(response.Trailers) > 0 {
		writer.field("trailers", &first)
		writeHeadersJSON(writer, response.Trailers)
	}
	writer.endObject()
}

func writeQueryWebSocketCloseJSON(writer *jsonDocumentWriter, closeSnapshot *capturevalue.WebSocketCloseSnapshot) {
	if closeSnapshot == nil {
		writer.raw("null")
		return
	}
	first := true
	writer.beginObject()
	writer.field("direction", &first)
	writer.string(string(closeSnapshot.Direction))
	writer.field("code", &first)
	writer.int(closeSnapshot.Code)
	if closeSnapshot.Reason != "" {
		writer.field("reason", &first)
		writer.string(closeSnapshot.Reason)
	}
	writer.field("reason_truncated", &first)
	writer.boolean(closeSnapshot.ReasonTruncated)
	writer.field("clean", &first)
	writer.boolean(closeSnapshot.Clean)
	writer.endObject()
}

func writeBlobPreviewJSON(writer *jsonDocumentWriter, preview capturevalue.BlobPreview) {
	first := true
	writer.beginObject()
	writer.field("data_base64", &first)
	writer.string(preview.DataBase64)
	writer.field("preview_bytes", &first)
	writer.int64(preview.PreviewBytes)
	writer.field("captured_bytes", &first)
	writer.int64(preview.CapturedBytes)
	writer.field("truncated", &first)
	writer.boolean(preview.Truncated)
	writer.field("checksum_sha256", &first)
	writer.string(preview.ChecksumSHA256)
	writer.endObject()
}
