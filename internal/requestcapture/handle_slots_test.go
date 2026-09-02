package requestcapture

import (
	"errors"
	"math"
	"net/http"
	"testing"
	"time"
)

func startCandidateChargeForTest(t *testing.T, manager *Manager, request StartRequest) (startShape, int64) {
	t.Helper()
	shape, err := manager.scanStart(request)
	if err != nil {
		t.Fatalf("scanStart() error = %v", err)
	}
	statusCharge := statusSlotBaseChargeBytes + int64(shape.statusJSONBytes)
	charge := sessionBaseChargeBytes + int64(maxSessionIDBytes) + shape.providerBytes +
		int64(len(request.Providers))*(2*mapEntryChargeBytes+sliceEntryChargeBytes) + statusCharge
	charge = addRetainedCharge64(
		charge,
		int64(len(request.Providers))*providerRecordIndexChargeBytes,
	)
	charge = addRetainedCharge64(charge, shape.handleSlots.charge)
	return shape, charge
}

func TestHandleSlotShapeUsesCheckedArithmetic(t *testing.T) {
	if _, valid := scanHandleSlotShape(math.MaxInt, math.MaxInt, 1, 1); valid {
		t.Fatal("overflowing provider retention produced a handle arena")
	}
	if _, valid := scanHandleSlotShape(1, 1, math.MaxInt, 1); valid {
		t.Fatal("overflowing gateway capacity produced a handle arena")
	}
	if _, valid := scanHandleSlotShape(1, 1, 1, math.MaxInt); valid {
		t.Fatal("overflowing record capacity produced a handle arena")
	}

	for range 32 {
		_, err := NewManager(Config{
			ProcessCeilingBytes:      2 << 20,
			DefaultSessionQuotaBytes: 1 << 20,
			ChunkBytes:               MinimumChunkBytes,
			ExportLineBytes:          4096,
			MaxActiveTraces:          math.MaxInt,
			MaxActiveRecords:         1,
		})
		var validation *ValidationError
		if !errors.As(err, &validation) || validation.Field != "process_ceiling_bytes" {
			t.Fatalf("NewManager huge-capacity error = %#v", err)
		}
	}
}

func TestStartChargesExactPreallocatedHandleArenaBoundary(t *testing.T) {
	newManager := func(t *testing.T) *Manager {
		return newTestManager(t, func(cfg *Config) {
			cfg.MaxActiveTraces = 2
			cfg.MaxActiveRecords = 2
		})
	}
	request := StartRequest{
		Providers:                   []ProviderIdentity{{ID: "selected", Name: "Selected"}},
		CompletedRecordsPerProvider: 1,
		RetainedBytesLimit:          1 << 20,
		AcknowledgeRawPayloadRisk:   true,
	}

	manager := newManager(t)
	shape, exactCharge := startCandidateChargeForTest(t, manager, request)
	request.RetainedBytesLimit = exactCharge
	manager.cfg.processCeilingBytes = exactCharge
	info, err := manager.Start(request)
	if err != nil {
		t.Fatalf("exact-boundary Start() error = %v", err)
	}
	session := manager.active.Load()
	if session == nil || len(session.gatewayHandleSlots) != shape.handleSlots.gatewayCount ||
		len(session.recordHandleSlots) != shape.handleSlots.recordCount {
		t.Fatalf("materialized handle arena = gateway:%d record:%d, want gateway:%d record:%d",
			len(session.gatewayHandleSlots), len(session.recordHandleSlots),
			shape.handleSlots.gatewayCount, shape.handleSlots.recordCount)
	}
	manager.mu.Lock()
	charged := manager.processCharged
	manager.mu.Unlock()
	if charged != exactCharge {
		t.Fatalf("exact-boundary process charge = %d, want %d", charged, exactCharge)
	}
	if err = manager.Stop(info.SessionID); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	denied := newManager(t)
	denied.cfg.processCeilingBytes = exactCharge - 1
	request.RetainedBytesLimit = exactCharge - 1
	if _, err = denied.Start(request); !errors.Is(err, ErrCapacityExceeded) {
		t.Fatalf("one-byte-short Start() error = %v, want ErrCapacityExceeded", err)
	}
	denied.mu.Lock()
	deniedCharge := denied.processCharged
	denied.mu.Unlock()
	if deniedCharge != 0 || denied.active.Load() != nil {
		t.Fatalf("denied Start retained state: charged=%d active=%p", deniedCharge, denied.active.Load())
	}
}

func TestHandleSlotsRejectSequenceAndGenerationABA(t *testing.T) {
	manager := newTestManager(t, func(cfg *Config) {
		cfg.MaxActiveTraces = 1
		cfg.MaxActiveRecords = 1
	})
	firstSession := startTestSession(t, manager, 1, 1<<20, "selected")

	firstGateway := manager.BeginGateway(GatewayStart{GatewayRequestID: "empty-first"})
	firstGatewaySlot := firstGateway.handleSlot
	firstGateway.Finish(GatewayOutcome{})
	secondGateway := manager.BeginGateway(GatewayStart{GatewayRequestID: "record-first"})
	if secondGateway.handleSlot != firstGatewaySlot || firstGateway.Valid() || !secondGateway.Valid() {
		t.Fatalf("gateway slot sequence ABA: first=%#v second=%#v", firstGateway, secondGateway)
	}
	firstRecord := secondGateway.BeginHTTP(RawHTTPStart{
		URL:     testParsedURL("https://selected.test/first"),
		Attempt: AttemptMetadata{Provider: ProviderIdentity{ID: "selected"}},
	})
	completeHTTP(firstRecord, []byte("first"))
	secondGateway.Finish(GatewayOutcome{})

	thirdGateway := manager.BeginGateway(GatewayStart{GatewayRequestID: "record-second"})
	secondRecord := thirdGateway.BeginHTTP(RawHTTPStart{
		URL:     testParsedURL("https://selected.test/second"),
		Attempt: AttemptMetadata{Provider: ProviderIdentity{ID: "selected"}},
	})
	completeHTTP(secondRecord, []byte("second"))
	thirdGateway.Finish(GatewayOutcome{})
	if firstRecord.Valid() {
		t.Fatal("evicted record handle still resolved")
	}

	fourthGateway := manager.BeginGateway(GatewayStart{GatewayRequestID: "record-third"})
	thirdRecord := fourthGateway.BeginHTTP(RawHTTPStart{
		URL:     testParsedURL("https://selected.test/third"),
		Attempt: AttemptMetadata{Provider: ProviderIdentity{ID: "selected"}},
	})
	if thirdRecord.recordSlot != firstRecord.recordSlot || firstRecord.Valid() || !thirdRecord.Valid() {
		t.Fatalf("record slot sequence ABA: first=%#v third=%#v", firstRecord, thirdRecord)
	}
	if err := manager.Stop(firstSession.SessionID); err != nil {
		t.Fatalf("first Stop() error = %v", err)
	}

	secondSession := startTestSession(t, manager, 1, 1<<20, "selected")
	replacement := manager.BeginGateway(GatewayStart{GatewayRequestID: "replacement"})
	if replacement.handleSlot != firstGatewaySlot || firstGateway.Valid() || secondGateway.Valid() || fourthGateway.Valid() {
		t.Fatalf("generation ABA resolved stale handle: replacement=%#v", replacement)
	}
	if err := manager.Stop(secondSession.SessionID); err != nil {
		t.Fatalf("second Stop() error = %v", err)
	}
}

func TestMessageRefIdentitySurvivesEvictionAndStop(t *testing.T) {
	manager := newTestManager(t, func(cfg *Config) {
		cfg.MaxActiveTraces = 2
		cfg.MaxActiveRecords = 2
	})
	session := startTestSession(t, manager, 1, 1<<20, "selected")
	gateway := manager.BeginGateway(GatewayStart{GatewayRequestID: "message-owner"})
	recorder := gateway.BeginWebSocket(RawWebSocketStart{
		TargetURL: "wss://selected.test/socket",
		Attempt:   AttemptMetadata{Provider: ProviderIdentity{ID: "selected"}},
	})
	recorder.ObserveWebSocketHandshake(WebSocketHandshake{StatusCode: http.StatusSwitchingProtocols})
	lineage := gateway.NewMessageID()
	ref := recorder.MessageRead(MessageRead{
		Lineage:   lineage,
		Direction: MessageDirectionClientToUpstream,
		Type:      MessageTypeText,
		Payload:   []byte("payload"),
		Source:    MessageSourceLive,
	})
	issuedID := ref.ID()
	issuedSequence := ref.Sequence()
	issuedLineage := ref.Lineage()
	if issuedID == "" || issuedSequence == 0 || !issuedLineage.Valid() {
		t.Fatalf("issued message reference = %#v", ref)
	}
	recorder.Finish(Outcome{SourceCompletion: SourceCompletionComplete, TerminationReason: TerminationReasonEOF})
	gateway.Finish(GatewayOutcome{})

	replacementGateway := manager.BeginGateway(GatewayStart{GatewayRequestID: "evictor"})
	replacementRecord := replacementGateway.BeginHTTP(RawHTTPStart{
		URL:     testParsedURL("https://selected.test/evict"),
		Attempt: AttemptMetadata{Provider: ProviderIdentity{ID: "selected"}},
	})
	completeHTTP(replacementRecord, nil)
	replacementGateway.Finish(GatewayOutcome{})
	if ref.ID() != issuedID || ref.Sequence() != issuedSequence || ref.Lineage() != issuedLineage {
		t.Fatalf("message identity changed after eviction: ref=%#v id=%q", ref, ref.ID())
	}
	if err := manager.Stop(session.SessionID); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if ref.Valid() || ref.ID() != issuedID || ref.Sequence() != issuedSequence || ref.Lineage() != issuedLineage {
		t.Fatalf("message identity changed after Stop: valid=%t id=%q ref=%#v", ref.Valid(), ref.ID(), ref)
	}
}

func TestHandleArenaSeversCapabilityBeforeRefund(t *testing.T) {
	manager := newTestManager(t, func(cfg *Config) {
		cfg.MaxActiveTraces = 1
		cfg.MaxActiveRecords = 1
	})
	startTestSession(t, manager, 1, 1<<20, "selected")
	_, recorder := beginTestHTTP(manager, "sever-order", "selected", nil)
	record := testRecordState(t, recorder)
	session := manager.active.Load()
	slotID := recorder.recordSlot

	manager.mu.Lock()
	released := make(chan struct{})
	go func() {
		session.mu.Lock()
		session.detachAndReleaseRecordLocked(record, false)
		session.mu.Unlock()
		close(released)
	}()
	deadline := time.Now().Add(2 * time.Second)
	for record.boundSession.Load() != nil {
		if time.Now().After(deadline) {
			manager.mu.Unlock()
			t.Fatal("record capability was not severed before account refund")
		}
	}
	if record.handleSlot != 0 || session.recordHandleSlots[slotID-1].record != nil {
		manager.mu.Unlock()
		t.Fatal("record slot still exposed the graph at the refund barrier")
	}
	manager.mu.Unlock()
	select {
	case <-released:
	case <-time.After(2 * time.Second):
		t.Fatal("record release did not finish after account barrier opened")
	}
}
