package requestcapture

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestExportMultipartMetadataHandlesManyEmptyWebSocketMessages(t *testing.T) {
	const messageCount = 300
	manager := newTestManager(t, func(cfg *Config) {
		cfg.ChunkBytes = MinimumChunkBytes
		cfg.ExportLineBytes = 4096
	})
	session := startTestSession(t, manager, 2, 1<<20, "selected")
	gateway := manager.BeginGateway(GatewayStart{GatewayRequestID: "ws-large"})
	recorder := gateway.BeginWebSocket(RawWebSocketStart{
		TargetURL: "wss://example.test/realtime",
		Attempt: AttemptMetadata{
			Provider:        ProviderIdentity{ID: "selected", Name: "Selected"},
			APIType:         "codex",
			SelectionMode:   SelectionModeInitial,
			SelectionSource: SelectionSourceStrategy,
			CredentialPhase: CredentialPhaseInitial,
		},
		Request: RawRequest{
			Method: http.MethodGet,
			Headers: http.Header{
				"X-Long-Metadata": {strings.Repeat("quoted-\"-slash-\\-unicode-??-", 600)},
			},
		},
	})
	recorder.ObserveWebSocketHandshake(WebSocketHandshake{
		StatusCode: http.StatusSwitchingProtocols,
		Protocol:   "HTTP/1.1",
	})
	for index := range messageCount {
		ref := recorder.MessageRead(MessageRead{
			Direction: MessageDirectionUpstreamToClient,
			Type:      MessageTypeText,
			Source:    MessageSourceLive,
		})
		recorder.MessageResult(ref, MessageResult{
			Disposition:    MessageDispositionForwarded,
			WriteConfirmed: index%2 == 0,
		})
	}
	recorder.Finish(Outcome{
		SourceCompletion:  SourceCompletionComplete,
		TerminationReason: TerminationReasonWebSocketClose,
		WebSocketClose: &WebSocketCloseObservation{
			Direction: MessageDirectionUpstreamToClient,
			Code:      1000,
			Reason:    "normal",
			Clean:     true,
		},
	})
	gateway.Finish(GatewayOutcome{})

	// Exercise the complete structured failure shape independently of proxy
	// classification so this test remains a wire-contract regression.
	failure := FailureObservation{
		Primary: FailureFact{
			Site:               FailureSiteResponseRead,
			Peer:               FailurePeerUpstream,
			Class:              FailureClassRead,
			Code:               FailureCodeUpstreamRead,
			HTTPStatusCode:     http.StatusBadGateway,
			WebSocketCloseCode: 1011,
			SystemErrorCode:    10054,
			Message:            "bounded provider diagnostic",
		},
		Secondary: FailureFact{
			Site:  FailureSiteResponseWrite,
			Peer:  FailurePeerClient,
			Class: FailureClassWrite,
			Code:  FailureCodeClientWrite,
		},
		HasSecondary: true,
		Truncated:    true,
	}
	capture := manager.active.Load()
	capture.mu.Lock()
	recordState := lookupRecordForTest(capture, recorder.ID())
	recordState.summary.Failure = failure
	recordState.summary.HasFailure = true
	recordState.gateway.entryFirst.snapshot.Failure = failure
	recordState.gateway.entryFirst.snapshot.HasFailure = true
	recordState.gateway.entryFirst.snapshot.MetadataTruncated = true
	recordState.messages[0].failure = failure
	recordState.messages[0].hasFailure = true
	capture.mu.Unlock()

	ticket, err := manager.CreateExport(context.Background(), session.SessionID, ExportRequest{
		Scope:     ExportScopeRecords,
		RecordIDs: []string{recorder.ID()},
	})
	if err != nil {
		t.Fatalf("CreateExport() error = %v", err)
	}
	download, err := manager.AcceptDownload(ticket.ExportID, ticket.DownloadToken)
	if err != nil {
		t.Fatalf("AcceptDownload() error = %v", err)
	}
	var destination bytes.Buffer
	if err := download.WriteTo(context.Background(), &destination); err != nil {
		t.Fatalf("WriteTo() error = %v", err)
	}
	lines := decodeExportLines(t, destination.Bytes(), manager.cfg.exportLineBytes)
	record := decodeRecordMetadata(t, lines, 0)
	if record.WebSocket == nil || len(record.WebSocket.Messages) != messageCount {
		t.Fatalf("message count = %d", len(record.WebSocket.Messages))
	}
	if record.Summary.GatewayTraceID == "" ||
		record.Summary.GatewayTraceID != record.GatewayTrace.GatewayTraceID ||
		!record.Summary.HasFailure ||
		record.Summary.Failure != failure {
		t.Fatalf("summary trace/failure metadata = %#v", record.Summary)
	}
	if len(record.GatewayTrace.Entries) == 0 ||
		!record.GatewayTrace.Entries[0].HasFailure ||
		record.GatewayTrace.Entries[0].Failure != failure ||
		!record.GatewayTrace.Entries[0].MetadataTruncated {
		t.Fatalf("trace failure metadata = %#v", record.GatewayTrace.Entries)
	}
	var firstMessage struct {
		HasFailure bool               `json:"has_failure"`
		Failure    FailureObservation `json:"failure"`
	}
	if err := json.Unmarshal(record.WebSocket.Messages[0], &firstMessage); err != nil {
		t.Fatalf("first message metadata: %v", err)
	}
	if !firstMessage.HasFailure || firstMessage.Failure != failure {
		t.Fatalf("message failure was not preserved: %#v", firstMessage)
	}
	var secondMessage map[string]json.RawMessage
	if err := json.Unmarshal(record.WebSocket.Messages[1], &secondMessage); err != nil {
		t.Fatalf("second message metadata: %v", err)
	}
	rawFailure, present := secondMessage["failure"]
	if !present {
		t.Fatal("message without a failure omitted the required failure value")
	}
	var secondFailure FailureObservation
	if err := json.Unmarshal(rawFailure, &secondFailure); err != nil {
		t.Fatalf("decode message zero failure value: %v", err)
	}
	if secondFailure != (FailureObservation{}) {
		t.Fatalf("message without a failure value = %#v, want zero value", secondFailure)
	}
	if raw, present := secondMessage["has_failure"]; !present || string(raw) != "false" {
		t.Fatalf("message without a failure has_failure = %s", raw)
	}
	if record.WebSocket.Close == nil || record.WebSocket.Close.Code != 1000 ||
		!record.WebSocket.Close.Clean {
		t.Fatalf("close metadata = %#v", record.WebSocket.Close)
	}
	metadataChunks := 0
	blobEnds := 0
	for _, line := range lines {
		if line.envelope.Event == exportEventRecord && line.envelope.Part == exportPartMetadataChunk {
			metadataChunks++
		}
		if line.envelope.Event == exportEventBlobChunk && line.envelope.Part == exportPartEnd {
			blobEnds++
		}
	}
	if metadataChunks < 2 {
		t.Fatalf("metadata chunks = %d, want multipart", metadataChunks)
	}
	if blobEnds != messageCount+2 {
		t.Fatalf("blob end count = %d, want %d", blobEnds, messageCount+2)
	}
	if lines[len(lines)-1].envelope.Event != exportEventExportEnd {
		t.Fatal("large websocket export is missing export_end")
	}
}

func TestExportSplitsStorageChunksToActualLineBudget(t *testing.T) {
	const storageChunkBytes = MinimumChunkBytes
	manager := newTestManager(t, func(cfg *Config) {
		cfg.ChunkBytes = storageChunkBytes
		cfg.ExportLineBytes = minimumExportLineBytes() + 64
	})
	session := startTestSession(t, manager, 2, 1<<20, "selected")
	payload := bytes.Repeat([]byte{0x00, 0xff, 0x5a}, storageChunkBytes/3+200)

	gateway, recorder := beginTestHTTP(manager, "gateway-fragmented-export", "selected", payload)
	completeHTTP(recorder, nil)
	gateway.Finish(GatewayOutcome{})
	ticket, err := manager.CreateExport(context.Background(), session.SessionID, ExportRequest{
		Scope:     ExportScopeRecords,
		RecordIDs: []string{recorder.ID()},
	})
	if err != nil {
		t.Fatalf("CreateExport() error = %v", err)
	}
	download, err := manager.AcceptDownload(ticket.ExportID, ticket.DownloadToken)
	if err != nil {
		t.Fatalf("AcceptDownload() error = %v", err)
	}
	var destination bytes.Buffer
	if err := download.WriteTo(context.Background(), &destination); err != nil {
		t.Fatalf("WriteTo() error = %v", err)
	}

	lines := decodeExportLines(t, destination.Bytes(), manager.cfg.exportLineBytes)
	record := decodeRecordMetadata(t, lines, 0)
	assertExportBlob(t, lines, 0, record, requestBodyBlobID, payload)

	requestBlobIndex := -1
	for _, descriptor := range record.Blobs {
		if descriptor.BlobID == requestBodyBlobID {
			requestBlobIndex = descriptor.BlobIndex
			break
		}
	}
	fragmentLimit := exportBlobRawFragmentBytes(manager.cfg.exportLineBytes)
	dataChunks := 0
	for _, line := range lines {
		if line.envelope.Event == exportEventBlobChunk &&
			line.envelope.Part == exportPartData &&
			line.envelope.RecordIndex == 0 &&
			line.envelope.BlobIndex == requestBlobIndex {
			dataChunks++
			if line.envelope.RawSize > fragmentLimit {
				t.Fatalf("fragment raw size = %d, limit %d", line.envelope.RawSize, fragmentLimit)
			}
		}
	}
	if dataChunks < 2 {
		t.Fatalf("storage chunk was not split: data chunks = %d", dataChunks)
	}
}

func TestExportLineFramingUsesFixedWorkspace(t *testing.T) {
	workspace := make([]byte, 4096)
	writer := newExportStreamWriter(
		io.Discard,
		workspace,
		nil,
		func() error { return nil },
	)
	summary := streamDigest{
		RawSize:    123,
		ChunkCount: 4,
		Checksum:   sha256.Sum256([]byte("value")),
	}
	var streamErr error
	allocations := testing.AllocsPerRun(1000, func() {
		streamErr = writer.WriteBlobEnd(12, 34, summary)
	})
	if streamErr != nil {
		t.Fatalf("writeBlobEnd() error = %v", streamErr)
	}
	if allocations > 0 {
		t.Fatalf("fixed line framing allocations = %.2f, want 0", allocations)
	}
}

type decodedManifestMetadata struct {
	Format            string                `json:"format"`
	ExportID          string                `json:"export_id"`
	SessionID         string                `json:"session_id"`
	SnapshotAt        time.Time             `json:"snapshot_at"`
	GatewayTraceCount int                   `json:"gateway_trace_count"`
	GatewayTraces     []decodedGatewayTrace `json:"gateway_traces"`
}

type decodedGatewayTrace struct {
	TraceIndex int                 `json:"trace_index"`
	Trace      GatewayTraceSummary `json:"trace"`
}

type decodedRecordMetadata struct {
	RecordID          string                    `json:"record_id"`
	Summary           RecordSummary             `json:"summary"`
	SnapshotState     SnapshotState             `json:"snapshot_state"`
	SnapshotReason    string                    `json:"snapshot_reason"`
	GatewayTraceIndex int                       `json:"gateway_trace_index"`
	GatewayTrace      GatewayTraceSummary       `json:"-"`
	Request           RequestSnapshot           `json:"request"`
	HTTP              *decodedHTTPMetadata      `json:"http"`
	WebSocket         *decodedWebSocketMetadata `json:"websocket"`
	Blobs             []decodedBlobDescriptor   `json:"blobs"`
}

type decodedHTTPMetadata struct {
	Response *HTTPResponseSnapshot `json:"response"`
}

type decodedWebSocketMetadata struct {
	Handshake *WebSocketHandshakeSnapshot `json:"handshake"`
	Close     *WebSocketCloseSnapshot     `json:"close"`
	Messages  []json.RawMessage           `json:"messages"`
}

type decodedBlobDescriptor struct {
	BlobIndex int    `json:"blob_index"`
	BlobID    string `json:"blob_id"`
	RawSize   int64  `json:"raw_size"`
}

type exportLineEnvelope struct {
	Version          int    `json:"version"`
	Event            string `json:"event"`
	Part             string `json:"part"`
	RecordCount      int    `json:"record_count"`
	RecordIndex      int    `json:"record_index"`
	BlobIndex        int    `json:"blob_index"`
	BlobCount        int    `json:"blob_count"`
	ChunkIndex       int    `json:"chunk_index"`
	ChunkCount       int    `json:"chunk_count"`
	RawOffset        int64  `json:"raw_offset"`
	RawSize          int    `json:"raw_size"`
	RawTotalSize     int64  `json:"raw_total_size"`
	RawPayloadSize   int64  `json:"raw_payload_size"`
	MetadataRawSize  int64  `json:"metadata_raw_size"`
	DataBase64       []byte `json:"data_base64"`
	CumulativeSHA256 string `json:"cumulative_sha256"`
	FinalSHA256      string `json:"final_sha256"`
	MetadataSHA256   string `json:"metadata_sha256"`
	RawPayloadRisk   string `json:"raw_payload_risk"`
}

type decodedExportLine struct {
	envelope exportLineEnvelope
	raw      []byte
}

func decodeExportLines(t *testing.T, payload []byte, maxLineBytes int) []decodedExportLine {
	t.Helper()
	rawLines := bytes.Split(payload, []byte{10})
	if len(rawLines) > 0 && len(rawLines[len(rawLines)-1]) == 0 {
		rawLines = rawLines[:len(rawLines)-1]
	}
	lines := make([]decodedExportLine, 0, len(rawLines))
	for index, raw := range rawLines {
		if len(raw)+1 > maxLineBytes {
			t.Fatalf("line %d has %d bytes, limit %d", index, len(raw)+1, maxLineBytes)
		}
		var envelope exportLineEnvelope
		if err := json.Unmarshal(raw, &envelope); err != nil {
			t.Fatalf("line %d is invalid JSON: %v: %q", index, err, raw)
		}
		if envelope.Version != exportFormatVersion {
			t.Fatalf("line %d version = %d", index, envelope.Version)
		}
		lines = append(lines, decodedExportLine{envelope: envelope, raw: raw})
	}
	return lines
}

func exportEventNames(lines []decodedExportLine) []string {
	names := make([]string, len(lines))
	for index, line := range lines {
		names[index] = line.envelope.Event + ":" + line.envelope.Part
	}
	return names
}

func reconstructMetadata(
	t *testing.T,
	lines []decodedExportLine,
	event string,
	recordIndex int,
) []byte {
	t.Helper()
	var reconstructed []byte
	nextOffset := int64(0)
	nextChunk := 0
	foundEnd := false
	for _, line := range lines {
		envelope := line.envelope
		if envelope.Event != event {
			continue
		}
		if event == exportEventRecord && envelope.RecordIndex != recordIndex {
			continue
		}
		switch envelope.Part {
		case exportPartMetadataChunk:
			if envelope.ChunkIndex != nextChunk || envelope.RawOffset != nextOffset ||
				envelope.RawSize != len(envelope.DataBase64) {
				t.Fatalf("metadata boundary = %#v, next chunk %d offset %d", envelope, nextChunk, nextOffset)
			}
			reconstructed = append(reconstructed, envelope.DataBase64...)
			nextOffset += int64(len(envelope.DataBase64))
			nextChunk++
			sum := sha256.Sum256(reconstructed)
			if envelope.CumulativeSHA256 != hex.EncodeToString(sum[:]) {
				t.Fatalf("metadata cumulative checksum = %q, want %x", envelope.CumulativeSHA256, sum)
			}
		case exportPartMetadataEnd:
			if envelope.ChunkCount != nextChunk || int64(envelope.RawSize) != nextOffset {
				t.Fatalf("metadata end = %#v", envelope)
			}
			sum := sha256.Sum256(reconstructed)
			if envelope.FinalSHA256 != hex.EncodeToString(sum[:]) {
				t.Fatalf("metadata final checksum = %q, want %x", envelope.FinalSHA256, sum)
			}
			foundEnd = true
		}
	}
	if !foundEnd {
		t.Fatalf("%s metadata has no metadata_end", event)
	}
	return reconstructed
}

func decodeRecordMetadata(
	t *testing.T,
	lines []decodedExportLine,
	recordIndex int,
) decodedRecordMetadata {
	t.Helper()
	raw := reconstructMetadata(t, lines, exportEventRecord, recordIndex)
	var metadata decodedRecordMetadata
	if err := json.Unmarshal(raw, &metadata); err != nil {
		t.Fatalf("record %d metadata: %v: %q", recordIndex, err, raw)
	}

	manifestRaw := reconstructMetadata(t, lines, exportEventManifest, 0)
	var manifest decodedManifestMetadata
	if err := json.Unmarshal(manifestRaw, &manifest); err != nil {
		t.Fatalf("manifest metadata: %v: %q", err, manifestRaw)
	}
	if metadata.GatewayTraceIndex < 0 || metadata.GatewayTraceIndex >= len(manifest.GatewayTraces) {
		t.Fatalf("record %d gateway_trace_index = %d, trace count %d", recordIndex, metadata.GatewayTraceIndex, len(manifest.GatewayTraces))
	}
	trace := manifest.GatewayTraces[metadata.GatewayTraceIndex]
	if trace.TraceIndex != metadata.GatewayTraceIndex ||
		trace.Trace.GatewayTraceID != metadata.Summary.GatewayTraceID {
		t.Fatalf("record %d trace reference = %#v, summary trace ID %q", recordIndex, trace, metadata.Summary.GatewayTraceID)
	}
	metadata.GatewayTrace = trace.Trace
	return metadata
}

func assertExportBlob(
	t *testing.T,
	lines []decodedExportLine,
	recordIndex int,
	metadata decodedRecordMetadata,
	blobID string,
	want []byte,
) {
	t.Helper()
	blobIndex := -1
	for _, descriptor := range metadata.Blobs {
		if descriptor.BlobID == blobID {
			blobIndex = descriptor.BlobIndex
			if descriptor.RawSize != int64(len(want)) {
				t.Fatalf("blob descriptor = %#v, want size %d", descriptor, len(want))
			}
			break
		}
	}
	if blobIndex < 0 {
		t.Fatalf("blob %s has no descriptor", blobID)
	}

	var reconstructed []byte
	nextOffset := int64(0)
	nextChunk := 0
	foundEnd := false
	for _, line := range lines {
		envelope := line.envelope
		if envelope.Event != exportEventBlobChunk ||
			envelope.RecordIndex != recordIndex ||
			envelope.BlobIndex != blobIndex {
			continue
		}
		switch envelope.Part {
		case exportPartData:
			if envelope.ChunkIndex != nextChunk || envelope.RawOffset != nextOffset ||
				envelope.RawSize != len(envelope.DataBase64) ||
				envelope.RawTotalSize != int64(len(want)) {
				t.Fatalf("blob boundary = %#v, next chunk %d offset %d", envelope, nextChunk, nextOffset)
			}
			reconstructed = append(reconstructed, envelope.DataBase64...)
			nextOffset += int64(len(envelope.DataBase64))
			nextChunk++
			sum := sha256.Sum256(reconstructed)
			if envelope.CumulativeSHA256 != hex.EncodeToString(sum[:]) {
				t.Fatalf("blob cumulative checksum = %q, want %x", envelope.CumulativeSHA256, sum)
			}
		case exportPartEnd:
			if envelope.ChunkCount != nextChunk || int64(envelope.RawSize) != nextOffset {
				t.Fatalf("blob end = %#v", envelope)
			}
			sum := sha256.Sum256(reconstructed)
			if envelope.FinalSHA256 != hex.EncodeToString(sum[:]) {
				t.Fatalf("blob final checksum = %q, want %x", envelope.FinalSHA256, sum)
			}
			foundEnd = true
		}
	}
	if !bytes.Equal(reconstructed, want) {
		t.Fatalf("blob %s = %v, want %v", blobID, reconstructed, want)
	}
	if !foundEnd {
		t.Fatalf("blob %s has no end line", blobID)
	}
}

type failingExportIDGenerator struct {
	err error
}

func (generator failingExportIDGenerator) NewID() ([16]byte, error) {
	return [16]byte{}, generator.err
}

type failOnWrite struct {
	buffer bytes.Buffer
	failAt int
	calls  int
	err    error
}

func (writer *failOnWrite) Write(payload []byte) (int, error) {
	writer.calls++
	if writer.calls >= writer.failAt {
		return 0, writer.err
	}
	return writer.buffer.Write(payload)
}

type afterFirstWrite struct {
	destination io.Writer
	after       func()
	once        sync.Once
}

func (writer *afterFirstWrite) Write(payload []byte) (int, error) {
	written, err := writer.destination.Write(payload)
	if err == nil {
		writer.once.Do(writer.after)
	}
	return written, err
}

func insertExportStateForTest(
	t *testing.T,
	manager *Manager,
	registryKey string,
	state *exportState,
) {
	t.Helper()
	manager.exportMu.Lock()
	err := manager.materializeExportRegistryLocked()
	inserted := err == nil && manager.insertExportLocked(registryKey, state)
	manager.exportMu.Unlock()
	if err != nil {
		t.Fatalf("materializeExportRegistryLocked() error = %v", err)
	}
	if !inserted {
		t.Fatal("test export state was not inserted")
	}
}

func assertNoExportLease(t *testing.T, manager *Manager) {
	t.Helper()
	status := manager.Status()
	if status.PendingExportCount != 0 || status.ActiveDownloadCount != 0 ||
		status.ProcessMemory.PinnedBytes != 0 ||
		status.ProcessMemory.TemporaryBytes != 0 ||
		status.ProcessMemory.ReleasingBytes != 0 {
		t.Fatalf("export lease remains: %#v", status)
	}
}
