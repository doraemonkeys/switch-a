package querywire

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/requestcapture/capturevalue"
)

func TestWriteRecordPageEncodesCompleteContract(t *testing.T) {
	t.Parallel()

	completedAt := time.Date(2026, time.August, 2, 3, 4, 5, 6, time.UTC)
	summary := completeRecordSummary(completedAt)
	page := capturevalue.RecordPage{
		SessionID:         "session-1",
		SnapshotWatermark: "watermark-1",
		Records:           []capturevalue.RecordSummary{summary},
		GatewayTraces: []capturevalue.GatewayTraceSummary{{
			GatewayTraceID:   "trace-1",
			GatewayRequestID: "gateway-1",
			Entries: []capturevalue.TraceEntry{{
				Kind:                 capturevalue.TraceEntryRecord,
				EntryID:              "entry-1",
				Sequence:             9,
				RecordID:             summary.RecordID,
				Provider:             summary.Provider,
				ProviderAttemptIndex: 2,
				SelectionMode:        capturevalue.SelectionModeFailover,
				SelectionSource:      capturevalue.SelectionSourceStickyContinuity,
				CredentialPhase:      capturevalue.CredentialPhaseRefreshed,
				TerminationReason:    capturevalue.TerminationReasonEOF,
				Failure:              completeFailureObservation(),
				HasFailure:           true,
				MetadataTruncated:    true,
			}},
			HistoryTruncatedBefore: true,
			HistoryTruncatedAfter:  true,
		}},
		NextCursor:  "cursor-2",
		EvictionGap: capturevalue.EvictionGap{Detected: true, RecordCount: 3},
	}

	payload, checks := encodePage(t, page)
	if checks == 0 {
		t.Fatal("readiness check was never called")
	}
	if payload[len(payload)-1] != '\n' {
		t.Fatalf("payload does not end with a newline: %q", payload)
	}

	var decoded capturevalue.RecordPage
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("decode page: %v; payload=%q", err, payload)
	}
	if decoded.NextCursor != page.NextCursor || len(decoded.Records) != 1 || len(decoded.GatewayTraces) != 1 {
		t.Fatalf("decoded page lost contract fields: %#v", decoded)
	}
	if got := decoded.GatewayTraces[0].Entries[0]; !got.HasFailure || !got.Failure.HasSecondary {
		t.Fatalf("decoded trace failure = %#v", got.Failure)
	}

	root := decodeObject(t, payload)
	records := decodeArray(t, root["records"])
	record := decodeObject(t, records[0])
	failure := decodeObject(t, record["failure"])
	if _, ok := failure["secondary"]; !ok {
		t.Fatal("secondary failure was omitted despite has_secondary=true")
	}
}

func TestWriteIngressFactsPreservesPendingAndPartialEvidence(t *testing.T) {
	for _, state := range []string{"receiving", "aborted"} {
		facts := &capturevalue.IngressSnapshot{Protocol: "HTTP/2.0", ContentLength: -1,
			TransferEncoding: []string{"chunked"}, DeclaredTrailerKeys: []string{"X-End"}, State: state, ReceivedBytes: 17}
		if state == "aborted" {
			facts.Reason = "upstream stopped upload"
			facts.Trailers = map[string][]string{"X-End": {"done"}}
			facts.CaptureTruncated = true
			facts.SourceFailure = &capturevalue.IngressFailureSnapshot{Kind: capturevalue.IngressFailureStorage, Reason: "disk read failed"}
		}
		detail := capturevalue.RecordDetail{HTTP: &capturevalue.HTTPExchangeDetail{Request: capturevalue.RequestSnapshot{Ingress: facts}}}
		payload, _ := encodeDetail(t, detail)
		var decoded capturevalue.RecordDetail
		if err := json.Unmarshal(payload, &decoded); err != nil {
			t.Fatal(err)
		}
		got := decoded.HTTP.Request.Ingress
		if got == nil || got.State != state || got.ContentLength != -1 || got.ReceivedBytes != 17 || got.Reason != facts.Reason || got.CaptureTruncated != facts.CaptureTruncated {
			t.Fatalf("ingress round trip: %+v", got)
		}
		if state == "aborted" && got.Trailers["X-End"][0] != "done" {
			t.Fatal("final trailers missing")
		}
		if state == "aborted" && (got.SourceFailure == nil || *got.SourceFailure != *facts.SourceFailure) {
			t.Fatal("source failure lost")
		}
	}
}

func TestWriteRecordDetailEncodesHTTPAndWebSocketContract(t *testing.T) {
	t.Parallel()

	completedAt := time.Date(2026, time.August, 2, 4, 5, 6, 7, time.FixedZone("test", 8*60*60))
	summary := completeRecordSummary(completedAt)
	trace := capturevalue.GatewayTraceSummary{
		GatewayTraceID:   "trace-1",
		GatewayRequestID: "gateway-1",
		Entries:          []capturevalue.TraceEntry{},
	}
	detail := capturevalue.RecordDetail{
		Summary:       summary,
		SnapshotState: capturevalue.SnapshotStateFinal,
		GatewayTrace:  &trace,
		HTTP: &capturevalue.HTTPExchangeDetail{
			Request:      completeRequestSnapshot(),
			RequestBody:  completeBlobPreview("cmVxdWVzdA=="),
			Response:     completeHTTPResponseSnapshot(),
			ResponseBody: completeBlobPreview("cmVzcG9uc2U="),
		},
		WebSocket: &capturevalue.WebSocketExchangeDetail{
			Request:       completeRequestSnapshot(),
			RequestBody:   completeBlobPreview("ZGlhbA=="),
			Handshake:     completeWebSocketHandshakeSnapshot(),
			HandshakeBody: completeBlobPreview("aGFuZHNoYWtl"),
			Messages: []capturevalue.MessageSnapshot{{
				MessageID:       "message-1",
				Sequence:        11,
				RelativeMillis:  12,
				Direction:       capturevalue.MessageDirectionUpstreamToClient,
				Type:            capturevalue.MessageTypeText,
				Source:          capturevalue.MessageSourceReplay,
				SourceMessageID: "source-message-1",
				Disposition:     capturevalue.MessageDispositionForwarded,
				ClientVisible:   true,
				Failure:         completeFailureObservation(),
				HasFailure:      true,
				Payload:         completeBlobPreview("bWVzc2FnZQ=="),
			}},
			EventsTruncated: true,
			Close: &capturevalue.WebSocketCloseSnapshot{
				Direction:       capturevalue.MessageDirectionUpstreamToClient,
				Code:            1000,
				Reason:          "complete",
				ReasonTruncated: true,
				Clean:           true,
			},
		},
	}

	payload, checks := encodeDetail(t, detail)
	if checks == 0 {
		t.Fatal("readiness check was never called")
	}
	var decoded capturevalue.RecordDetail
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("decode detail: %v; payload=%q", err, payload)
	}
	if decoded.HTTP == nil || decoded.HTTP.Response == nil {
		t.Fatalf("decoded HTTP detail = %#v", decoded.HTTP)
	}
	if decoded.WebSocket == nil || decoded.WebSocket.Handshake == nil || decoded.WebSocket.Close == nil {
		t.Fatalf("decoded websocket detail = %#v", decoded.WebSocket)
	}
	if len(decoded.WebSocket.Messages) != 1 || decoded.WebSocket.Messages[0].SourceMessageID != "source-message-1" {
		t.Fatalf("decoded websocket messages = %#v", decoded.WebSocket.Messages)
	}
}

func TestWriteDocumentsPreserveNullAndOmittedFields(t *testing.T) {
	t.Parallel()

	pagePayload, _ := encodePage(t, capturevalue.RecordPage{})
	page := decodeObject(t, pagePayload)
	assertJSONNull(t, page["records"], "records")
	assertJSONNull(t, page["gateway_traces"], "gateway_traces")
	if _, ok := page["next_cursor"]; ok {
		t.Fatal("empty next_cursor must be omitted")
	}

	detailPayload, _ := encodeDetail(t, capturevalue.RecordDetail{
		HTTP:      &capturevalue.HTTPExchangeDetail{},
		WebSocket: &capturevalue.WebSocketExchangeDetail{},
	})
	detail := decodeObject(t, detailPayload)
	assertJSONNull(t, detail["gateway_trace"], "gateway_trace")
	summary := decodeObject(t, detail["summary"])
	for _, field := range []string{"source_completion", "completed_at", "termination_reason"} {
		if _, ok := summary[field]; ok {
			t.Fatalf("zero summary field %q must be omitted", field)
		}
	}
	failure := decodeObject(t, summary["failure"])
	if _, ok := failure["secondary"]; ok {
		t.Fatal("secondary failure must be omitted when has_secondary=false")
	}
	primary := decodeObject(t, failure["primary"])
	for _, field := range []string{
		"http_status_code",
		"websocket_close_code",
		"system_error_code",
		"provider_error_type",
		"provider_error_code",
		"message",
	} {
		if _, ok := primary[field]; ok {
			t.Fatalf("zero failure field %q must be omitted", field)
		}
	}

	httpDetail := decodeObject(t, detail["http"])
	if _, ok := httpDetail["response"]; ok {
		t.Fatal("nil HTTP response must be omitted")
	}
	request := decodeObject(t, httpDetail["request"])
	if _, ok := request["trailers"]; ok {
		t.Fatal("empty request trailers must be omitted")
	}
	websocket := decodeObject(t, detail["websocket"])
	for _, field := range []string{"handshake", "close"} {
		if _, ok := websocket[field]; ok {
			t.Fatalf("nil websocket field %q must be omitted", field)
		}
	}
	assertJSONNull(t, websocket["messages"], "messages")
}

func TestWriteRecordPageStreamsInFixedChunks(t *testing.T) {
	t.Parallel()

	destination := &countingWriter{}
	checks := 0
	page := capturevalue.RecordPage{SessionID: strings.Repeat("x", 2*ChunkBytes)}
	err := WriteRecordPage(destination, page, func() error {
		checks++
		return nil
	})
	if err != nil {
		t.Fatalf("WriteRecordPage() error = %v", err)
	}
	if destination.writes < 3 {
		t.Fatalf("external writes = %d, want at least 3 fixed chunks", destination.writes)
	}
	if checks <= destination.writes*2 {
		t.Fatalf("readiness checks = %d, writes = %d", checks, destination.writes)
	}
	var decoded capturevalue.RecordPage
	if err = json.Unmarshal(destination.Bytes(), &decoded); err != nil {
		t.Fatalf("decode chunked page: %v", err)
	}
	if decoded.SessionID != page.SessionID {
		t.Fatalf("session ID length = %d, want %d", len(decoded.SessionID), len(page.SessionID))
	}
}

func TestWriterFailuresRemainObservable(t *testing.T) {
	t.Parallel()

	t.Run("invalid destination", func(t *testing.T) {
		err := WriteRecordPage(nil, capturevalue.RecordPage{}, func() error { return nil })
		if !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("WriteRecordPage() error = %v, want closed pipe", err)
		}
	})

	t.Run("invalid check", func(t *testing.T) {
		err := WriteRecordDetail(io.Discard, capturevalue.RecordDetail{}, nil)
		if !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("WriteRecordDetail() error = %v, want closed pipe", err)
		}
	})

	t.Run("canceled after external write", func(t *testing.T) {
		canceled := false
		destination := &cancelingWriter{canceled: &canceled}
		errCanceled := errors.New("query canceled")
		err := WriteRecordPage(destination, capturevalue.RecordPage{}, func() error {
			if canceled {
				return errCanceled
			}
			return nil
		})
		if !errors.Is(err, errCanceled) {
			t.Fatalf("WriteRecordPage() error = %v, want cancellation", err)
		}
		if destination.writes != 1 {
			t.Fatalf("external writes = %d, want 1", destination.writes)
		}
	})

	t.Run("short write", func(t *testing.T) {
		err := WriteRecordPage(shortWriter{}, capturevalue.RecordPage{}, func() error { return nil })
		if !errors.Is(err, io.ErrShortWrite) {
			t.Fatalf("WriteRecordPage() error = %v, want short write", err)
		}
	})

	t.Run("writer error", func(t *testing.T) {
		errWriter := errors.New("writer failed")
		err := WriteRecordDetail(failingWriter{err: errWriter}, capturevalue.RecordDetail{}, func() error { return nil })
		if !errors.Is(err, errWriter) {
			t.Fatalf("WriteRecordDetail() error = %v, want writer failure", err)
		}
	})

	t.Run("empty flush checks readiness", func(t *testing.T) {
		errCanceled := errors.New("query canceled")
		stream := stream{check: func() error { return errCanceled }, dst: io.Discard}
		if err := stream.flush(); !errors.Is(err, errCanceled) {
			t.Fatalf("flush() error = %v, want cancellation", err)
		}
	})

	t.Run("writer panic propagates", func(t *testing.T) {
		defer func() {
			if recovered := recover(); recovered != "writer panic" {
				t.Fatalf("panic = %#v, want writer panic", recovered)
			}
		}()
		_ = WriteRecordPage(panickingWriter{}, capturevalue.RecordPage{}, func() error { return nil })
		t.Fatal("WriteRecordPage() swallowed writer panic")
	})
}

func completeRecordSummary(completedAt time.Time) capturevalue.RecordSummary {
	return capturevalue.RecordSummary{
		SessionID:                      "session-1",
		RecordID:                       "record-1",
		GatewayTraceID:                 "trace-1",
		GatewayRequestID:               "gateway-1",
		ExchangeIndex:                  4,
		RecordSequence:                 5,
		Provider:                       completeProviderSnapshot(),
		Protocol:                       capturevalue.ProtocolHTTP,
		SelectionMode:                  capturevalue.SelectionModeFailover,
		SelectionSource:                capturevalue.SelectionSourceStrategy,
		ProviderAttemptIndex:           2,
		CredentialPhase:                capturevalue.CredentialPhaseRefreshed,
		LifecycleState:                 capturevalue.LifecycleStateCompleted,
		SourceCompletion:               capturevalue.SourceCompletionComplete,
		CaptureCompletion:              capturevalue.CaptureCompletionComplete,
		StartedAt:                      completedAt.Add(-time.Second),
		CompletedAt:                    &completedAt,
		TerminationReason:              capturevalue.TerminationReasonEOF,
		Failure:                        completeFailureObservation(),
		HasFailure:                     true,
		UpstreamObservedBytes:          13,
		ApplicationWriteConfirmedBytes: 12,
	}
}

func completeProviderSnapshot() capturevalue.ProviderSnapshot {
	return capturevalue.ProviderSnapshot{
		ID:        "provider-1",
		Name:      "Provider One",
		APIType:   "openai",
		TargetURL: "https://provider.example/v1?token=%5BREDACTED%5D",
	}
}

func completeFailureObservation() capturevalue.FailureObservation {
	return capturevalue.FailureObservation{
		Primary: capturevalue.FailureFact{
			Site:               capturevalue.FailureSiteResponseWrite,
			Peer:               capturevalue.FailurePeerClient,
			Class:              capturevalue.FailureClassWrite,
			Code:               capturevalue.FailureCodeClientWrite,
			HTTPStatusCode:     502,
			WebSocketCloseCode: 1011,
			SystemErrorCode:    32,
			ProviderErrorType:  "upstream_error",
			ProviderErrorCode:  "temporarily_unavailable",
			Message:            "sanitized failure",
		},
		Secondary: capturevalue.FailureFact{
			Site:    capturevalue.FailureSiteResponseRead,
			Peer:    capturevalue.FailurePeerUpstream,
			Class:   capturevalue.FailureClassRead,
			Code:    capturevalue.FailureCodeUpstreamRead,
			Message: "secondary failure",
		},
		HasSecondary: true,
		Truncated:    true,
	}
}

func completeRequestSnapshot() capturevalue.RequestSnapshot {
	return capturevalue.RequestSnapshot{
		Method: "POST",
		URL:    "https://provider.example/v1/messages?token=%5BREDACTED%5D",
		Host:   "provider.example",
		Headers: map[string][]string{
			"Authorization": {"[REDACTED]"},
			"X-Control":     {"line\x01break"},
		},
		ContentLength: 7,
		Trailers:      map[string][]string{"X-Trailer": {"done"}},
	}
}

func completeHTTPResponseSnapshot() *capturevalue.HTTPResponseSnapshot {
	return &capturevalue.HTTPResponseSnapshot{
		StatusCode:          200,
		Protocol:            "HTTP/2.0",
		Headers:             map[string][]string{"Content-Type": {"text/event-stream"}},
		ContentLength:       -1,
		DeclaredTrailerKeys: []string{"X-Checksum"},
		Trailers:            map[string][]string{"X-Checksum": {"abc123"}},
	}
}

func completeWebSocketHandshakeSnapshot() *capturevalue.WebSocketHandshakeSnapshot {
	return &capturevalue.WebSocketHandshakeSnapshot{
		StatusCode: 101,
		Protocol:   "HTTP/1.1",
		Headers:    map[string][]string{"Upgrade": {"websocket"}},
	}
}

func completeBlobPreview(data string) capturevalue.BlobPreview {
	return capturevalue.BlobPreview{
		DataBase64:     data,
		PreviewBytes:   7,
		CapturedBytes:  9,
		Truncated:      true,
		ChecksumSHA256: strings.Repeat("a", 64),
	}
}

func encodePage(t *testing.T, page capturevalue.RecordPage) ([]byte, int) {
	t.Helper()
	var destination bytes.Buffer
	checks := 0
	if err := WriteRecordPage(&destination, page, func() error {
		checks++
		return nil
	}); err != nil {
		t.Fatalf("WriteRecordPage() error = %v", err)
	}
	return destination.Bytes(), checks
}

func encodeDetail(t *testing.T, detail capturevalue.RecordDetail) ([]byte, int) {
	t.Helper()
	var destination bytes.Buffer
	checks := 0
	if err := WriteRecordDetail(&destination, detail, func() error {
		checks++
		return nil
	}); err != nil {
		t.Fatalf("WriteRecordDetail() error = %v", err)
	}
	return destination.Bytes(), checks
}

func decodeObject(t *testing.T, payload []byte) map[string]json.RawMessage {
	t.Helper()
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("decode object: %v; payload=%q", err, payload)
	}
	return decoded
}

func decodeArray(t *testing.T, payload []byte) []json.RawMessage {
	t.Helper()
	var decoded []json.RawMessage
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("decode array: %v; payload=%q", err, payload)
	}
	return decoded
}

func assertJSONNull(t *testing.T, payload []byte, field string) {
	t.Helper()
	if string(payload) != "null" {
		t.Fatalf("%s = %s, want null", field, payload)
	}
}

type countingWriter struct {
	bytes.Buffer
	writes int
}

func (writer *countingWriter) Write(payload []byte) (int, error) {
	writer.writes++
	return writer.Buffer.Write(payload)
}

type cancelingWriter struct {
	canceled *bool
	writes   int
}

func (writer *cancelingWriter) Write(payload []byte) (int, error) {
	writer.writes++
	*writer.canceled = true
	return len(payload), nil
}

type shortWriter struct{}

func (shortWriter) Write(payload []byte) (int, error) {
	return len(payload) - 1, nil
}

type failingWriter struct {
	err error
}

func (writer failingWriter) Write([]byte) (int, error) {
	return 0, writer.err
}

type panickingWriter struct{}

func (panickingWriter) Write([]byte) (int, error) {
	panic("writer panic")
}
