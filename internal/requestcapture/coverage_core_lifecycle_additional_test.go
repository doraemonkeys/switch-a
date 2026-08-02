package requestcapture

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
)

type shortStatusWriter struct{}

func (shortStatusWriter) Write(payload []byte) (int, error) {
	return len(payload) - 1, nil
}

func additionalQuerySession() (*Manager, *sessionState) {
	manager := &Manager{cfg: normalizedConfig{
		processCeilingBytes: 1 << 20,
		logger:              zap.NewNop(),
	}}
	session := &sessionState{
		manager:        manager,
		id:             "synthetic-session",
		generation:     1,
		accepting:      true,
		ownerCount:     1,
		ownerAccepting: true,
		queryDone:      make(chan struct{}),
		quotaBytes:     1 << 20,
	}
	manager.active.Store(session)
	return manager, session
}

func TestAdditionalQueryPublicRejectionsAndNilCapabilities(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	var manager *Manager
	if lease, err := manager.OpenRecordPage(canceled, "missing", ListQuery{}); lease != nil || !errors.Is(err, ErrQueryCanceled) {
		t.Fatalf("canceled nil-manager OpenRecordPage() = (%v, %v)", lease, err)
	}
	if lease, err := manager.OpenRecordPage(context.Background(), "missing", ListQuery{}); lease != nil || !errors.Is(err, ErrNoActiveSession) {
		t.Fatalf("nil-manager OpenRecordPage() = (%v, %v)", lease, err)
	}
	if lease, err := manager.OpenRecordDetail(canceled, "missing", "record", 1); lease != nil || !errors.Is(err, ErrQueryCanceled) {
		t.Fatalf("canceled nil-manager OpenRecordDetail() = (%v, %v)", lease, err)
	}
	if lease, err := manager.OpenRecordDetail(context.Background(), "missing", "record", 1); lease != nil || !errors.Is(err, ErrNoActiveSession) {
		t.Fatalf("nil-manager OpenRecordDetail() = (%v, %v)", lease, err)
	}

	var page *RecordPageLease
	select {
	case <-page.Done():
	default:
		t.Fatal("nil page lease Done channel is not closed")
	}
	if err := page.WriteJSON(context.Background(), io.Discard); !errors.Is(err, ErrQueryCanceled) {
		t.Fatalf("nil page lease WriteJSON() error = %v", err)
	}
	page.Close()
	var detail *RecordDetailLease
	select {
	case <-detail.Done():
	default:
		t.Fatal("nil detail lease Done channel is not closed")
	}
	if err := detail.WriteJSON(context.Background(), io.Discard); !errors.Is(err, ErrQueryCanceled) {
		t.Fatalf("nil detail lease WriteJSON() error = %v", err)
	}
	detail.Close()

	active := newTestManager(t, nil)
	info := startTestSession(t, active, 2, 1<<20, "selected")
	if lease, err := active.OpenRecordPage(context.Background(), "wrong-session", ListQuery{Limit: 1}); lease != nil ||
		!errors.Is(err, ErrSessionMismatch) {
		t.Fatalf("mismatched OpenRecordPage() = (%v, %v)", lease, err)
	}
	recordID := addCompletedRecord(t, active, 1)
	pageLease, err := active.OpenRecordPage(context.Background(), info.SessionID, ListQuery{Limit: 1})
	if err != nil {
		t.Fatalf("OpenRecordPage() error = %v", err)
	}
	if err = pageLease.WriteJSON(context.Background(), nil); !errors.Is(err, ErrQueryCanceled) {
		t.Fatalf("page WriteJSON(nil) error = %v", err)
	}
	if err = pageLease.WriteJSON(context.Background(), io.Discard); !errors.Is(err, ErrQueryCanceled) {
		t.Fatalf("consumed page WriteJSON() error = %v", err)
	}
	pageLease.Close()

	detailLease, err := active.OpenRecordDetail(context.Background(), info.SessionID, recordID, 1)
	if err != nil {
		t.Fatalf("OpenRecordDetail() error = %v", err)
	}
	if err = detailLease.WriteJSON(context.Background(), nil); !errors.Is(err, ErrQueryCanceled) {
		t.Fatalf("detail WriteJSON(nil) error = %v", err)
	}
	if err = detailLease.WriteJSON(context.Background(), io.Discard); !errors.Is(err, ErrQueryCanceled) {
		t.Fatalf("consumed detail WriteJSON() error = %v", err)
	}
	detailLease.Close()
}

func TestAdditionalQueryAdmissionRollbackAndRegistryEdges(t *testing.T) {
	t.Run("pre-admission rejection", func(t *testing.T) {
		manager, session := additionalQuerySession()
		closed := make(chan struct{})
		close(closed)
		if lease, err := session.beginQuery(closed); lease != nil || !errors.Is(err, ErrQueryCanceled) {
			t.Fatalf("beginQuery(closed) = (%v, %v)", lease, err)
		}

		foreign := &sessionState{manager: manager, queryDone: make(chan struct{})}
		if lease, err := foreign.beginQuery(nil); lease != nil || !errors.Is(err, ErrNoActiveSession) {
			t.Fatalf("foreign beginQuery() = (%v, %v)", lease, err)
		}

		close(session.queryDone)
		if lease, err := session.beginQuery(nil); lease != nil || !errors.Is(err, ErrNoActiveSession) {
			t.Fatalf("canceled-session beginQuery() = (%v, %v)", lease, err)
		}
	})

	t.Run("owner and capacity rollback", func(t *testing.T) {
		_, session := additionalQuerySession()
		session.ownerAccepting = false
		if lease, err := session.beginQuery(nil); lease != nil || !errors.Is(err, ErrNoActiveSession) {
			t.Fatalf("owner-rejected beginQuery() = (%v, %v)", lease, err)
		}
		session.ownerAccepting = true
		session.quotaBytes = queryLeaseBaseChargeBytes + queryWriteChunkBytes - 1
		if lease, err := session.beginQuery(nil); lease != nil || !errors.Is(err, ErrCapacityExceeded) {
			t.Fatalf("capacity-rejected beginQuery() = (%v, %v)", lease, err)
		}
		if session.ownerCount != 1 || session.queryLeaseCount != 0 || session.chargedBytes != 0 {
			t.Fatalf("failed admission leaked ownership: owners=%d leases=%d charged=%d",
				session.ownerCount, session.queryLeaseCount, session.chargedBytes)
		}
	})

	t.Run("registry and growth", func(t *testing.T) {
		manager, session := additionalQuerySession()
		first, err := session.beginQuery(nil)
		if err != nil {
			t.Fatalf("first beginQuery() error = %v", err)
		}
		second, err := session.beginQuery(nil)
		if err != nil {
			t.Fatalf("second beginQuery() error = %v", err)
		}
		if session.queryLeaseFirst != first || session.queryLeaseLast != second || first.after != second || second.before != first {
			t.Fatal("query registry did not link both leases")
		}

		session.mu.Lock()
		if session.growQueryLeaseLocked(nil, first.charge+1) {
			t.Fatal("nil query lease grew")
		}
		if !session.growQueryLeaseLocked(first, first.charge) {
			t.Fatal("idempotent query growth failed")
		}
		before := first.charge
		if !session.growQueryLeaseLocked(first, before+64) || first.charge != before+64 {
			t.Fatal("query growth failed")
		}
		session.releasing = true
		if session.growQueryLeaseLocked(first, first.charge+1) {
			t.Fatal("releasing query lease grew")
		}
		session.releasing = false
		session.mu.Unlock()

		first.close()
		first.close()
		if session.queryLeaseFirst != second || second.before != nil {
			t.Fatal("head query removal left stale links")
		}
		second.close()
		if session.queryLeaseCount != 0 || session.queryLeaseFirst != nil || session.queryLeaseLast != nil ||
			session.chargedBytes != 0 || manager.processTemporary != 0 || session.ownerCount != 1 {
			t.Fatal("query close did not restore registry/accounting")
		}
	})

	t.Run("cancellation and rejected release", func(t *testing.T) {
		manager, session := additionalQuerySession()
		lease, err := session.beginQuery(nil)
		if err != nil {
			t.Fatalf("beginQuery() error = %v", err)
		}
		session.cancelQueries()
		session.cancelQueries()
		if err = lease.queryError(nil); !errors.Is(err, ErrQueryCanceled) {
			t.Fatalf("canceled lease queryError() = %v", err)
		}
		lease.close()
		if session.queryLeaseCount != 0 || manager.processTemporary != 0 {
			t.Fatal("canceled query lease did not release")
		}

		bad := &queryLease{session: session, registered: true, charge: 1, done: make(chan struct{})}
		session.releaseQueryLease(bad)
		if !bad.registered {
			t.Fatal("underflowing release mutated the capability")
		}
		session.releaseQueryLease(nil)
		session.releaseQueryLease(&queryLease{session: session})
	})

	var nilSession *sessionState
	nilSession.cancelQueries()
	var nilLease *queryLease
	if err := nilLease.queryError(nil); !errors.Is(err, ErrQueryCanceled) {
		t.Fatalf("nil query lease error = %v", err)
	}
	nilLease.cancel()
	nilLease.close()
}

func TestAdditionalQueryLockCancellationAndCursorValidation(t *testing.T) {
	_, session := additionalQuerySession()
	lease, err := session.beginQuery(nil)
	if err != nil {
		t.Fatalf("beginQuery() error = %v", err)
	}
	lease.cancel()
	if err = session.lockQueryState(nil, lease); !errors.Is(err, ErrQueryCanceled) {
		t.Fatalf("lockQueryState(canceled lease) error = %v", err)
	}
	lease.close()

	_, session = additionalQuerySession()
	lease, err = session.beginQuery(nil)
	if err != nil {
		t.Fatalf("second beginQuery() error = %v", err)
	}
	session.accepting = false
	if err = session.lockQueryState(nil, lease); !errors.Is(err, ErrQueryCanceled) {
		t.Fatalf("lockQueryState(nonaccepting) error = %v", err)
	}
	lease.close()

	for _, value := range []string{
		"!",
		base64.RawURLEncoding.EncodeToString([]byte("not-json")),
		base64.RawURLEncoding.EncodeToString([]byte(`{"g":0,"w":1,"b":1}`)),
		base64.RawURLEncoding.EncodeToString([]byte(`{"g":1,"w":1,"b":0}`)),
	} {
		if _, err := decodeCursor(value); !errors.Is(err, ErrInvalidCursor) {
			t.Fatalf("decodeCursor(%q) error = %v", value, err)
		}
	}
	validCursor := encodeCursor(1, 2, 3)
	if payload, err := decodeCursor(validCursor); err != nil || payload != (cursorPayload{Generation: 1, Watermark: 2, Before: 3}) {
		t.Fatalf("decodeCursor(valid) = (%+v, %v)", payload, err)
	}
	for _, value := range []string{
		"!",
		base64.RawURLEncoding.EncodeToString([]byte("not-json")),
		base64.RawURLEncoding.EncodeToString([]byte(`{"g":0,"w":1}`)),
	} {
		if _, _, err := decodeWatermark(value); !errors.Is(err, ErrInvalidCursor) {
			t.Fatalf("decodeWatermark(%q) error = %v", value, err)
		}
	}
	validWatermark := encodeWatermark(4, 5)
	if generation, watermark, err := decodeWatermark(validWatermark); err != nil || generation != 4 || watermark != 5 {
		t.Fatalf("decodeWatermark(valid) = (%d, %d, %v)", generation, watermark, err)
	}
	if contextDone(nil) != nil || queryContextCanceled(nil) {
		t.Fatal("nil context channel was treated as canceled")
	}
	closed := make(chan struct{})
	close(closed)
	if !queryContextCanceled(closed) {
		t.Fatal("closed context channel was not canceled")
	}
	select {
	case <-closedQueryDone():
	default:
		t.Fatal("closedQueryDone() is open")
	}
	select {
	case <-closedQueryDone():
	default:
		t.Fatal("reused closedQueryDone() is open")
	}
}

func TestAdditionalStatusCapabilityAndEpochFailurePaths(t *testing.T) {
	for _, value := range []string{"", strings.Repeat("x", maxSessionIDBytes+1)} {
		id := makeStatusSessionID(value)
		if id.Valid() || id.String() != "" {
			t.Fatalf("invalid status session ID %q was retained", value)
		}
	}
	id := makeStatusSessionID("session")
	if !id.Valid() || !id.Equal("session") || id.Equal("sessioN") || id.Equal("short") || id.String() != "session" {
		t.Fatalf("status session ID comparison failed: valid=%t string=%q", id.Valid(), id.String())
	}

	var nilManager *Manager
	var mutation statusEpochMutation
	if nilManager.beginStatusEpochMutation(&mutation) || (&Manager{}).beginStatusEpochMutation(nil) {
		t.Fatal("invalid status mutation was admitted")
	}
	if (*statusEpochMutation)(nil).finish() {
		t.Fatal("nil status mutation finished")
	}
	if !(&statusEpochMutation{}).finish() {
		t.Fatal("already-finished status mutation was not idempotent")
	}
	if (&statusEpochMutation{active: true}).finish() {
		t.Fatal("orphan status mutation finished")
	}

	epochManager := &Manager{}
	epochManager.statusEpochWriters = math.MaxUint64
	if epochManager.beginStatusEpochMutation(&mutation) {
		t.Fatal("saturated status writer registry admitted a writer")
	}
	epochManager.statusEpochWriters = 0
	epochManager.statusEpoch.Store(math.MaxUint64 - 1)
	if epochManager.beginStatusEpochMutation(&mutation) {
		t.Fatal("saturated status epoch admitted a writer")
	}
	epochManager.statusEpoch.Store(0)
	var first, second statusEpochMutation
	if !epochManager.beginStatusEpochMutation(&first) || !epochManager.beginStatusEpochMutation(&second) {
		t.Fatal("overlapping status mutations were rejected")
	}
	if epochManager.beginStatusEpochMutation(&first) {
		t.Fatal("active status mutation was reused")
	}
	if !first.finish() || epochManager.statusEpoch.Load() != 1 || !second.finish() || epochManager.statusEpoch.Load() != 2 {
		t.Fatal("overlapping status mutation epoch did not close exactly once")
	}
	broken := statusEpochMutation{manager: epochManager, active: true}
	if broken.finish() {
		t.Fatal("status mutation with a missing writer count finished")
	}
}

func TestAdditionalStatusLeaseShortWriteCloseRaceAndContext(t *testing.T) {
	var manager *Manager
	if lease, err := manager.OpenStatus(context.Background()); lease.Valid() || !errors.Is(err, ErrManagerClosed) {
		t.Fatalf("nil OpenStatus() = (%+v, %v)", lease, err)
	}
	if (StatusLease{}).Valid() {
		t.Fatal("zero status lease is valid")
	}
	if err := (StatusLease{}).WriteJSON(context.Background(), io.Discard); !errors.Is(err, ErrStatusLeaseClosed) {
		t.Fatalf("zero status WriteJSON() error = %v", err)
	}
	if err := (StatusLease{}).Close(); err != nil {
		t.Fatalf("zero status Close() error = %v", err)
	}

	active := newTestManager(t, nil)
	startTestSession(t, active, 1, 1<<20, "provider")
	lease, err := active.OpenStatus(context.Background())
	if err != nil {
		t.Fatalf("OpenStatus() error = %v", err)
	}
	if err = lease.WriteJSON(context.Background(), shortStatusWriter{}); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short status write error = %v", err)
	}
	if err = lease.Close(); err != nil {
		t.Fatalf("stale status Close() error = %v", err)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	lease, err = active.OpenStatus(context.Background())
	if err != nil {
		t.Fatalf("second OpenStatus() error = %v", err)
	}
	if err = lease.WriteJSON(canceled, io.Discard); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled status write error = %v", err)
	}
	if replacement, replaceErr := active.OpenStatus(context.Background()); replaceErr != nil {
		t.Fatalf("status claim remained after cancellation: %v", replaceErr)
	} else {
		_ = replacement.Close()
	}

	lease, err = active.OpenStatus(context.Background())
	if err != nil {
		t.Fatalf("third OpenStatus() error = %v", err)
	}
	writer := &blockingStatusWriter{entered: make(chan struct{}, 1), release: make(chan struct{})}
	result := make(chan error, 1)
	go func() { result <- lease.WriteJSON(context.Background(), writer) }()
	<-writer.entered
	if err = lease.Close(); err != nil {
		t.Fatalf("Close during status write error = %v", err)
	}
	close(writer.release)
	if err = <-result; err != nil {
		t.Fatalf("blocked status write error = %v", err)
	}
	if replacement, replaceErr := active.OpenStatus(context.Background()); replaceErr != nil {
		t.Fatalf("close-requested status claim remained: %v", replaceErr)
	} else {
		_ = replacement.Close()
	}
}

func TestAdditionalStatusSlotStateChangeOverflowAndRetirement(t *testing.T) {
	manager := newTestManager(t, nil)
	info := startTestSession(t, manager, 1, 1<<20, "provider")
	session := manager.active.Load()
	if _, _, err := manager.claimSessionStatusSlot(&sessionState{}); !errors.Is(err, errStatusChanged) {
		t.Fatalf("foreign claimSessionStatusSlot() error = %v", err)
	}
	retired := &statusJSONSlot{retired: true}
	session.mu.Lock()
	originalSlot := session.statusSlot
	session.statusSlot = retired
	session.mu.Unlock()
	if _, _, err := manager.claimSessionStatusSlot(session); !errors.Is(err, errStatusChanged) {
		t.Fatalf("retired claimSessionStatusSlot() error = %v", err)
	}
	session.mu.Lock()
	session.statusSlot = originalSlot
	session.mu.Unlock()

	if _, err := manager.claimStatusSlot(nil, nil); !errors.Is(err, ErrStatusBusy) {
		t.Fatalf("claimStatusSlot(nil) error = %v", err)
	}
	manager.statusLeaseNext = math.MaxUint64
	if _, err := manager.claimStatusSlot(nil, &manager.managerStatusSlot); !errors.Is(err, ErrStatusBusy) {
		t.Fatalf("saturated claimStatusSlot() error = %v", err)
	}
	manager.statusLeaseNext = 0

	tiny := &statusJSONSlot{storage: make([]byte, 1)}
	stopped := newTestManager(t, nil)
	if err := stopped.populateStoppedStatusSlot(tiny, 0, 0); !errors.Is(err, ErrInternalFailure) {
		t.Fatalf("tiny stopped status error = %v", err)
	}
	if err := manager.populateStoppedStatusSlot(tiny, 0, 0); !errors.Is(err, errStatusChanged) {
		t.Fatalf("active populateStoppedStatusSlot() error = %v", err)
	}

	session.mu.Lock()
	session.statusSlot = tiny
	session.mu.Unlock()
	if err := manager.populateActiveStatusSlot(tiny, session, 0, 0); !errors.Is(err, ErrInternalFailure) {
		t.Fatalf("tiny active status error = %v", err)
	}
	session.mu.Lock()
	session.statusSlot = originalSlot
	session.activeRecords = 2
	session.retainedRecordCount = 1
	session.mu.Unlock()
	if err := manager.populateActiveStatusSlot(originalSlot, session, 0, 0); err != nil {
		t.Fatalf("negative-completed active status error = %v", err)
	}
	session.mu.Lock()
	session.activeRecords = 0
	session.retainedRecordCount = 0
	session.mu.Unlock()

	if err := manager.Stop(info.SessionID); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	var nilSession *sessionState
	nilSession.retireStatusSlotLocked()
	(&sessionState{}).retireStatusSlotLocked()
	discardStatusSlot(nil)
	slot := &statusJSONSlot{storage: []byte("value"), session: session, charge: 9}
	discardStatusSlot(slot)
	if slot.storage != nil || slot.session != nil || slot.charge != 0 {
		t.Fatalf("discardStatusSlot() retained state: %#v", slot)
	}
	if statusContextError(nil) != nil {
		t.Fatal("nil status context failed")
	}
}

func TestAdditionalManagerNilAdmissionAndOwnerFailurePaths(t *testing.T) {
	var manager *Manager
	if manager.Enabled() || manager.BeginGateway(GatewayStart{}).Valid() {
		t.Fatal("nil manager exposed capture capability")
	}
	if _, err := manager.Start(StartRequest{}); !errors.Is(err, ErrManagerClosed) {
		t.Fatalf("nil Start() error = %v", err)
	}
	if err := manager.Stop("session"); !errors.Is(err, ErrNoActiveSession) {
		t.Fatalf("nil Stop() error = %v", err)
	}
	if status := manager.Status(); status.State != SessionStateStopped {
		t.Fatalf("nil Status() = %#v", status)
	}
	if err := manager.Close(); err != nil {
		t.Fatalf("nil Close() error = %v", err)
	}
	if _, err := manager.activeSession("session"); !errors.Is(err, ErrNoActiveSession) {
		t.Fatalf("nil activeSession() error = %v", err)
	}

	admission := &Manager{}
	admission.closed = true
	if _, err := admission.claimStartAdmission(); !errors.Is(err, ErrManagerClosed) {
		t.Fatalf("closed claimStartAdmission() error = %v", err)
	}
	admission.closed = false
	admission.starting = true
	if _, err := admission.claimStartAdmission(); !errors.Is(err, ErrSessionActive) {
		t.Fatalf("concurrent claimStartAdmission() error = %v", err)
	}
	admission.starting = false
	admission.generation = math.MaxUint64
	if _, err := admission.claimStartAdmission(); !errors.Is(err, ErrGenerationExhausted) {
		t.Fatalf("exhausted claimStartAdmission() error = %v", err)
	}

	active := newTestManager(t, nil)
	info := startTestSession(t, active, 1, 1<<20, "provider")
	if active.retainActive(info.Generation+1) != nil {
		t.Fatal("retainActive() crossed a generation boundary")
	}
	if _, err := active.activeSession("wrong"); !errors.Is(err, ErrSessionMismatch) {
		t.Fatalf("mismatched activeSession() error = %v", err)
	}

	var nilSession *sessionState
	if nilSession.retainOwner() || nilSession.releaseOwner() || nilSession.releaseActiveOwner() {
		t.Fatal("nil session changed owner state")
	}
	syntheticManager := &Manager{cfg: normalizedConfig{logger: zap.NewNop()}}
	session := &sessionState{manager: syntheticManager, ownerCount: 1, ownerAccepting: false}
	if session.retainOwner() {
		t.Fatal("nonaccepting session retained an owner")
	}
	session.ownerAccepting = true
	session.ownerCount = int(^uint(0) >> 1)
	if session.retainOwner() {
		t.Fatal("saturated owner count grew")
	}
	session.ownerCount = 2
	if !session.releaseOwner() || session.ownerCount != 1 {
		t.Fatal("multi-owner release did not decrement exactly once")
	}
	session.manager = nil
	if session.releaseOwner() {
		t.Fatal("owner release without manager succeeded")
	}
}

func TestAdditionalManagerValidationBoundaries(t *testing.T) {
	manager := newTestManager(t, nil)
	valid := StartRequest{
		Providers:                   []ProviderIdentity{{ID: "provider", Name: "Provider"}},
		CompletedRecordsPerProvider: 1,
		RetainedBytesLimit:          1 << 20,
		AcknowledgeRawPayloadRisk:   true,
	}
	tests := []struct {
		name   string
		mutate func(*StartRequest)
	}{
		{name: "too many providers", mutate: func(request *StartRequest) {
			request.Providers = make([]ProviderIdentity, maxRetainedProviders+1)
		}},
		{name: "oversized identity", mutate: func(request *StartRequest) {
			request.Providers[0].ID = strings.Repeat("x", maxRetainedProviderIDBytes+1)
		}},
		{name: "empty provider ID", mutate: func(request *StartRequest) {
			request.Providers[0].ID = ""
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := valid
			request.Providers = append([]ProviderIdentity(nil), valid.Providers...)
			test.mutate(&request)
			if _, err := manager.scanStart(request); err == nil {
				t.Fatal("scanStart() unexpectedly succeeded")
			}
		})
	}
}

func TestAdditionalStatusJSONBuilderAdapter(t *testing.T) {
	storage := make([]byte, 512)
	builder := newStatusJSONBuilder(storage)
	builder.byte('[')
	builder.quoted("value")
	builder.byte(',')
	builder.int64(-1)
	builder.byte(',')
	builder.uint64(2)
	builder.byte(',')
	builder.int(3)
	builder.byte(',')
	builder.timestamp(time.Unix(0, 4).UnixNano())
	builder.byte(']')
	if builder.overflowed() || builder.length() == 0 || !json.Valid(storage[:builder.length()]) {
		t.Fatalf("status builder adapter produced %q", storage[:builder.length()])
	}
}
