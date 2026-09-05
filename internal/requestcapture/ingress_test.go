package requestcapture

import (
	"bytes"
	"context"
	"encoding/base64"
	"net/http"
	"strings"
	"testing"
)

func beginIngressAttempt(gateway GatewayRecorder) Recorder {
	return gateway.BeginHTTP(RawHTTPStart{
		URL:     testParsedURL("https://selected.test/upload"),
		Attempt: AttemptMetadata{Provider: ProviderIdentity{ID: "selected"}},
		Request: RawRequest{Method: http.MethodPost, ContentLength: -1,
			SensitiveHeaders: testSensitiveHeaderEvidence(), CredentialEvidence: testCredentialEvidence()},
	})
}

func TestIngressSharesLiveBodyAndFreezesQueryAndExport(t *testing.T) {
	manager := newTestManager(t, nil)
	session := startTestSession(t, manager, 4, 1<<20, "selected")
	gateway := manager.BeginGateway(GatewayStart{GatewayRequestID: "ingress-shared"})
	head := IngressHead{Protocol: "HTTP/2.0", ContentLength: 6, TransferEncoding: []string{"identity"}, TrailerKeys: []string{"X-End"}}
	ingress := gateway.BeginIngress(head)
	head.TransferEncoding[0] = "mutated"
	head.TrailerKeys[0] = "mutated"
	chunk := []byte("abc")
	ingress.ObserveChunk(chunk)
	chunk[0] = 'x'
	first := beginIngressAttempt(gateway)
	active, err := readRecordDetailForTest(t, manager, session.SessionID, first.ID(), 64)
	if err != nil {
		t.Fatal(err)
	}
	if active.HTTP.Request.Ingress.State != "receiving" || active.HTTP.Request.Ingress.ReceivedBytes != 3 ||
		active.HTTP.Request.Ingress.ContentLength != 6 || active.HTTP.Request.ContentLength != -1 ||
		active.HTTP.Request.Ingress.TransferEncoding[0] != "identity" || active.HTTP.Request.Ingress.DeclaredTrailerKeys[0] != "X-End" {
		t.Fatalf("original framing: %+v", active.HTTP.Request)
	}
	ticket, err := manager.CreateExport(context.Background(), session.SessionID, ExportRequest{Scope: ExportScopeRecords, RecordIDs: []string{first.ID()}})
	if err != nil {
		t.Fatal(err)
	}
	completeHTTP(first, nil)
	second := beginIngressAttempt(gateway)
	if testRecordState(t, first).requestBody != testRecordState(t, second).requestBody {
		t.Fatal("attempts copied logical body")
	}
	ingress.ObserveChunk([]byte("def"))
	trailers := http.Header{"X-End": {"done"}, "Authorization": {"Bearer secret"}, "X-Ref": {"secret"}}
	ingress.FinishIngress(IngressFinish{State: "complete", ReceivedBytes: 6, Trailers: trailers})
	trailers["X-End"][0] = "mutated"
	ingress.ObserveChunk([]byte("ignored"))
	ingress.FinishIngress(IngressFinish{State: "failed", ReceivedBytes: 999})
	for _, recorder := range []Recorder{first, second} {
		detail, err := readRecordDetailForTest(t, manager, session.SessionID, recorder.ID(), 64)
		if err != nil {
			t.Fatal(err)
		}
		facts := detail.HTTP.Request.Ingress
		if facts.State != "complete" || facts.ReceivedBytes != 6 || facts.Trailers["X-End"][0] != "done" ||
			facts.Trailers["Authorization"][0] != "Bearer secret" || facts.Trailers["X-Ref"][0] != "secret" {
			t.Fatalf("final ingress: %+v", facts)
		}
		body, _ := base64.StdEncoding.DecodeString(detail.HTTP.RequestBody.DataBase64)
		if string(body) != "abcdef" {
			t.Fatalf("body = %q", body)
		}
	}
	if active.HTTP.Request.Ingress.State != "receiving" || active.HTTP.Request.Ingress.ReceivedBytes != 3 {
		t.Fatal("query snapshot changed")
	}
	download, err := manager.AcceptDownload(ticket.ExportID, ticket.DownloadToken)
	if err != nil {
		t.Fatal(err)
	}
	var dst bytes.Buffer
	if err := download.WriteTo(context.Background(), &dst); err != nil {
		t.Fatal(err)
	}
	lines := decodeExportLines(t, dst.Bytes(), manager.cfg.exportLineBytes)
	record := decodeRecordMetadata(t, lines, 0)
	if record.Request.Ingress == nil || record.Request.Ingress.State != "receiving" || record.Request.Ingress.ReceivedBytes != 3 {
		t.Fatalf("export ingress changed: %+v", record.Request)
	}
	assertExportBlob(t, lines, 0, record, requestBodyBlobID, []byte("abc"))
	completeHTTP(second, nil)
	gateway.Finish(GatewayOutcome{})
}

func TestIngressPartialAndCaptureBudgetDoNotChangeSourceFacts(t *testing.T) {
	for _, state := range []string{"failed", "aborted"} {
		t.Run(state, func(t *testing.T) {
			manager := newTestManager(t, nil)
			session := startTestSession(t, manager, 4, 1<<20, "selected")
			gateway := manager.BeginGateway(GatewayStart{GatewayRequestID: state})
			ingress := gateway.BeginIngress(IngressHead{Protocol: "HTTP/1.1", ContentLength: -1, TransferEncoding: []string{"chunked"}})
			recorder := beginIngressAttempt(gateway)
			restore := constrainAdditionalCapacity(manager.active.Load(), 0)
			ingress.ObserveChunk([]byte("partial"))
			ingress.FinishIngress(IngressFinish{State: state, ReceivedBytes: 7, Reason: "upload stopped", Trailers: http.Header{"X-End": {"done"}}})
			restore()
			detail, err := readRecordDetailForTest(t, manager, session.SessionID, recorder.ID(), 64)
			if err != nil {
				t.Fatal(err)
			}
			facts := detail.HTTP.Request.Ingress
			if facts.State != state || facts.ReceivedBytes != 7 || !facts.CaptureTruncated || facts.Trailers != nil ||
				detail.Summary.CaptureCompletion != CaptureCompletionOverflowed {
				t.Fatalf("partial: %+v / %+v", facts, detail.Summary)
			}
			later := beginIngressAttempt(gateway)
			if testRecordState(t, later).summary.CaptureCompletion != CaptureCompletionOverflowed {
				t.Fatal("retry lost truncation")
			}
			gateway.Finish(GatewayOutcome{})
			ingress.ObserveChunk([]byte("late"))
			ingress.FinishIngress(IngressFinish{})
		})
	}
}

func TestIngressDisabledAdmissionAndBoundedMetadata(t *testing.T) {
	disabled := (GatewayRecorder{}).BeginIngress(IngressHead{})
	disabled.ObserveChunk(nil)
	disabled.ObserveChunk([]byte("ignored"))
	disabled.FinishIngress(IngressFinish{})
	manager := newTestManager(t, nil)
	startTestSession(t, manager, 4, 1<<20, "selected")
	gateway := manager.BeginGateway(GatewayStart{GatewayRequestID: "bounded"})
	restore := constrainAdditionalCapacity(manager.active.Load(), 0)
	rejected := gateway.BeginIngress(IngressHead{})
	restore()
	rejected.ObserveChunk([]byte("ignored"))
	gateway.Finish(GatewayOutcome{})
	gateway = manager.BeginGateway(GatewayStart{GatewayRequestID: "bounded-metadata"})
	head := IngressHead{Protocol: strings.Repeat("p", 512)}
	for range 130 {
		head.TransferEncoding = append(head.TransferEncoding, strings.Repeat("e", 300))
		head.TrailerKeys = append(head.TrailerKeys, strings.Repeat("t", 300))
	}
	ingress := gateway.BeginIngress(head)
	duplicate := gateway.BeginIngress(IngressHead{})
	duplicate.ObserveChunk([]byte("ignored"))
	ingress.ObserveChunk(nil)
	recorder := beginIngressAttempt(gateway)
	ingress.FinishIngress(IngressFinish{State: "unexpected", Reason: strings.Repeat("r", 512)})
	facts := testRecordState(t, recorder).request.Ingress
	if facts.State != "failed" || !facts.CaptureTruncated || len(facts.TransferEncoding) != 128 || len(facts.DeclaredTrailerKeys) != 128 {
		t.Fatalf("bounded metadata: %+v", facts)
	}
	gateway.Finish(GatewayOutcome{})
}

func TestIngressRecordsSourceRejectedTailAndFinalExportMetadata(t *testing.T) {
	manager := newTestManager(t, nil)
	session := startTestSession(t, manager, 4, 1<<20, "selected")
	gateway := manager.BeginGateway(GatewayStart{GatewayRequestID: "rejected-tail"})
	ingress := gateway.BeginIngress(IngressHead{Protocol: "HTTP/1.1", ContentLength: 3})
	ingress.ObserveChunk([]byte("abc"))
	recorder := beginIngressAttempt(gateway)
	ingress.FinishIngress(IngressFinish{State: "failed", ReceivedBytes: 4,
		Trailers: http.Header{"X-End": {"partial"}}, Reason: "declared length exceeded"})
	completeHTTP(recorder, nil)
	gateway.Finish(GatewayOutcome{})
	ticket, err := manager.CreateExport(context.Background(), session.SessionID,
		ExportRequest{Scope: ExportScopeRecords, RecordIDs: []string{recorder.ID()}})
	if err != nil {
		t.Fatal(err)
	}
	download, err := manager.AcceptDownload(ticket.ExportID, ticket.DownloadToken)
	if err != nil {
		t.Fatal(err)
	}
	var dst bytes.Buffer
	if err := download.WriteTo(context.Background(), &dst); err != nil {
		t.Fatal(err)
	}
	lines := decodeExportLines(t, dst.Bytes(), manager.cfg.exportLineBytes)
	record := decodeRecordMetadata(t, lines, 0)
	facts := record.Request.Ingress
	if facts == nil || facts.State != "failed" || facts.ReceivedBytes != 4 || !facts.CaptureTruncated || facts.Trailers["X-End"][0] != "partial" || facts.Reason != "declared length exceeded" {
		t.Fatalf("final exported ingress = %+v", facts)
	}
	assertExportBlob(t, lines, 0, record, requestBodyBlobID, []byte("abc"))
}

func BenchmarkIncrementalIngressCapture(b *testing.B) {
	const quota = 256 << 10
	const chunkBytes = 32 << 10
	for _, enabled := range []bool{false, true} {
		for _, size := range []int{64 << 10, 8 << 20} {
			name := "disabled/"
			if enabled {
				name = "enabled/"
			}
			if size == 64<<10 {
				name += "64KiB"
			} else {
				name += "8MiB"
			}
			b.Run(name, func(b *testing.B) {
				manager, err := NewManager(Config{ProcessCeilingBytes: 2 << 20, DefaultSessionQuotaBytes: quota})
				if err != nil {
					b.Fatal(err)
				}
				defer manager.Close()
				if enabled {
					_, err := manager.Start(StartRequest{Providers: []ProviderIdentity{{ID: "selected"}}, CompletedRecordsPerProvider: 1,
						RetainedBytesLimit: quota, AcknowledgeRawPayloadRisk: true})
					if err != nil {
						b.Fatal(err)
					}
				}
				chunk := make([]byte, chunkBytes)
				b.ReportAllocs()
				b.SetBytes(int64(size))
				for b.Loop() {
					gateway := manager.BeginGateway(GatewayStart{GatewayRequestID: "benchmark-ingress"})
					ingress := gateway.BeginIngress(IngressHead{Protocol: "HTTP/1.1", ContentLength: -1})
					for received := 0; received < size; received += len(chunk) {
						ingress.ObserveChunk(chunk)
					}
					ingress.FinishIngress(IngressFinish{State: "complete", ReceivedBytes: int64(size)})
					gateway.Finish(GatewayOutcome{})
				}
			})
		}
	}
}
