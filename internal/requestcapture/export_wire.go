package requestcapture

import (
	"github.com/doraemonkeys/switch-a/internal/requestcapture/capturevalue"
	"strconv"
	"time"

	"github.com/doraemonkeys/switch-a/internal/requestcapture/jsonstream"
)

type fixedJSONLine struct {
	storage []byte
	length  int
}

func (line *fixedJSONLine) writeByte(value byte) error {
	if line.length == len(line.storage) {
		return errExportLineTooLarge
	}
	line.storage[line.length] = value
	line.length++
	return nil
}

func (line *fixedJSONLine) writeString(value string) error {
	if len(value) > len(line.storage)-line.length {
		return errExportLineTooLarge
	}
	copy(line.storage[line.length:], value)
	line.length += len(value)
	return nil
}

func (line *fixedJSONLine) writeBytes(value []byte) error {
	if len(value) > len(line.storage)-line.length {
		return errExportLineTooLarge
	}
	copy(line.storage[line.length:], value)
	line.length += len(value)
	return nil
}

func (line *fixedJSONLine) bytes() []byte {
	return line.storage[:line.length]
}

type jsonTextSink interface {
	writeByte(byte) error
	writeString(string) error
	writeBytes([]byte) error
}

type jsonStreamSink struct {
	sink jsonTextSink
}

func (adapter jsonStreamSink) WriteByte(value byte) error {
	return adapter.sink.writeByte(value)
}

func (adapter jsonStreamSink) WriteString(value string) error {
	return adapter.sink.writeString(value)
}

func (adapter jsonStreamSink) WriteBytes(value []byte) error {
	return adapter.sink.writeBytes(value)
}

func writeJSONString(sink jsonTextSink, value string) error {
	return jsonstream.WriteString(jsonStreamSink{sink: sink}, value)
}

type jsonDocumentWriter struct {
	sink jsonTextSink
	err  error
}

func (writer *jsonDocumentWriter) raw(value string) {
	if writer.err == nil {
		writer.err = writer.sink.writeString(value)
	}
}

func (writer *jsonDocumentWriter) string(value string) {
	if writer.err == nil {
		writer.err = writeJSONString(writer.sink, value)
	}
}

func (writer *jsonDocumentWriter) int(value int) {
	writer.int64(int64(value))
}

func (writer *jsonDocumentWriter) int64(value int64) {
	if writer.err != nil {
		return
	}
	var encoded [32]byte
	writer.err = writer.sink.writeBytes(strconv.AppendInt(encoded[:0], value, 10))
}

func (writer *jsonDocumentWriter) uint64(value uint64) {
	if writer.err != nil {
		return
	}
	var encoded [32]byte
	writer.err = writer.sink.writeBytes(strconv.AppendUint(encoded[:0], value, 10))
}

func (writer *jsonDocumentWriter) boolean(value bool) {
	if value {
		writer.raw("true")
	} else {
		writer.raw("false")
	}
}

func (writer *jsonDocumentWriter) time(value time.Time) {
	if writer.err != nil {
		return
	}
	if err := writer.sink.writeByte('"'); err != nil {
		writer.err = err
		return
	}
	var encoded [64]byte
	rendered := value.AppendFormat(encoded[:0], time.RFC3339Nano)
	if err := writer.sink.writeBytes(rendered); err != nil {
		writer.err = err
		return
	}
	writer.err = writer.sink.writeByte('"')
}

func (writer *jsonDocumentWriter) beginObject() {
	writer.raw("{")
}

func (writer *jsonDocumentWriter) endObject() {
	writer.raw("}")
}

func (writer *jsonDocumentWriter) beginArray() {
	writer.raw("[")
}

func (writer *jsonDocumentWriter) endArray() {
	writer.raw("]")
}

func (writer *jsonDocumentWriter) field(name string, first *bool) {
	if !*first {
		writer.raw(",")
	}
	*first = false
	writer.string(name)
	writer.raw(":")
}

func writeManifestMetadata(writer *jsonDocumentWriter, state *exportState) {
	first := true
	writer.beginObject()
	writer.field("format", &first)
	writer.string("switch-a.request-capture")
	writer.field("export_id", &first)
	writer.string(state.id)
	writer.field("session_id", &first)
	writer.string(state.sessionID)
	writer.field("snapshot_at", &first)
	writer.time(state.snapshotAt)
	writer.field("gateway_trace_count", &first)
	writer.int(len(state.snapshot.traces))
	writer.field("gateway_traces", &first)
	writer.beginArray()
	for traceIndex := range state.snapshot.traces {
		if traceIndex > 0 {
			writer.raw(",")
		}
		traceFirst := true
		writer.beginObject()
		writer.field("trace_index", &traceFirst)
		writer.int(traceIndex)
		writer.field("trace", &traceFirst)
		writeGatewayTraceJSON(writer, state.snapshot.traces[traceIndex])
		writer.endObject()
	}
	writer.endArray()
	writer.endObject()
}

func writeRecordMetadata(writer *jsonDocumentWriter, record *exportRecordSnapshot) {
	first := true
	writer.beginObject()
	writer.field("record_id", &first)
	writer.string(record.summary.RecordID)
	writer.field("summary", &first)
	writeRecordSummaryJSON(writer, record.summary)
	writer.field("snapshot_state", &first)
	writer.string(string(record.snapshotState))
	writer.field("snapshot_reason", &first)
	if record.snapshotState == SnapshotStateActivePartial {
		writer.string(snapshotWhileActive)
	} else {
		writer.raw("null")
	}
	writer.field("gateway_trace_index", &first)
	writer.int(record.traceIndex)
	writer.field("request", &first)
	writeRequestSnapshotJSON(writer, record.request)
	writer.field("http", &first)
	if record.http == nil {
		writer.raw("null")
	} else {
		httpFirst := true
		writer.beginObject()
		writer.field("response", &httpFirst)
		writeHTTPResponseJSON(writer, record.http.response)
		writer.endObject()
	}
	writer.field("websocket", &first)
	if record.websocket == nil {
		writer.raw("null")
	} else {
		writeWebSocketSnapshotJSON(writer, record.websocket)
	}
	writer.field("blobs", &first)
	writer.beginArray()
	for index := range record.blobs {
		if index > 0 {
			writer.raw(",")
		}
		blobFirst := true
		writer.beginObject()
		writer.field("blob_index", &blobFirst)
		writer.int(index)
		writer.field("blob_id", &blobFirst)
		writer.string(record.blobs[index].id)
		writer.field("raw_size", &blobFirst)
		writer.int64(record.blobs[index].view.size)
		writer.endObject()
	}
	writer.endArray()
	writer.endObject()
}

func writeRecordSummaryJSON(writer *jsonDocumentWriter, summary RecordSummary) {
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
	writer.field("source_completion", &first)
	writer.string(string(summary.SourceCompletion))
	writer.field("capture_completion", &first)
	writer.string(string(summary.CaptureCompletion))
	writer.field("started_at", &first)
	writer.time(summary.StartedAt)
	writer.field("completed_at", &first)
	if summary.CompletedAt == nil {
		writer.raw("null")
	} else {
		writer.time(*summary.CompletedAt)
	}
	writer.field("termination_reason", &first)
	writer.string(string(summary.TerminationReason))
	writer.field("failure", &first)
	writeExportFailureObservationJSON(writer, summary.Failure)
	writer.field("has_failure", &first)
	writer.boolean(summary.HasFailure)
	writer.field("upstream_observed_bytes", &first)
	writer.int64(summary.UpstreamObservedBytes)
	writer.field("application_write_confirmed_bytes", &first)
	writer.int64(summary.ApplicationWriteConfirmedBytes)
	writer.endObject()
}

func writeProviderSnapshotJSON(writer *jsonDocumentWriter, provider ProviderSnapshot) {
	first := true
	writer.beginObject()
	writer.field("id", &first)
	writer.string(provider.ID)
	writer.field("name", &first)
	writer.string(provider.Name)
	writer.field("api_type", &first)
	writer.string(provider.APIType)
	writer.field("target_url", &first)
	writer.string(provider.TargetURL)
	writer.endObject()
}

func writeExportFailureObservationJSON(
	writer *jsonDocumentWriter,
	observation FailureObservation,
) {
	first := true
	writer.beginObject()
	writer.field("primary", &first)
	writeExportFailureFactJSON(writer, observation.Primary)
	writer.field("has_secondary", &first)
	writer.boolean(observation.HasSecondary)
	if observation.HasSecondary {
		writer.field("secondary", &first)
		writeExportFailureFactJSON(writer, observation.Secondary)
	}
	writer.field("truncated", &first)
	writer.boolean(observation.Truncated)
	writer.endObject()
}

func writeExportFailureFactJSON(writer *jsonDocumentWriter, fact FailureFact) {
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

func writeGatewayTraceJSON(writer *jsonDocumentWriter, trace GatewayTraceSummary) {
	first := true
	writer.beginObject()
	writer.field("gateway_trace_id", &first)
	writer.string(trace.GatewayTraceID)
	writer.field("gateway_request_id", &first)
	writer.string(trace.GatewayRequestID)
	writer.field("entries", &first)
	writer.beginArray()
	for index, entry := range trace.Entries {
		if index > 0 {
			writer.raw(",")
		}
		writeTraceEntryJSON(writer, entry)
	}
	writer.endArray()
	writer.field("history_truncated_before", &first)
	writer.boolean(trace.HistoryTruncatedBefore)
	writer.field("history_truncated_after", &first)
	writer.boolean(trace.HistoryTruncatedAfter)
	writer.endObject()
}

func writeTraceEntryJSON(writer *jsonDocumentWriter, entry TraceEntry) {
	first := true
	writer.beginObject()
	writer.field("kind", &first)
	writer.string(string(entry.Kind))
	writer.field("entry_id", &first)
	writer.string(entry.EntryID)
	writer.field("sequence", &first)
	writer.uint64(entry.Sequence)
	writer.field("record_id", &first)
	writer.string(entry.RecordID)
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
	writer.field("termination_reason", &first)
	writer.string(string(entry.TerminationReason))
	writer.field("failure", &first)
	writeExportFailureObservationJSON(writer, entry.Failure)
	writer.field("has_failure", &first)
	writer.boolean(entry.HasFailure)
	writer.field("metadata_truncated", &first)
	writer.boolean(entry.MetadataTruncated)
	writer.endObject()
}

func writeRequestSnapshotJSON(writer *jsonDocumentWriter, request RequestSnapshot) {
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
	writer.field("trailers", &first)
	writeHeadersJSON(writer, request.Trailers)
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

func writeHTTPResponseJSON(writer *jsonDocumentWriter, response *HTTPResponseSnapshot) {
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
	writer.field("declared_trailer_keys", &first)
	writeStringsJSON(writer, response.DeclaredTrailerKeys)
	writer.field("trailers", &first)
	writeHeadersJSON(writer, response.Trailers)
	writer.endObject()
}

func writeWebSocketSnapshotJSON(writer *jsonDocumentWriter, websocket *exportWebSocketSnapshot) {
	first := true
	writer.beginObject()
	writer.field("handshake", &first)
	writeWebSocketHandshakeJSON(writer, websocket.handshake)
	writer.field("close", &first)
	writeWebSocketCloseJSON(writer, websocket.close)
	writer.field("messages", &first)
	writer.beginArray()
	for index := range websocket.messages {
		if index > 0 {
			writer.raw(",")
		}
		writeMessageJSON(writer, websocket.messages[index])
	}
	writer.endArray()
	writer.endObject()
}

func writeWebSocketHandshakeJSON(writer *jsonDocumentWriter, handshake *WebSocketHandshakeSnapshot) {
	if handshake == nil {
		writer.raw("null")
		return
	}
	first := true
	writer.beginObject()
	writer.field("status_code", &first)
	writer.int(handshake.StatusCode)
	writer.field("protocol", &first)
	writer.string(handshake.Protocol)
	writer.field("headers", &first)
	writeHeadersJSON(writer, handshake.Headers)
	writer.endObject()
}

func writeWebSocketCloseJSON(writer *jsonDocumentWriter, closeSnapshot *WebSocketCloseSnapshot) {
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
	writer.field("reason", &first)
	writer.string(closeSnapshot.Reason)
	writer.field("reason_truncated", &first)
	writer.boolean(closeSnapshot.ReasonTruncated)
	writer.field("clean", &first)
	writer.boolean(closeSnapshot.Clean)
	writer.endObject()
}

func writeMessageJSON(writer *jsonDocumentWriter, message exportMessageSnapshot) {
	first := true
	writer.beginObject()
	writer.field("message_id", &first)
	writer.string(message.messageID)
	writer.field("sequence", &first)
	writer.uint64(message.sequence)
	writer.field("relative_millis", &first)
	writer.int64(message.relativeMillis)
	writer.field("direction", &first)
	writer.string(string(message.direction))
	writer.field("message_type", &first)
	writer.string(string(message.messageType))
	writer.field("source", &first)
	writer.string(string(message.source))
	writer.field("source_message_id", &first)
	writer.string(message.sourceMessageID)
	writer.field("disposition", &first)
	writer.string(string(message.disposition))
	writer.field("client_visible", &first)
	writer.boolean(message.clientVisible)
	writer.field("failure", &first)
	writeExportFailureObservationJSON(writer, message.failure)
	writer.field("has_failure", &first)
	writer.boolean(message.hasFailure)
	writer.field("observed_payload_raw_size", &first)
	writer.int64(message.observedPayloadBytes)
	writer.field("payload_blob_index", &first)
	writer.int64(int64(message.payloadBlobIndex))
	writer.field("payload_blob_id", &first)
	writer.string(message.payloadBlobID)
	writer.endObject()
}

func writeHeadersJSON(writer *jsonDocumentWriter, headers map[string][]string) {
	if headers == nil {
		writer.raw("null")
		return
	}
	first := true
	writer.beginObject()
	for name, values := range headers {
		writer.field(name, &first)
		writeStringsJSON(writer, values)
	}
	writer.endObject()
}

func writeStringsJSON(writer *jsonDocumentWriter, values []string) {
	if values == nil {
		writer.raw("null")
		return
	}
	writer.beginArray()
	for index, value := range values {
		if index > 0 {
			writer.raw(",")
		}
		writer.string(value)
	}
	writer.endArray()
}
