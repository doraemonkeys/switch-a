package requestcapture

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"runtime"
	"testing"
	"time"
)

type statusSessionWire struct {
	SessionID         string             `json:"session_id"`
	Providers         []ProviderIdentity `json:"providers"`
	ProviderIDs       []string           `json:"provider_ids"`
	DroppedTraceCount uint64             `json:"dropped_trace_count"`
}

type statusWire struct {
	State               string              `json:"state"`
	ProcessMemory       ProcessMemoryStatus `json:"process_memory"`
	PendingExportCount  int                 `json:"pending_export_count"`
	ActiveDownloadCount int                 `json:"active_download_count"`
	Session             *statusSessionWire  `json:"session"`
}

func readStatusLease(t *testing.T, lease StatusLease) statusWire {
	t.Helper()
	var encoded bytes.Buffer
	if err := lease.WriteJSON(context.Background(), &encoded); err != nil {
		t.Fatalf("StatusLease.WriteJSON() error = %v", err)
	}
	var status statusWire
	if err := json.Unmarshal(encoded.Bytes(), &status); err != nil {
		t.Fatalf("status JSON error = %v; payload = %q", err, encoded.String())
	}
	return status
}

func makeStatusQuotaExact(t *testing.T, manager *Manager) *sessionState {
	t.Helper()
	session := manager.active.Load()
	if session == nil {
		t.Fatal("active session is missing")
	}
	session.mu.Lock()
	manager.mu.Lock()
	session.quotaBytes = session.chargedBytes
	manager.cfg.processCeilingBytes = manager.processCharged
	manager.mu.Unlock()
	session.mu.Unlock()
	return session
}

func TestOpenStatusUsesPreReservedStorageAtExactQuotas(t *testing.T) {
	manager := newTestManager(t, nil)
	sessionInfo := startTestSession(t, manager, 2, 1<<20, `provider-"\\`, "provider-b")
	makeStatusQuotaExact(t, manager)

	allocations := testing.AllocsPerRun(100, func() {
		lease, err := manager.OpenStatus(context.Background())
		if err != nil {
			panic(err)
		}
		_ = lease.Close()
	})
	if allocations != 0 {
		t.Fatalf("OpenStatus allocations = %v, want 0", allocations)
	}
	statusAllocations := testing.AllocsPerRun(100, func() {
		_ = manager.Status()
	})
	if statusAllocations != 0 {
		t.Fatalf("Status allocations = %v, want 0", statusAllocations)
	}

	lease, err := manager.OpenStatus(context.Background())
	if err != nil {
		t.Fatalf("OpenStatus() error = %v", err)
	}
	status := readStatusLease(t, lease)
	if status.State != "active" || status.Session == nil {
		t.Fatalf("active status = %#v", status)
	}
	if status.Session.SessionID != sessionInfo.SessionID ||
		len(status.Session.Providers) != len(sessionInfo.Providers) ||
		len(status.Session.ProviderIDs) != len(sessionInfo.ProviderIDs) {
		t.Fatalf("provider catalog was not streamed exactly: %#v", status.Session)
	}
	if status.Session.Providers == nil || status.Session.ProviderIDs == nil {
		t.Fatalf("provider arrays must never be null: %#v", status.Session)
	}
	if status.ProcessMemory.ChargedBytes != status.ProcessMemory.CeilingBytes {
		t.Fatalf("exact-full process status = %#v", status.ProcessMemory)
	}
}

func TestOpenStatusSerializesStoppedStateAndEnforcesSingleCheckout(t *testing.T) {
	manager := newTestManager(t, nil)
	first, err := manager.OpenStatus(context.Background())
	if err != nil {
		t.Fatalf("first OpenStatus() error = %v", err)
	}
	if _, err = manager.OpenStatus(context.Background()); !errors.Is(err, ErrStatusBusy) {
		t.Fatalf("second OpenStatus() error = %v, want ErrStatusBusy", err)
	}
	if err = first.Close(); err != nil {
		t.Fatalf("StatusLease.Close() error = %v", err)
	}

	second, err := manager.OpenStatus(context.Background())
	if err != nil {
		t.Fatalf("reused OpenStatus() error = %v", err)
	}
	status := readStatusLease(t, second)
	if status.State != "stopped" || status.Session != nil {
		t.Fatalf("stopped status = %#v", status)
	}
}

type blockingStatusWriter struct {
	entered chan struct{}
	release chan struct{}
	payload []byte
}

type panickingStatusWriter struct{}

func (panickingStatusWriter) Write([]byte) (int, error) {
	panic("status writer panic")
}

func (writer *blockingStatusWriter) Write(payload []byte) (int, error) {
	writer.entered <- struct{}{}
	<-writer.release
	writer.payload = append(writer.payload, payload...)
	return len(payload), nil
}

func TestStatusLeaseDoesNotMakeStopWaitForDestination(t *testing.T) {
	manager := newTestManager(t, nil)
	sessionInfo := startTestSession(t, manager, 2, 1<<20, "provider-a")
	lease, err := manager.OpenStatus(context.Background())
	if err != nil {
		t.Fatalf("OpenStatus() error = %v", err)
	}
	writer := &blockingStatusWriter{entered: make(chan struct{}, 1), release: make(chan struct{})}
	writeResult := make(chan error, 1)
	go func() {
		writeResult <- lease.WriteJSON(context.Background(), writer)
	}()
	<-writer.entered

	stopResult := make(chan error, 1)
	go func() {
		stopResult <- manager.Stop(sessionInfo.SessionID)
	}()
	select {
	case err = <-stopResult:
		if err != nil {
			t.Fatalf("Stop() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Stop waited for the external status destination")
	}
	manager.mu.Lock()
	releasingWhileCheckedOut := manager.processReleasing
	manager.mu.Unlock()
	if releasingWhileCheckedOut == 0 {
		t.Fatal("checked-out status storage lost its releasing account owner")
	}

	close(writer.release)
	if err = <-writeResult; err != nil {
		t.Fatalf("StatusLease.WriteJSON() error = %v", err)
	}
	var status statusWire
	if err = json.Unmarshal(writer.payload, &status); err != nil || status.Session == nil {
		t.Fatalf("status payload became invalid during Stop: error=%v payload=%q", err, writer.payload)
	}
	manager.mu.Lock()
	charged := manager.processCharged
	releasing := manager.processReleasing
	manager.mu.Unlock()
	if charged != 0 || releasing != 0 {
		t.Fatalf("status lease account remained after write: charged=%d releasing=%d", charged, releasing)
	}
}

func TestStatusLeaseWriterPanicRetiresClaim(t *testing.T) {
	manager := newTestManager(t, nil)
	sessionInfo := startTestSession(t, manager, 1, 1<<20, "provider-a")
	lease, err := manager.OpenStatus(context.Background())
	if err != nil {
		t.Fatalf("OpenStatus() error = %v", err)
	}
	func() {
		defer func() {
			if recovered := recover(); recovered != "status writer panic" {
				t.Fatalf("WriteJSON() panic = %#v", recovered)
			}
		}()
		_ = lease.WriteJSON(context.Background(), panickingStatusWriter{})
	}()

	replacement, err := manager.OpenStatus(context.Background())
	if err != nil {
		t.Fatalf("OpenStatus() after panic error = %v", err)
	}
	if err = replacement.Close(); err != nil {
		t.Fatalf("replacement Close() error = %v", err)
	}
	if err = manager.Stop(sessionInfo.SessionID); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	manager.mu.Lock()
	charged := manager.processCharged
	manager.mu.Unlock()
	if charged != 0 {
		t.Fatalf("process charge after panic and Stop = %d, want 0", charged)
	}
}

func TestStatusWaitsForCompositeMutationEpoch(t *testing.T) {
	manager := newTestManager(t, nil)
	startTestSession(t, manager, 2, 1<<20, "provider-a")
	session := makeStatusQuotaExact(t, manager)
	var mutation statusEpochMutation
	if !manager.beginStatusEpochMutation(&mutation) {
		t.Fatal("beginStatusEpochMutation() failed")
	}

	leaseResult := make(chan StatusLease, 1)
	errorResult := make(chan error, 1)
	go func() {
		lease, err := manager.OpenStatus(context.Background())
		leaseResult <- lease
		errorResult <- err
	}()
	statusResult := make(chan Status, 1)
	go func() { statusResult <- manager.Status() }()

	deadline := time.Now().Add(2 * time.Second)
	for {
		manager.statusLeaseMu.Lock()
		claimed := manager.statusLeaseClaim.slot == session.statusSlot
		manager.statusLeaseMu.Unlock()
		if claimed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("OpenStatus did not claim its pre-reserved slot")
		}
		runtime.Gosched()
	}
	select {
	case <-leaseResult:
		t.Fatal("OpenStatus returned an intermediate composite epoch")
	default:
	}
	select {
	case <-statusResult:
		t.Fatal("Status returned an intermediate composite epoch")
	default:
	}

	manager.exportMu.Lock()
	manager.reservedExportSlots = 1
	manager.exportMu.Unlock()
	session.mu.Lock()
	session.droppedTraceCount = 1
	session.mu.Unlock()
	manager.mu.Lock()
	manager.processPinned = 1
	manager.mu.Unlock()
	if !mutation.finish() {
		t.Fatal("status epoch finish failed")
	}

	lease := <-leaseResult
	if err := <-errorResult; err != nil {
		t.Fatalf("OpenStatus() error = %v", err)
	}
	wire := readStatusLease(t, lease)
	value := <-statusResult
	if wire.PendingExportCount != 1 || wire.ProcessMemory.PinnedBytes != 1 ||
		wire.Session == nil || wire.Session.DroppedTraceCount != 1 {
		t.Fatalf("streamed status crossed epochs: %#v", wire)
	}
	if value.PendingExportCount != 1 || value.ProcessMemory.PinnedBytes != 1 ||
		value.Session.DroppedTraceCount != 1 {
		t.Fatalf("scalar status crossed epochs: %#v", value)
	}

	manager.exportMu.Lock()
	manager.reservedExportSlots = 0
	manager.exportMu.Unlock()
	session.mu.Lock()
	session.droppedTraceCount = 0
	session.mu.Unlock()
	manager.mu.Lock()
	manager.processPinned = 0
	manager.mu.Unlock()
}

func TestStatusEpochPreventsTornSnapshotsUnderRace(t *testing.T) {
	manager := newTestManager(t, nil)
	startTestSession(t, manager, 2, 1<<20, "provider-a")
	session := makeStatusQuotaExact(t, manager)
	writerDone := make(chan error, 1)
	go func() {
		for iteration := 0; iteration < 250; iteration++ {
			marker := iteration & 1
			var mutation statusEpochMutation
			if !manager.beginStatusEpochMutation(&mutation) {
				writerDone <- errors.New("could not begin status epoch")
				return
			}
			manager.exportMu.Lock()
			manager.reservedExportSlots = marker
			manager.exportMu.Unlock()
			runtime.Gosched()
			session.mu.Lock()
			session.droppedTraceCount = uint64(marker)
			session.mu.Unlock()
			runtime.Gosched()
			manager.mu.Lock()
			manager.processPinned = int64(marker)
			manager.mu.Unlock()
			if !mutation.finish() {
				writerDone <- errors.New("could not finish status epoch")
				return
			}
		}
		writerDone <- nil
	}()

	for {
		select {
		case err := <-writerDone:
			if err != nil {
				t.Fatal(err)
			}
			return
		default:
		}
		lease, err := manager.OpenStatus(context.Background())
		if err != nil {
			t.Fatalf("OpenStatus() error = %v", err)
		}
		wire := readStatusLease(t, lease)
		if wire.Session == nil || wire.PendingExportCount != int(wire.ProcessMemory.PinnedBytes) ||
			wire.PendingExportCount != int(wire.Session.DroppedTraceCount) {
			t.Fatalf("streamed status is torn: %#v", wire)
		}
		value := manager.Status()
		if value.PendingExportCount != int(value.ProcessMemory.PinnedBytes) ||
			value.PendingExportCount != int(value.Session.DroppedTraceCount) {
			t.Fatalf("scalar status is torn: %#v", value)
		}
	}
}

func TestStatusValueContainsNoRetainedReferences(t *testing.T) {
	var inspect func(reflect.Type, string)
	inspect = func(current reflect.Type, path string) {
		switch current.Kind() {
		case reflect.Array:
			inspect(current.Elem(), path+"[]")
		case reflect.Struct:
			for index := 0; index < current.NumField(); index++ {
				field := current.Field(index)
				inspect(field.Type, path+"."+field.Name)
			}
		case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer,
			reflect.Slice, reflect.String, reflect.UnsafePointer:
			t.Errorf("%s retains %s", path, current.Kind())
		}
	}
	inspect(reflect.TypeOf(Status{}), "Status")
}

func TestStatusLeaseContainsOnlyManagerNumericCapability(t *testing.T) {
	typeOfLease := reflect.TypeOf(StatusLease{})
	if typeOfLease.NumField() != 2 ||
		typeOfLease.Field(0).Type != reflect.TypeOf((*Manager)(nil)) ||
		typeOfLease.Field(1).Type.Kind() != reflect.Uint64 {
		t.Fatalf("StatusLease must contain only manager and numeric sequence: %v", typeOfLease)
	}
}

func TestStaleStatusLeaseCannotRetainOrReleaseLaterGeneration(t *testing.T) {
	manager := newTestManager(t, nil)
	firstInfo := startTestSession(t, manager, 1, 1<<20, "provider-a")
	lease, err := manager.OpenStatus(context.Background())
	if err != nil {
		t.Fatalf("OpenStatus() error = %v", err)
	}
	stale := lease
	if err = lease.Close(); err != nil {
		t.Fatalf("StatusLease.Close() error = %v", err)
	}
	if err = manager.Stop(firstInfo.SessionID); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	secondInfo := startTestSession(t, manager, 1, 1<<20, "provider-b")
	current, err := manager.OpenStatus(context.Background())
	if err != nil {
		t.Fatalf("second OpenStatus() error = %v", err)
	}
	if err = stale.Close(); err != nil {
		t.Fatalf("stale StatusLease.Close() error = %v", err)
	}
	status := readStatusLease(t, current)
	if status.Session == nil || status.Session.SessionID != secondInfo.SessionID {
		t.Fatalf("stale lease changed current claim: %#v", status)
	}
}

func TestDetachedSessionIdentityRemainsOwnedUntilFinalOperation(t *testing.T) {
	manager := newTestManager(t, nil)
	info := startTestSession(t, manager, 1, 1<<20, "provider-a")
	session := manager.retainActive(info.Generation)
	if session == nil {
		t.Fatal("retainActive() did not acquire operation owner")
	}
	if err := manager.Stop(info.SessionID); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if session.id != info.SessionID {
		t.Fatalf("detached identity = %q, want %q", session.id, info.SessionID)
	}
	manager.mu.Lock()
	charged := manager.processCharged
	manager.mu.Unlock()
	if charged != sessionRootChargeBytes {
		t.Fatalf("detached root charge = %d, want %d", charged, sessionRootChargeBytes)
	}
	if !session.releaseOwner() {
		t.Fatal("releaseOwner() failed")
	}
	manager.mu.Lock()
	charged = manager.processCharged
	manager.mu.Unlock()
	if charged != 0 {
		t.Fatalf("final process charge = %d, want 0", charged)
	}
}
