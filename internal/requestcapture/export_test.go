package requestcapture

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"
	"unsafe"
)

func TestExportNDJSONRoundTripsBinaryChunkBoundariesInOrder(t *testing.T) {
	manager := newTestManager(t, func(cfg *Config) {
		cfg.ChunkBytes = MinimumChunkBytes
		cfg.ExportLineBytes = 4096
	})
	session := startTestSession(t, manager, 3, 1<<20, "selected")
	requestPayload := bytes.Repeat([]byte{0x00, 0xff, 0x80, 0x7f, 0x01}, MinimumChunkBytes/5+2)
	responsePayload := bytes.Repeat([]byte{0xff, 0x00, 0x01, 0x80, 0x81, 0x82}, MinimumChunkBytes/6+2)

	gateway := manager.BeginGateway(GatewayStart{GatewayRequestID: "gateway-roundtrip"})
	gateway.Transition(TransitionStart{
		Attempt: AttemptMetadata{
			Provider:             ProviderIdentity{ID: "fallback", Name: "Fallback"},
			SelectionMode:        SelectionModeInitial,
			SelectionSource:      SelectionSourceStrategy,
			ProviderAttemptIndex: 0,
			CredentialPhase:      CredentialPhaseInitial,
		},
		TerminationReason: TerminationReasonTransportError,
	})
	recorder := gateway.BeginHTTP(RawHTTPStart{
		URL: testParsedURL("https://example.test/v1/messages"),
		Attempt: AttemptMetadata{
			Provider:             ProviderIdentity{ID: "selected", Name: "Selected"},
			APIType:              "claude",
			SelectionMode:        SelectionModeFailover,
			SelectionSource:      SelectionSourceStrategy,
			ProviderAttemptIndex: 1,
			CredentialPhase:      CredentialPhaseInitial,
		},
		Request: RawRequest{
			Method:        http.MethodPost,
			Headers:       http.Header{"Content-Type": {"application/octet-stream"}},
			ContentLength: int64(len(requestPayload)),
			Body:          requestPayload,
		},
	})
	completeHTTP(recorder, responsePayload)
	gateway.Finish(GatewayOutcome{})

	ticket, err := manager.CreateExport(context.Background(), session.SessionID, ExportRequest{
		Scope:     ExportScopeRecords,
		RecordIDs: []string{recorder.ID()},
	})
	if err != nil {
		t.Fatalf("CreateExport() error = %v", err)
	}
	if ticket.RecordCount != 1 || manager.Status().ProcessMemory.PinnedBytes == 0 {
		t.Fatalf("ticket/status = %#v %#v", ticket, manager.Status())
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
	if lines[0].envelope.Event != exportEventManifest ||
		lines[0].envelope.Part != exportPartBegin ||
		lines[len(lines)-2].envelope.Event != exportEventRecordEnd ||
		lines[len(lines)-1].envelope.Event != exportEventExportEnd {
		t.Fatalf("event order = %v", exportEventNames(lines))
	}
	manifestRaw := reconstructMetadata(t, lines, exportEventManifest, 0)
	var manifest decodedManifestMetadata
	if err := json.Unmarshal(manifestRaw, &manifest); err != nil {
		t.Fatalf("manifest metadata: %v", err)
	}
	if manifest.ExportID != ticket.ExportID || manifest.SessionID != session.SessionID {
		t.Fatalf("manifest metadata = %#v", manifest)
	}
	if lines[0].envelope.RawPayloadRisk != RawPayloadRiskNotice ||
		lines[0].envelope.RecordCount != 1 {
		t.Fatalf("manifest begin = %#v", lines[0].envelope)
	}

	record := decodeRecordMetadata(t, lines, 0)
	if record.SnapshotState != SnapshotStateFinal || record.RecordID != recorder.ID() ||
		len(record.GatewayTrace.Entries) != 2 {
		t.Fatalf("record metadata = %#v", record)
	}
	if record.GatewayTrace.Entries[0].Kind != TraceEntryTransition ||
		record.GatewayTrace.Entries[1].Kind != TraceEntryRecord {
		t.Fatalf("trace entries = %#v", record.GatewayTrace.Entries)
	}

	assertExportBlob(t, lines, 0, record, requestBodyBlobID, requestPayload)
	assertExportBlob(t, lines, 0, record, responseBodyBlobID, responsePayload)
	if status := manager.Status(); status.PendingExportCount != 0 ||
		status.ActiveDownloadCount != 0 ||
		status.ProcessMemory.PinnedBytes != 0 ||
		status.ProcessMemory.TemporaryBytes != 0 {
		t.Fatalf("status after completed stream = %#v", status)
	}
	if err := download.WriteTo(context.Background(), io.Discard); !errors.Is(err, ErrDownloadUnavailable) {
		t.Fatalf("second WriteTo() error = %v", err)
	}
}

func TestActiveExportSnapshotIsImmutableThroughCompletionAndEviction(t *testing.T) {
	manager := newTestManager(t, func(cfg *Config) {
		cfg.ChunkBytes = MinimumChunkBytes
		cfg.ExportLineBytes = 4096
	})
	session := startTestSession(t, manager, 1, 1<<20, "selected")
	firstGateway, first := beginTestHTTP(manager, "gateway-first", "selected", []byte("request"))
	first.ObserveResponse(HTTPResponseHead{StatusCode: http.StatusOK, Protocol: "HTTP/2.0"})
	first.ObserveUpstream([]byte("before"))

	ticket, err := manager.CreateExport(context.Background(), session.SessionID, ExportRequest{
		Scope:     ExportScopeRecords,
		RecordIDs: []string{first.ID()},
	})
	if err != nil {
		t.Fatalf("CreateExport() error = %v", err)
	}
	first.ObserveUpstream([]byte("-after"))
	first.Finish(Outcome{
		SourceCompletion:  SourceCompletionComplete,
		TerminationReason: TerminationReasonEOF,
	})
	firstGateway.Finish(GatewayOutcome{})

	secondGateway, second := beginTestHTTP(manager, "gateway-second", "selected", nil)
	completeHTTP(second, []byte("newest"))
	secondGateway.Finish(GatewayOutcome{})
	if _, err := readRecordDetailForTest(t, manager, session.SessionID, first.ID(), 64); !errors.Is(err, ErrRecordEvicted) {
		t.Fatalf("first GetRecord() error = %v", err)
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
	if record.SnapshotState != SnapshotStateActivePartial ||
		record.SnapshotReason != snapshotWhileActive ||
		record.Summary.LifecycleState != LifecycleStateActive {
		t.Fatalf("active record metadata = %#v", record)
	}
	assertExportBlob(t, lines, 0, record, responseBodyBlobID, []byte("before"))
	if record.RecordID == second.ID() {
		t.Fatal("record created after snapshot entered export membership")
	}
	if manager.Status().ProcessMemory.PinnedBytes != 0 {
		t.Fatal("snapshot pins were not released after download")
	}
}

func TestExportTruncatingWriterLeavesMissingEndEventsAndReleasesLease(t *testing.T) {
	manager, _, ticket := newTokenTestExport(t, nil)
	download, err := manager.AcceptDownload(ticket.ExportID, ticket.DownloadToken)
	if err != nil {
		t.Fatalf("AcceptDownload() error = %v", err)
	}
	wantErr := errors.New("client disconnected")
	destination := &failOnWrite{failAt: 6, err: wantErr}
	if err := download.WriteTo(context.Background(), destination); !errors.Is(err, wantErr) {
		t.Fatalf("WriteTo() error = %v", err)
	}
	lines := decodeExportLines(t, destination.buffer.Bytes(), manager.cfg.exportLineBytes)
	if len(lines) == 0 || lines[0].envelope.Event != exportEventManifest {
		t.Fatalf("truncated events = %v", exportEventNames(lines))
	}
	for _, line := range lines {
		if line.envelope.Event == exportEventRecordEnd || line.envelope.Event == exportEventExportEnd {
			t.Fatalf("truncated stream included terminal event %q", line.envelope.Event)
		}
	}
	status := manager.Status()
	if status.ActiveDownloadCount != 0 || status.ProcessMemory.PinnedBytes != 0 ||
		status.ProcessMemory.TemporaryBytes != 0 {
		t.Fatalf("status after writer failure = %#v", status)
	}
}

func TestExportCancellationAndSessionLifecycleReleaseEveryLease(t *testing.T) {
	t.Run("request context cancellation", func(t *testing.T) {
		manager, _, ticket := newTokenTestExport(t, nil)
		download, err := manager.AcceptDownload(ticket.ExportID, ticket.DownloadToken)
		if err != nil {
			t.Fatalf("AcceptDownload() error = %v", err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := download.WriteTo(ctx, io.Discard); !errors.Is(err, context.Canceled) {
			t.Fatalf("WriteTo() error = %v", err)
		}
		assertNoExportLease(t, manager)
	})

	for _, test := range []struct {
		name   string
		action func(*Manager, SessionInfo) error
	}{
		{name: "stop", action: func(manager *Manager, session SessionInfo) error {
			return manager.Stop(session.SessionID)
		}},
		{name: "close", action: func(manager *Manager, _ SessionInfo) error {
			return manager.Close()
		}},
	} {
		t.Run("active stream "+test.name, func(t *testing.T) {
			manager, session, ticket := newTokenTestExport(t, nil)
			download, err := manager.AcceptDownload(ticket.ExportID, ticket.DownloadToken)
			if err != nil {
				t.Fatalf("AcceptDownload() error = %v", err)
			}
			var lifecycleErr error
			destination := &afterFirstWrite{
				destination: io.Discard,
				after: func() {
					lifecycleErr = test.action(manager, session)
				},
			}
			if err := download.WriteTo(context.Background(), destination); !errors.Is(err, ErrExportCanceled) {
				t.Fatalf("WriteTo() error = %v", err)
			}
			if lifecycleErr != nil {
				t.Fatalf("lifecycle action error = %v", lifecycleErr)
			}
			assertNoExportLease(t, manager)
		})

		t.Run("pending lease "+test.name, func(t *testing.T) {
			manager, session, _ := newTokenTestExport(t, nil)
			if manager.Status().ProcessMemory.PinnedBytes == 0 {
				t.Fatal("pending lease has no pins")
			}
			if err := test.action(manager, session); err != nil {
				t.Fatalf("lifecycle action error = %v", err)
			}
			assertNoExportLease(t, manager)
		})

		t.Run("claimed lease "+test.name, func(t *testing.T) {
			manager, session, ticket := newTokenTestExport(t, nil)
			download, err := manager.AcceptDownload(ticket.ExportID, ticket.DownloadToken)
			if err != nil {
				t.Fatalf("AcceptDownload() error = %v", err)
			}
			if status := manager.Status(); status.ActiveDownloadCount != 1 ||
				status.ProcessMemory.TemporaryBytes == 0 {
				t.Fatalf("claimed status = %#v", status)
			}
			if err := test.action(manager, session); err != nil {
				t.Fatalf("lifecycle action error = %v", err)
			}
			// Claim commit transfers physical ownership to the returned handle.
			// Lifecycle teardown may detach and cancel it, but cannot free memory
			// that Close or WriteTo can still reach.
			if status := manager.Status(); status.ActiveDownloadCount != 1 ||
				status.ProcessMemory.TemporaryBytes == 0 ||
				status.ProcessMemory.ReleasingBytes == 0 {
				t.Fatalf("detached claimed status = %#v", status)
			}
			if err := download.Close(); err != nil {
				t.Fatalf("Close() error = %v", err)
			}
			assertNoExportLease(t, manager)
			if err := download.WriteTo(context.Background(), io.Discard); !errors.Is(err, ErrDownloadUnavailable) {
				t.Fatalf("closed claimed WriteTo() error = %v", err)
			}
		})
	}
}

func TestStopCancelsAcquiringExportBeforeWaitingForSessionGate(t *testing.T) {
	manager := newTestManager(t, nil)
	sessionInfo := startTestSession(t, manager, 2, 1<<20, "selected")
	session := manager.active.Load()
	state := &exportState{
		manager:   manager,
		id:        "acquiring-export",
		sessionID: session.id,
		session:   session,
		phase:     exportPhaseAcquiring,
	}

	// Holding the read gate models a snapshot in progress. Stop must still hide
	// the session and publish cancellation before teardown can take the write gate.
	session.gate.RLock()
	insertExportStateForTest(t, manager, state.id, state)

	stopDone := make(chan error, 1)
	go func() {
		stopDone <- manager.Stop(sessionInfo.SessionID)
	}()

	deadline := time.Now().Add(time.Second)
	for manager.Enabled() || !state.canceled.Load() {
		if time.Now().After(deadline) {
			session.gate.RUnlock()
			t.Fatal("Stop did not hide the session and cancel acquisition promptly")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case err := <-stopDone:
		session.gate.RUnlock()
		t.Fatalf("Stop returned before the in-progress snapshot released its gate: %v", err)
	default:
	}

	session.gate.RUnlock()
	select {
	case err := <-stopDone:
		if err != nil {
			t.Fatalf("Stop() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Stop did not complete after acquisition released its gate")
	}
	manager.exportMu.Lock()
	retained := manager.lookupExportLocked(state.id)
	manager.exportMu.Unlock()
	if retained != nil {
		t.Fatal("canceled acquiring export remains registered")
	}
}

func TestCanceledSnapshotAcquisitionDoesNotReserveOrPin(t *testing.T) {
	manager := newTestManager(t, nil)
	startTestSession(t, manager, 2, 1<<20, "selected")
	gateway, recorder := beginTestHTTP(manager, "gateway-canceled-snapshot", "selected", []byte("payload"))
	completeHTTP(recorder, []byte("response"))
	gateway.Finish(GatewayOutcome{})

	session := manager.active.Load()
	state := &exportState{phase: exportPhaseAcquiring, done: make(chan struct{})}
	before := manager.Status().ProcessMemory
	selectionRequest, err := inspectExportSelectionRequest(
		newExportCancellationProbe(context.Background(), nil),
		ExportRequest{Scope: ExportScopeRecords, RecordIDs: []string{recorder.ID()}},
	)
	if err != nil {
		t.Fatalf("inspectExportSelectionRequest() error = %v", err)
	}
	selection, err := materializeExportSelection(
		newExportCancellationProbe(context.Background(), state),
		selectionRequest,
	)
	if err != nil {
		t.Fatalf("materializeExportSelection() error = %v", err)
	}
	session.mu.Lock()
	source, err := freezeExportReadSourceLocked(
		newExportCancellationProbe(context.Background(), state),
		session,
		selection,
		state,
	)
	session.mu.Unlock()
	if err != nil {
		t.Fatalf("freezeExportReadSourceLocked() error = %v", err)
	}
	state.canceled.Store(true)
	if _, err := acquireExportSnapshot(
		newExportCancellationProbe(context.Background(), state),
		source,
	); !errors.Is(err, ErrNoActiveSession) {
		t.Fatalf("acquireExportSnapshot() error = %v", err)
	}
	after := manager.Status().ProcessMemory
	if after.ChargedBytes != before.ChargedBytes || after.PinnedBytes != before.PinnedBytes {
		t.Fatalf("snapshot cancellation leaked accounting: before=%#v after=%#v", before, after)
	}
}

func TestExportInvariantFaultsFailClosedWithoutPanicking(t *testing.T) {
	manager := newTestManager(t, nil)
	startTestSession(t, manager, 2, 1<<20, "selected")
	session := manager.active.Load()

	t.Run("unowned admission release", func(t *testing.T) {
		guard := exportAdmissionGuard{
			slot:   session.exportAdmission,
			logger: manager.cfg.logger,
			active: true,
		}
		guard.release()
	})

	t.Run("corrupt lease attach", func(t *testing.T) {
		state := &exportState{
			manager:     manager,
			registryKey: "fault-attach",
			sessionID:   session.id,
			session:     session,
			phase:       exportPhaseAcquiring,
			leaseCharge: 1,
			done:        make(chan struct{}),
		}
		insertExportStateForTest(t, manager, state.registryKey, state)
		session.mu.Lock()
		fault, err := state.attachLeaseLocked()
		session.mu.Unlock()
		state.failInvariant(fault)
		manager.exportMu.Lock()
		manager.removeExportLocked(state.registryKey, state)
		manager.exportMu.Unlock()

		if !errors.Is(err, ErrInternalFailure) || !state.canceled.Load() {
			t.Fatalf("attachLeaseLocked() error=%v canceled=%v", err, state.canceled.Load())
		}
	})

	t.Run("unexpected stream phase", func(t *testing.T) {
		state := &exportState{
			manager:     manager,
			registryKey: "fault-finish",
			sessionID:   session.id,
			session:     session,
			phase:       exportPhaseClaimed,
			done:        make(chan struct{}),
		}
		insertExportStateForTest(t, manager, state.registryKey, state)
		if err := state.finishDownload(nil); !errors.Is(err, ErrInternalFailure) {
			t.Fatalf("finishDownload() error = %v", err)
		}
		manager.exportMu.Lock()
		retained := manager.lookupExportLocked(state.registryKey)
		manager.exportMu.Unlock()
		if retained != nil {
			t.Fatal("faulted stream remained registered")
		}
	})

	t.Run("corrupt workspace accounting", func(t *testing.T) {
		state := &exportState{
			manager:         manager,
			sessionID:       session.id,
			session:         session,
			temporaryCharge: 1,
			workspace:       []byte{1},
			done:            make(chan struct{}),
		}
		state.release("fault_injection")
		if !state.canceled.Load() || state.workspace != nil {
			t.Fatalf("fault cleanup canceled=%v workspace=%v", state.canceled.Load(), state.workspace)
		}
	})
}

func TestCreateExportFailsClosedWhenIDGenerationFails(t *testing.T) {
	manager := newTestManager(t, nil)
	session := startTestSession(t, manager, 2, 1<<20, "selected")
	baseline := manager.Status().ProcessMemory
	manager.cfg.idGenerator = failingExportIDGenerator{err: errors.New("entropy unavailable")}

	_, err := manager.CreateExport(
		context.Background(),
		session.SessionID,
		ExportRequest{Scope: ExportScopeAll},
	)
	if !errors.Is(err, ErrInternalFailure) {
		t.Fatalf("CreateExport() error = %v, want ErrInternalFailure", err)
	}
	status := manager.Status()
	if status.PendingExportCount != 0 ||
		status.ProcessMemory.ChargedBytes != baseline.ChargedBytes ||
		status.ProcessMemory.PinnedBytes != baseline.PinnedBytes ||
		status.ProcessMemory.TemporaryBytes != baseline.TemporaryBytes {
		t.Fatalf("ID generation failure leaked export ownership: before=%#v after=%#v", baseline, status)
	}
}

func TestExportFailureJSONOmitsAbsentOptionalFacts(t *testing.T) {
	line := &fixedJSONLine{storage: make([]byte, 1024)}
	writer := &jsonDocumentWriter{sink: line}
	writeExportFailureObservationJSON(writer, FailureObservation{
		Primary: FailureFact{
			Site:  FailureSiteTransport,
			Peer:  FailurePeerUpstream,
			Class: FailureClassTransport,
			Code:  FailureCodeRoundTrip,
		},
	})
	if writer.err != nil {
		t.Fatalf("writeExportFailureObservationJSON() error = %v", writer.err)
	}

	var observation map[string]json.RawMessage
	if err := json.Unmarshal(line.bytes(), &observation); err != nil {
		t.Fatalf("decode failure observation: %v", err)
	}
	if _, present := observation["secondary"]; present {
		t.Fatal("absent secondary failure was serialized")
	}
	if string(observation["has_secondary"]) != "false" ||
		string(observation["truncated"]) != "false" {
		t.Fatalf("failure presence flags = %#v", observation)
	}

	var primary map[string]json.RawMessage
	if err := json.Unmarshal(observation["primary"], &primary); err != nil {
		t.Fatalf("decode primary failure: %v", err)
	}
	for _, field := range []string{
		"http_status_code",
		"websocket_close_code",
		"system_error_code",
		"message",
	} {
		if _, present := primary[field]; present {
			t.Fatalf("absent failure field %q was serialized", field)
		}
	}
}

func TestExportJSONAlwaysEmitsFailurePresencePair(t *testing.T) {
	tests := []struct {
		name  string
		write func(*jsonDocumentWriter)
	}{
		{
			name: "record summary",
			write: func(writer *jsonDocumentWriter) {
				writeRecordSummaryJSON(writer, RecordSummary{})
			},
		},
		{
			name: "trace entry",
			write: func(writer *jsonDocumentWriter) {
				writeTraceEntryJSON(writer, TraceEntry{})
			},
		},
		{
			name: "message",
			write: func(writer *jsonDocumentWriter) {
				writeMessageJSON(writer, exportMessageSnapshot{})
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			line := &fixedJSONLine{storage: make([]byte, 4096)}
			writer := &jsonDocumentWriter{sink: line}
			test.write(writer)
			if writer.err != nil {
				t.Fatalf("write export JSON: %v", writer.err)
			}

			var document map[string]json.RawMessage
			if err := json.Unmarshal(line.bytes(), &document); err != nil {
				t.Fatalf("decode export JSON: %v", err)
			}
			failureJSON, present := document["failure"]
			if !present {
				t.Fatal("required failure value was omitted")
			}
			var failure FailureObservation
			if err := json.Unmarshal(failureJSON, &failure); err != nil {
				t.Fatalf("decode failure value: %v", err)
			}
			if failure != (FailureObservation{}) {
				t.Fatalf("failure value = %#v, want zero value", failure)
			}
			if raw, present := document["has_failure"]; !present || string(raw) != "false" {
				t.Fatalf("has_failure = %s, want false", raw)
			}
		})
	}
}

func TestCreateExportSelectionValidationAndCaptureOrdering(t *testing.T) {
	manager := newTestManager(t, nil)
	session := startTestSession(t, manager, 3, 1<<20, "selected")
	var recorders []Recorder
	for index := range 2 {
		gateway, recorder := beginTestHTTP(manager, fmt.Sprintf("gateway-%d", index), "selected", nil)
		completeHTTP(recorder, []byte{byte(index)})
		gateway.Finish(GatewayOutcome{})
		recorders = append(recorders, recorder)
	}

	for _, request := range []ExportRequest{
		{},
		{Scope: ExportScopeAll, RecordIDs: []string{recorders[0].ID()}},
		{Scope: ExportScopeRecords},
		{Scope: ExportScopeRecords, RecordIDs: []string{" "}},
		{Scope: ExportScopeRecords, RecordIDs: []string{" " + recorders[0].ID() + " "}},
		{Scope: ExportScopeRecords, RecordIDs: []string{recorders[0].ID(), recorders[0].ID()}},
	} {
		if _, err := manager.CreateExport(context.Background(), session.SessionID, request); err == nil {
			t.Fatalf("CreateExport(%#v) succeeded", request)
		}
	}

	ticket, err := manager.CreateExport(context.Background(), session.SessionID, ExportRequest{
		Scope:     ExportScopeRecords,
		RecordIDs: []string{recorders[1].ID(), recorders[0].ID()},
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
	first := decodeRecordMetadata(t, lines, 0)
	second := decodeRecordMetadata(t, lines, 1)
	if first.RecordID != recorders[0].ID() || second.RecordID != recorders[1].ID() {
		t.Fatalf("record order = %q, %q", first.RecordID, second.RecordID)
	}
}

func TestCreateExportSelectionRejectsResourceAbuseBeforeAdmission(t *testing.T) {
	manager := newTestManager(t, nil)
	session := startTestSession(t, manager, 2, 1<<20, "selected")
	gateway, recorder := beginTestHTTP(manager, "selection-bounds", "selected", nil)
	completeHTTP(recorder, nil)
	gateway.Finish(GatewayOutcome{})

	baseline := manager.Status()
	tooMany := make([]string, maximumExportSelectedRecordIDs+1)
	for index := range tooMany {
		tooMany[index] = recorder.ID()
	}
	requests := []ExportRequest{
		{Scope: ExportScopeRecords, RecordIDs: tooMany},
		{
			Scope: ExportScopeRecords,
			RecordIDs: []string{
				strings.Repeat("x", maximumExportSelectionIDBytes+1),
			},
		},
	}
	for _, request := range requests {
		_, err := manager.CreateExport(context.Background(), session.SessionID, request)
		var validation *ValidationError
		if !errors.As(err, &validation) {
			t.Fatalf("CreateExport() error = %v, want ValidationError", err)
		}
	}

	status := manager.Status()
	if status.PendingExportCount != baseline.PendingExportCount ||
		status.ProcessMemory.ChargedBytes != baseline.ProcessMemory.ChargedBytes ||
		status.ProcessMemory.TemporaryBytes != baseline.ProcessMemory.TemporaryBytes ||
		status.ProcessMemory.PinnedBytes != baseline.ProcessMemory.PinnedBytes {
		t.Fatalf("rejected selections changed accounting: before=%#v after=%#v", baseline, status)
	}
}

func TestExportSelectionTightClonesCanonicalIDSubstring(t *testing.T) {
	manager := newTestManager(t, nil)
	startTestSession(t, manager, 2, 1<<20, "selected")
	session := manager.active.Load()
	recordID := session.makeRecordID(1)

	// Pointer separation proves the selection cannot retain the source owner.
	// A multi-megabyte backing is enough to catch aliasing; allocating a GiB
	// would exercise the same pointer relationship without additional coverage.
	backing := recordID + strings.Repeat("x", 4<<20)
	borrowed := backing[:len(recordID)]
	request, err := inspectExportSelectionRequest(newExportCancellationProbe(context.Background(), nil), ExportRequest{
		Scope:     ExportScopeRecords,
		RecordIDs: []string{borrowed},
	})
	if err != nil {
		t.Fatalf("inspectExportSelectionRequest() error = %v", err)
	}
	wantCharge := exportSelectionBaseChargeBytes +
		exportSelectionEntryChargeBytes +
		int64(len(recordID))
	if request.selectionCharge != wantCharge {
		t.Fatalf("selection charge = %d, want %d", request.selectionCharge, wantCharge)
	}

	state := &exportState{done: make(chan struct{})}
	selection, err := materializeExportSelection(
		newExportCancellationProbe(context.Background(), state),
		request,
	)
	if err != nil {
		t.Fatalf("materializeExportSelection() error = %v", err)
	}
	if unsafe.StringData(selection.recordIDs[0]) == unsafe.StringData(borrowed) {
		t.Fatal("materialized selection retained caller string backing")
	}
	for selectedID := range selection.records {
		if unsafe.StringData(selectedID) == unsafe.StringData(borrowed) {
			t.Fatal("selection map retained caller string backing")
		}
	}

	selection.clear()
	if selection.recordIDs != nil || selection.records != nil {
		t.Fatalf("selection clear retained ownership: %#v", selection)
	}
}

func TestMaximumSelectionAccountingIsReleasedAtFreezeBoundary(t *testing.T) {
	manager := newTestManager(t, func(cfg *Config) {
		cfg.ProcessCeilingBytes = 8 << 20
		cfg.DefaultSessionQuotaBytes = 8 << 20
	})
	startTestSession(t, manager, 2, 8<<20, "selected")
	session := manager.active.Load()
	baseline := manager.Status().ProcessMemory

	recordIDs := make([]string, maximumExportSelectedRecordIDs)
	for index := range recordIDs {
		recordIDs[index] = session.makeRecordID(uint64(index + 1))
	}
	request, err := inspectExportSelectionRequest(newExportCancellationProbe(context.Background(), nil), ExportRequest{
		Scope:     ExportScopeRecords,
		RecordIDs: recordIDs,
	})
	if err != nil {
		t.Fatalf("inspectExportSelectionRequest() error = %v", err)
	}
	state, err := manager.beginExportAcquisition(session, request.selectionCharge)
	if err != nil {
		t.Fatalf("beginExportAcquisition() error = %v", err)
	}
	var selection *exportSelection
	defer func() {
		selection.clear()
		_ = state.releaseSelectionAccounting()
		_ = manager.abortExportAcquisition(state, context.Canceled, "test_cleanup")
	}()

	selection, err = materializeExportSelection(
		newExportCancellationProbe(context.Background(), state),
		request,
	)
	if err != nil {
		t.Fatalf("materializeExportSelection() error = %v", err)
	}
	session.mu.Lock()
	fault, err := state.attachLeaseLocked()
	session.mu.Unlock()
	if fault != exportInvariantNone || err != nil {
		t.Fatalf("attachLeaseLocked() fault=%v error=%v", fault, err)
	}

	// This is the ownership transition createExport performs immediately after
	// freeze: the immutable source replaces the transient selection graph.
	selection.clear()
	selection = nil
	if err := state.releaseSelectionAccounting(); err != nil {
		t.Fatalf("releaseSelectionAccounting() error = %v", err)
	}
	baseLeaseCharge, valid := exportLeaseCharge(session.id, 0)
	if !valid || state.acquisitionCharge > math.MaxInt64-baseLeaseCharge {
		t.Fatal("retained export lease charge overflowed")
	}
	// The acquiring registry key remains reachable until canonical publication;
	// only the now-severed selection graph is released at this boundary.
	retainedLeaseCharge := baseLeaseCharge + state.acquisitionCharge
	status := manager.Status().ProcessMemory
	if state.selectionCharge != 0 || state.leaseCharge != retainedLeaseCharge {
		t.Fatalf(
			"state charges after selection release = selection:%d lease:%d, want 0/%d",
			state.selectionCharge,
			state.leaseCharge,
			retainedLeaseCharge,
		)
	}
	if status.ChargedBytes-baseline.ChargedBytes != retainedLeaseCharge+manager.exportRegistryCharge ||
		status.PinnedBytes-baseline.PinnedBytes != retainedLeaseCharge ||
		status.TemporaryBytes != baseline.TemporaryBytes {
		t.Fatalf("selection remained accounted after freeze boundary: before=%#v after=%#v", baseline, status)
	}
}

func TestStopRemainsPromptAtMaximumExportSelection(t *testing.T) {
	const stopDeadline = 500 * time.Millisecond

	manager := newTestManager(t, func(cfg *Config) {
		cfg.ProcessCeilingBytes = 8 << 20
		cfg.DefaultSessionQuotaBytes = 8 << 20
	})
	sessionInfo := startTestSession(t, manager, 2, 8<<20, "selected")
	session := manager.active.Load()

	// Occupying admission lets the selection finish without entering either
	// session lock. Stop must cancel that admitted, fully charged work directly.
	session.exportAdmission <- struct{}{}
	t.Cleanup(func() {
		select {
		case <-session.exportAdmission:
		default:
		}
	})

	recordIDs := make([]string, maximumExportSelectedRecordIDs)
	for index := range recordIDs {
		recordIDs[index] = session.makeRecordID(uint64(index + 1))
	}
	createDone := make(chan error, 1)
	go func() {
		_, err := manager.CreateExport(context.Background(), sessionInfo.SessionID, ExportRequest{
			Scope:     ExportScopeRecords,
			RecordIDs: recordIDs,
		})
		createDone <- err
	}()

	var state *exportState
	deadline := time.Now().Add(time.Second)
	for state == nil || !state.selectionMaterialized.Load() {
		manager.exportMu.Lock()
		for index := 0; index < manager.exports.Capacity(); index++ {
			_, candidate, occupied := manager.exports.Entry(index)
			if occupied && candidate != nil && candidate.sessionID == sessionInfo.SessionID {
				state = candidate
				break
			}
		}
		manager.exportMu.Unlock()
		if time.Now().After(deadline) {
			t.Fatal("maximum selection did not reach the admitted wait")
		}
		time.Sleep(time.Millisecond)
	}

	stopStarted := time.Now()
	if err := manager.Stop(sessionInfo.SessionID); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if elapsed := time.Since(stopStarted); elapsed > stopDeadline {
		t.Fatalf("Stop() latency = %v, want <= %v", elapsed, stopDeadline)
	}

	select {
	case err := <-createDone:
		if !errors.Is(err, ErrNoActiveSession) {
			t.Fatalf("CreateExport() error = %v, want ErrNoActiveSession", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled maximum selection did not exit")
	}
	assertNoExportLease(t, manager)
}
