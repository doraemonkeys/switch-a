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
	"sync/atomic"
	"testing"
	"time"
)

func readRecordPageForTest(
	t testing.TB,
	manager *Manager,
	sessionID string,
	query ListQuery,
) (RecordPage, error) {
	t.Helper()
	lease, err := manager.OpenRecordPage(context.Background(), sessionID, query)
	if err != nil {
		return RecordPage{}, err
	}
	defer lease.Close()
	var encoded bytes.Buffer
	if err := lease.WriteJSON(context.Background(), &encoded); err != nil {
		return RecordPage{}, err
	}
	var page RecordPage
	if err := json.Unmarshal(encoded.Bytes(), &page); err != nil {
		return RecordPage{}, fmt.Errorf("decode record page: %w", err)
	}
	return page, nil
}

func readRecordDetailForTest(
	t testing.TB,
	manager *Manager,
	sessionID, recordID string,
	previewBytes int,
) (RecordDetail, error) {
	t.Helper()
	lease, err := manager.OpenRecordDetail(context.Background(), sessionID, recordID, previewBytes)
	if err != nil {
		return RecordDetail{}, err
	}
	defer lease.Close()
	var encoded bytes.Buffer
	if err := lease.WriteJSON(context.Background(), &encoded); err != nil {
		return RecordDetail{}, err
	}
	var detail RecordDetail
	if err := json.Unmarshal(encoded.Bytes(), &detail); err != nil {
		return RecordDetail{}, fmt.Errorf("decode record detail: %w", err)
	}
	return detail, nil
}

type queryWriteFailure struct{}

func (queryWriteFailure) Error() string { return "query write failure" }

type failingQueryWriter struct{}

func (failingQueryWriter) Write([]byte) (int, error) {
	return 0, queryWriteFailure{}
}

type panickingQueryWriter struct{}

func (panickingQueryWriter) Write([]byte) (int, error) {
	panic("query writer panic")
}

type singleReadQueryContext struct {
	calls   atomic.Int32
	reenter func()
}

func (*singleReadQueryContext) Deadline() (time.Time, bool) { return time.Time{}, false }

func (ctx *singleReadQueryContext) Done() <-chan struct{} {
	if ctx.calls.Add(1) != 1 {
		panic("query read Context.Done more than once")
	}
	if ctx.reenter != nil {
		ctx.reenter()
	}
	return nil
}

func (*singleReadQueryContext) Err() error {
	panic("query called hostile Context.Err")
}

func (*singleReadQueryContext) Value(any) any { return nil }

func writePageWithPanickingWriter(lease *RecordPageLease) (recovered any) {
	defer func() {
		recovered = recover()
	}()
	_ = lease.WriteJSON(context.Background(), panickingQueryWriter{})
	return nil
}

type countingQueryWriter struct {
	writes atomic.Int32
}

func (writer *countingQueryWriter) Write(payload []byte) (int, error) {
	writer.writes.Add(1)
	return len(payload), nil
}

type blockingQueryWriter struct {
	entered   chan struct{}
	release   chan struct{}
	writes    atomic.Int32
	completed atomic.Int32
}

func newBlockingQueryWriter() *blockingQueryWriter {
	return &blockingQueryWriter{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (writer *blockingQueryWriter) Write(payload []byte) (int, error) {
	if writer.writes.Add(1) == 1 {
		close(writer.entered)
		<-writer.release
	}
	writer.completed.Add(1)
	return len(payload), nil
}

func TestQueryLeaseCloseAndWriteFailureReleaseExactAccounting(t *testing.T) {
	manager := newTestManager(t, nil)
	session := startTestSession(t, manager, 2, 1<<20, "selected")
	recordID := addCompletedRecord(t, manager, 1)
	baseline := manager.Status().ProcessMemory

	pageLease, err := manager.OpenRecordPage(context.Background(), session.SessionID, ListQuery{Limit: 1})
	if err != nil {
		t.Fatalf("OpenRecordPage() error = %v", err)
	}
	during := manager.Status().ProcessMemory
	if during.TemporaryBytes <= baseline.TemporaryBytes ||
		during.ChargedBytes-baseline.ChargedBytes != during.TemporaryBytes-baseline.TemporaryBytes {
		t.Fatalf("page lease accounting: before=%#v during=%#v", baseline, during)
	}
	pageLease.Close()
	pageLease.Close()
	if after := manager.Status().ProcessMemory; after != baseline {
		t.Fatalf("page Close accounting: got=%#v want=%#v", after, baseline)
	}

	detailLease, err := manager.OpenRecordDetail(context.Background(), session.SessionID, recordID, 64)
	if err != nil {
		t.Fatalf("OpenRecordDetail() error = %v", err)
	}
	err = detailLease.WriteJSON(context.Background(), failingQueryWriter{})
	var writeFailure queryWriteFailure
	if !errors.As(err, &writeFailure) {
		t.Fatalf("WriteJSON() error = %v, want query writer failure", err)
	}
	if after := manager.Status().ProcessMemory; after != baseline {
		t.Fatalf("failed write accounting: got=%#v want=%#v", after, baseline)
	}

	canceledLease, err := manager.OpenRecordPage(context.Background(), session.SessionID, ListQuery{Limit: 1})
	if err != nil {
		t.Fatalf("second OpenRecordPage() error = %v", err)
	}
	canceledContext, cancel := context.WithCancel(context.Background())
	cancel()
	destination := &countingQueryWriter{}
	if err := canceledLease.WriteJSON(canceledContext, destination); !errors.Is(err, ErrQueryCanceled) {
		t.Fatalf("canceled WriteJSON() error = %v", err)
	}
	if destination.writes.Load() != 0 {
		t.Fatalf("canceled query started %d writes", destination.writes.Load())
	}
	if after := manager.Status().ProcessMemory; after != baseline {
		t.Fatalf("canceled write accounting: got=%#v want=%#v", after, baseline)
	}
}

func TestQueryReadsHostileContextOutsideInternalLocksExactlyOnce(t *testing.T) {
	manager := newTestManager(t, nil)
	session := startTestSession(t, manager, 2, 1<<20, "selected")
	addCompletedRecord(t, manager, 1)

	openContext := &singleReadQueryContext{reenter: func() { _ = manager.Status() }}
	lease, err := manager.OpenRecordPage(openContext, session.SessionID, ListQuery{Limit: 1})
	if err != nil {
		t.Fatalf("OpenRecordPage() error = %v", err)
	}
	if calls := openContext.calls.Load(); calls != 1 {
		t.Fatalf("OpenRecordPage() Context.Done calls = %d, want 1", calls)
	}

	writeContext := &singleReadQueryContext{reenter: func() { _ = manager.Status() }}
	if err := lease.WriteJSON(writeContext, io.Discard); err != nil {
		t.Fatalf("WriteJSON() error = %v", err)
	}
	if calls := writeContext.calls.Load(); calls != 1 {
		t.Fatalf("WriteJSON() Context.Done calls = %d, want 1", calls)
	}
}

func TestQueryWriterPanicSeversLeaseAndReleasesAccounting(t *testing.T) {
	manager := newTestManager(t, nil)
	session := startTestSession(t, manager, 2, 1<<20, "selected")
	addCompletedRecord(t, manager, 1)
	baseline := manager.Status().ProcessMemory

	lease, err := manager.OpenRecordPage(context.Background(), session.SessionID, ListQuery{Limit: 1})
	if err != nil {
		t.Fatalf("OpenRecordPage() error = %v", err)
	}
	if recovered := writePageWithPanickingWriter(lease); recovered == nil {
		t.Fatal("WriteJSON() did not propagate writer panic")
	}
	if after := manager.Status().ProcessMemory; after != baseline {
		t.Fatalf("writer panic accounting: got=%#v want=%#v", after, baseline)
	}
	if err := lease.WriteJSON(context.Background(), io.Discard); !errors.Is(err, ErrQueryCanceled) {
		t.Fatalf("consumed lease WriteJSON() error = %v", err)
	}
	lease.Close()
}

func TestQuerySequenceExhaustionDoesNotPublishOrChargeLease(t *testing.T) {
	manager := newTestManager(t, nil)
	session := startTestSession(t, manager, 2, 1<<20, "selected")
	state := manager.active.Load()
	state.queryMu.Lock()
	state.nextQuerySequence = math.MaxUint64
	state.queryMu.Unlock()
	baseline := manager.Status().ProcessMemory

	lease, err := manager.OpenRecordPage(context.Background(), session.SessionID, ListQuery{Limit: 1})
	if lease != nil || !errors.Is(err, ErrCapacityExceeded) {
		t.Fatalf("OpenRecordPage() = (%#v, %v), want capacity rejection", lease, err)
	}
	state.queryMu.Lock()
	count := state.queryLeaseCount
	first, last := state.queryLeaseFirst, state.queryLeaseLast
	state.queryMu.Unlock()
	if count != 0 || first != nil || last != nil {
		t.Fatalf("sequence exhaustion published query registry state: count=%d first=%p last=%p", count, first, last)
	}
	if after := manager.Status().ProcessMemory; after != baseline {
		t.Fatalf("sequence exhaustion accounting: got=%#v want=%#v", after, baseline)
	}
}

func TestQueryStopCancelsWithoutWaitingForExternalWriterAndRejectsABA(t *testing.T) {
	manager := newTestManager(t, nil)
	first := startTestSession(t, manager, 2, 1<<20, "selected")
	oldSession := manager.active.Load()

	blockedLease, err := manager.OpenRecordPage(context.Background(), first.SessionID, ListQuery{Limit: 1})
	if err != nil {
		t.Fatalf("OpenRecordPage(blocked) error = %v", err)
	}
	idleLease, err := manager.OpenRecordPage(context.Background(), first.SessionID, ListQuery{Limit: 1})
	if err != nil {
		t.Fatalf("OpenRecordPage(idle) error = %v", err)
	}

	destination := newBlockingQueryWriter()
	writeResult := make(chan error, 1)
	go func() {
		writeResult <- blockedLease.WriteJSON(context.Background(), destination)
	}()
	select {
	case <-destination.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("query did not enter destination Write")
	}

	stopResult := make(chan error, 1)
	go func() {
		stopResult <- manager.Stop(first.SessionID)
	}()
	select {
	case err := <-stopResult:
		if err != nil {
			t.Fatalf("Stop() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Stop waited for an external query writer")
	}
	select {
	case <-blockedLease.Done():
	default:
		t.Fatal("Stop did not cancel blocked lease")
	}
	stoppedMemory := manager.Status().ProcessMemory
	if stoppedMemory.TemporaryBytes == 0 || stoppedMemory.ReleasingBytes == 0 {
		t.Fatalf("in-flight query ownership disappeared early: %#v", stoppedMemory)
	}

	second := startTestSession(t, manager, 2, 1<<20, "selected")
	idleDestination := &countingQueryWriter{}
	if err := idleLease.WriteJSON(context.Background(), idleDestination); !errors.Is(err, ErrQueryCanceled) {
		t.Fatalf("old idle lease WriteJSON() error = %v", err)
	}
	if idleDestination.writes.Load() != 0 {
		t.Fatalf("old idle lease wrote into generation %d", second.Generation)
	}

	close(destination.release)
	select {
	case err := <-writeResult:
		if !errors.Is(err, ErrQueryCanceled) {
			t.Fatalf("blocked WriteJSON() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("blocked query did not exit after destination release")
	}
	if destination.writes.Load() != 1 || destination.completed.Load() != 1 {
		t.Fatalf("post-cancel writes: started=%d completed=%d",
			destination.writes.Load(), destination.completed.Load())
	}
	if err := blockedLease.WriteJSON(context.Background(), io.Discard); !errors.Is(err, ErrQueryCanceled) {
		t.Fatalf("second blocked lease WriteJSON() error = %v", err)
	}

	oldSession.queryMu.Lock()
	manager.mu.Lock()
	if oldSession.queryLeaseCount != 0 || oldSession.queryLeaseFirst != nil ||
		oldSession.queryLeaseLast != nil || oldSession.chargedBytes != 0 {
		manager.mu.Unlock()
		oldSession.queryMu.Unlock()
		t.Fatalf("old query registry/accounting retained state: count=%d charged=%d",
			oldSession.queryLeaseCount, oldSession.chargedBytes)
	}
	manager.mu.Unlock()
	oldSession.queryMu.Unlock()

	final := manager.Status()
	if !final.HasSession || !final.Session.SessionID.Equal(second.SessionID) ||
		final.ProcessMemory.TemporaryBytes != 0 || final.ProcessMemory.ReleasingBytes != 0 ||
		final.ProcessMemory.ChargedBytes != final.Session.RetainedBytes {
		t.Fatalf("post-ABA accounting/status = %#v", final)
	}
}

func TestQueryAdmissionIsChargedAndCancelableWhileSessionMutexIsContended(t *testing.T) {
	manager := newTestManager(t, nil)
	session := startTestSession(t, manager, 2, 1<<20, "selected")
	state := manager.active.Load()

	manager.mu.Lock()
	baselineSession := state.chargedBytes
	baselineProcess := manager.processCharged
	baselineTemporary := manager.processTemporary
	manager.mu.Unlock()

	state.mu.Lock()
	locked := true
	defer func() {
		if locked {
			state.mu.Unlock()
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		lease, err := manager.OpenRecordPage(ctx, session.SessionID, ListQuery{Limit: 1})
		if lease != nil {
			lease.Close()
		}
		result <- err
	}()

	deadline := time.Now().Add(2 * time.Second)
	for {
		state.queryMu.Lock()
		admitted := state.queryLeaseCount == 1
		state.queryMu.Unlock()
		if admitted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("query never established its charged admission")
		}
		time.Sleep(time.Millisecond)
	}

	manager.mu.Lock()
	if state.chargedBytes-baselineSession != queryLeaseBaseChargeBytes+queryWriteChunkBytes ||
		manager.processCharged-baselineProcess != queryLeaseBaseChargeBytes+queryWriteChunkBytes ||
		manager.processTemporary-baselineTemporary != queryLeaseBaseChargeBytes+queryWriteChunkBytes {
		manager.mu.Unlock()
		t.Fatal("contending query was not fully charged before waiting")
	}
	manager.mu.Unlock()

	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, ErrQueryCanceled) {
			t.Fatalf("OpenRecordPage() error = %v, want canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("context cancellation waited for the session mutex")
	}

	state.queryMu.Lock()
	manager.mu.Lock()
	if state.queryLeaseCount != 0 || state.chargedBytes != baselineSession ||
		manager.processCharged != baselineProcess || manager.processTemporary != baselineTemporary {
		manager.mu.Unlock()
		state.queryMu.Unlock()
		t.Fatal("canceled lock waiter leaked registry or accounting ownership")
	}
	manager.mu.Unlock()
	state.queryMu.Unlock()
	state.mu.Unlock()
	locked = false
}

func TestQueryWriterRejectsNextChunkAtLogicalDetachBeforeCancellation(t *testing.T) {
	manager := newTestManager(t, nil)
	session := startTestSession(t, manager, 2, 1<<20, "selected")
	gateway, recorder := beginTestHTTP(manager, "stream-detach", "selected", nil)
	recorder.ObserveResponse(HTTPResponseHead{StatusCode: http.StatusOK})
	recorder.ObserveUpstream(bytes.Repeat([]byte("x"), 64<<10))
	recorder.Finish(Outcome{SourceCompletion: SourceCompletionComplete})
	gateway.Finish(GatewayOutcome{})

	lease, err := manager.OpenRecordDetail(
		context.Background(),
		session.SessionID,
		recorder.ID(),
		64<<10,
	)
	if err != nil {
		t.Fatalf("OpenRecordDetail() error = %v", err)
	}
	state := manager.active.Load()
	destination := newBlockingQueryWriter()
	writeResult := make(chan error, 1)
	go func() {
		writeResult <- lease.WriteJSON(context.Background(), destination)
	}()
	select {
	case <-destination.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("query did not admit its first destination write")
	}

	// Holding the registry lock delays explicit query cancellation after Stop's
	// CAS. The stream must still observe logical detachment through active.
	state.queryMu.Lock()
	queryLocked := true
	defer func() {
		if queryLocked {
			state.queryMu.Unlock()
		}
	}()
	stopResult := make(chan error, 1)
	go func() {
		stopResult <- manager.Stop(session.SessionID)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for manager.Enabled() {
		if time.Now().After(deadline) {
			t.Fatal("Stop did not logically detach the session")
		}
		time.Sleep(time.Millisecond)
	}
	close(destination.release)
	deadline = time.Now().Add(2 * time.Second)
	for destination.completed.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("first destination write did not return")
		}
		time.Sleep(time.Millisecond)
	}
	time.Sleep(10 * time.Millisecond)
	if writes := destination.writes.Load(); writes != 1 {
		t.Fatalf("detached stream admitted %d writes, want exactly the pre-detach write", writes)
	}

	state.queryMu.Unlock()
	queryLocked = false
	select {
	case err := <-stopResult:
		if err != nil {
			t.Fatalf("Stop() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not finish after query cancellation was unblocked")
	}
	select {
	case err := <-writeResult:
		if !errors.Is(err, ErrQueryCanceled) {
			t.Fatalf("WriteJSON() error = %v, want canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("detached query did not release after cancellation")
	}
}

func TestQueryPageMaximumLimitSupportsUniqueTraces(t *testing.T) {
	manager := newTestManager(t, func(cfg *Config) {
		cfg.ProcessCeilingBytes = 64 << 20
		cfg.DefaultSessionQuotaBytes = 32 << 20
		cfg.MaxRecordsPerProvider = DefaultMaxListLimit
	})
	session := startTestSession(t, manager, DefaultMaxListLimit, 32<<20, "selected")
	for index := 0; index < DefaultMaxListLimit; index++ {
		addCompletedRecord(t, manager, index)
	}
	page, err := readRecordPageForTest(t, manager, session.SessionID, ListQuery{Limit: DefaultMaxListLimit})
	if err != nil {
		t.Fatalf("maximum page error = %v", err)
	}
	if len(page.Records) != DefaultMaxListLimit || len(page.GatewayTraces) != DefaultMaxListLimit {
		t.Fatalf("maximum page counts: records=%d traces=%d", len(page.Records), len(page.GatewayTraces))
	}
}

func TestFaultedRecorderStillFinalizesExactlyOnce(t *testing.T) {
	tests := []struct {
		name     string
		finalize func(GatewayRecorder, Recorder)
	}{
		{
			name: "explicit",
			finalize: func(gateway GatewayRecorder, recorder Recorder) {
				recorder.Finish(Outcome{
					SourceCompletion:  SourceCompletionComplete,
					TerminationReason: TerminationReasonEOF,
				})
				gateway.Finish(GatewayOutcome{})
			},
		},
		{
			name: "gateway safety finalizer",
			finalize: func(gateway GatewayRecorder, _ Recorder) {
				gateway.Finish(GatewayOutcome{})
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager := newTestManager(t, nil)
			session := startTestSession(t, manager, 2, 1<<20, "selected")
			gateway, recorder := beginTestHTTP(manager, "fault", "selected", nil)
			recorder.ObserveWebSocketHandshake(WebSocketHandshake{StatusCode: http.StatusSwitchingProtocols})

			record := testRecordState(t, recorder)
			if record == nil {
				t.Fatal("value recorder lookup failed")
			}
			state := manager.active.Load()
			state.mu.Lock()
			if !record.disabled || state.activeRecords != 1 {
				state.mu.Unlock()
				t.Fatalf("fault state: disabled=%v active=%d", record.disabled, state.activeRecords)
			}
			state.mu.Unlock()

			test.finalize(gateway, recorder)
			state.mu.Lock()
			if !record.completed || state.activeRecords != 0 {
				state.mu.Unlock()
				t.Fatalf("terminal state: completed=%v active=%d", record.completed, state.activeRecords)
			}
			state.mu.Unlock()

			detail, err := readRecordDetailForTest(t, manager, session.SessionID, recorder.ID(), 64)
			if err != nil {
				t.Fatalf("record detail error = %v", err)
			}
			if detail.Summary.SourceCompletion != SourceCompletionPartial ||
				detail.Summary.TerminationReason != TerminationReasonCaptureFault ||
				detail.Summary.CaptureCompletion != CaptureCompletionOverflowed {
				t.Fatalf("fault completion = %#v", detail.Summary)
			}
		})
	}
}

func TestTransitionOnlySelectedGatewayReleasesTrace(t *testing.T) {
	manager := newTestManager(t, nil)
	startTestSession(t, manager, 2, 1<<20, "selected")
	state := manager.active.Load()
	state.mu.Lock()
	baseline := state.chargedBytes
	state.mu.Unlock()

	gateway := manager.BeginGateway(GatewayStart{GatewayRequestID: "transition-only"})
	gateway.Transition(TransitionStart{
		Attempt:           AttemptMetadata{Provider: ProviderIdentity{ID: "selected"}},
		TerminationReason: TerminationReasonPreparationError,
	})
	gateway.Finish(GatewayOutcome{TerminationReason: TerminationReasonPreparationError})

	state.mu.Lock()
	defer state.mu.Unlock()
	if state.traceCount != 0 || state.activeTraces != 0 || state.retainedRecordCount != 0 {
		t.Fatalf("transition-only trace retained: traces=%d active=%d records=%d",
			state.traceCount, state.activeTraces, state.retainedRecordCount)
	}
	if state.chargedBytes != baseline {
		t.Fatalf("transition-only charge = %d, want baseline %d", state.chargedBytes, baseline)
	}
	if err := state.debugInvariantLocked(); err != nil {
		t.Fatalf("invariant = %v", err)
	}
}
