package requestcapture

import (
	"bytes"
	"context"
	"net/http"
	"sync"
	"testing"
)

func TestIngressReplayFailurePreservesEOFAndHistoricalSnapshots(t *testing.T) {
	manager := newTestManager(t, nil)
	session := startTestSession(t, manager, 4, 1<<20, "selected")
	gateway := manager.BeginGateway(GatewayStart{GatewayRequestID: "late-replay-failure"})
	ingress := gateway.BeginIngress(IngressHead{Protocol: "HTTP/2.0", ContentLength: 7})
	ingress.ObserveChunk([]byte("payload"))
	ingress.FinishIngress(IngressFinish{State: "complete", ReceivedBytes: 7, Trailers: http.Header{"X-End": {"done"}}})
	recorder := beginIngressAttempt(gateway)
	before, err := readRecordDetailForTest(t, manager, session.SessionID, recorder.ID(), 64)
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := manager.CreateExport(context.Background(), session.SessionID, ExportRequest{Scope: ExportScopeRecords, RecordIDs: []string{recorder.ID()}})
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for range 8 {
		group.Go(func() {
			ingress.ObserveFailure(IngressFailure{Kind: IngressFailureStorage, Reason: "disk read failed"})
		})
	}
	group.Wait()
	ingress.ObserveFailure(IngressFailure{Kind: IngressFailureRead, Reason: "duplicate"})
	after, err := readRecordDetailForTest(t, manager, session.SessionID, recorder.ID(), 64)
	if err != nil {
		t.Fatal(err)
	}
	facts := after.HTTP.Request.Ingress
	if facts.State != "complete" || facts.ReceivedBytes != 7 || facts.Trailers["X-End"][0] != "done" ||
		facts.SourceFailure == nil || facts.SourceFailure.Kind != IngressFailureStorage || facts.SourceFailure.Reason != "disk read failed" {
		t.Fatalf("late failure erased input facts: %+v", facts)
	}
	if before.HTTP.Request.Ingress.SourceFailure != nil {
		t.Fatal("earlier query gained a future failure")
	}
	assertFailureExport := func(ticket ExportTicket, wantFailure bool) {
		t.Helper()
		download, err := manager.AcceptDownload(ticket.ExportID, ticket.DownloadToken)
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		if err := download.WriteTo(context.Background(), &out); err != nil {
			t.Fatal(err)
		}
		lines := decodeExportLines(t, out.Bytes(), manager.cfg.exportLineBytes)
		record := decodeRecordMetadata(t, lines, 0)
		if (record.Request.Ingress.SourceFailure != nil) != wantFailure {
			t.Fatalf("export failure=%+v want presence=%v", record.Request.Ingress.SourceFailure, wantFailure)
		}
		assertExportBlob(t, lines, 0, record, requestBodyBlobID, []byte("payload"))
	}
	assertFailureExport(ticket, false)
	finalTicket, err := manager.CreateExport(context.Background(), session.SessionID, ExportRequest{Scope: ExportScopeRecords, RecordIDs: []string{recorder.ID()}})
	if err != nil {
		t.Fatal(err)
	}
	assertFailureExport(finalTicket, true)
	completeHTTP(recorder, nil)
	gateway.Finish(GatewayOutcome{})
	ingress.ObserveFailure(IngressFailure{})
}

func TestIngressFailureBudgetDenialIsVisibleAndIdempotent(t *testing.T) {
	(IngressRecorder{}).ObserveFailure(IngressFailure{})
	manager := newTestManager(t, nil)
	session := startTestSession(t, manager, 4, 1<<20, "selected")
	gateway := manager.BeginGateway(GatewayStart{GatewayRequestID: "failure-budget"})
	ingress := gateway.BeginIngress(IngressHead{Protocol: "HTTP/1.1", ContentLength: 0})
	recorder := beginIngressAttempt(gateway)
	restore := constrainAdditionalCapacity(manager.active.Load(), 0)
	ingress.ObserveFailure(IngressFailure{Kind: IngressFailureStorage, Reason: "no capture capacity"})
	restore()
	ingress.ObserveFailure(IngressFailure{Kind: IngressFailureRead, Reason: "replacement must not rewrite missing evidence"})
	ingress.FinishIngress(IngressFinish{State: "complete"})
	detail, err := readRecordDetailForTest(t, manager, session.SessionID, recorder.ID(), 64)
	if err != nil {
		t.Fatal(err)
	}
	if detail.HTTP.Request.Ingress.SourceFailure != nil || !detail.HTTP.Request.Ingress.CaptureTruncated || detail.Summary.CaptureCompletion != CaptureCompletionOverflowed {
		t.Fatalf("failure annotation loss hidden: %+v", detail.HTTP.Request.Ingress)
	}
	gateway.Finish(GatewayOutcome{})
}
