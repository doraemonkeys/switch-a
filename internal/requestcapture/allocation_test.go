package requestcapture

import "testing"

func TestCaptureAllocationAccountsPeakCommitReplaceAndRollback(t *testing.T) {
	manager := newTestManager(t, nil)
	_ = startTestSession(t, manager, 1, 1<<20, "selected")
	session := manager.active.Load()

	session.mu.Lock()
	manager.mu.Lock()
	baselineSession := session.chargedBytes
	baselineProcess := manager.processCharged
	manager.mu.Unlock()

	plan := session.planTransitionAllocationLocked(100, 50)
	var allocation CaptureAllocation
	if !session.beginCaptureAllocationLocked(plan, &allocation) {
		session.mu.Unlock()
		t.Fatal("beginCaptureAllocationLocked() rejected exact reservation")
	}
	manager.mu.Lock()
	if session.chargedBytes != baselineSession+150 ||
		session.temporaryBytes != 150 ||
		manager.processCharged != baselineProcess+150 ||
		manager.processTemporary != 150 {
		manager.mu.Unlock()
		session.mu.Unlock()
		t.Fatalf("temporary reservation mismatch: session=(%d,%d) process=(%d,%d)",
			session.chargedBytes, session.temporaryBytes,
			manager.processCharged, manager.processTemporary)
	}
	manager.mu.Unlock()

	if !allocation.claimCandidate(80) ||
		!allocation.claimScratch(50) ||
		!allocation.releaseScratch(50) {
		session.mu.Unlock()
		t.Fatal("valid allocation claims were rejected")
	}
	var commit captureCommit
	if !allocation.beginCommitAccountingLocked(nil, &commit) {
		session.mu.Unlock()
		t.Fatal("beginCommitAccountingLocked() failed")
	}
	published := true
	commit.finishLocked(&allocation)
	if !published {
		session.mu.Unlock()
		t.Fatal("typed publication did not run")
	}
	manager.mu.Lock()
	if session.chargedBytes != baselineSession+80 ||
		session.temporaryBytes != 0 ||
		manager.processCharged != baselineProcess+80 ||
		manager.processTemporary != 0 {
		manager.mu.Unlock()
		session.mu.Unlock()
		t.Fatalf("commit accounting mismatch: session=(%d,%d) process=(%d,%d)",
			session.chargedBytes, session.temporaryBytes,
			manager.processCharged, manager.processTemporary)
	}
	manager.mu.Unlock()

	replacementPlan := session.planMessageResultAllocationLocked(40, 0)
	var replacement CaptureAllocation
	retired := ownedCharge{bytes: 30, live: true}
	var replacementCommit captureCommit
	if !session.beginCaptureAllocationLocked(replacementPlan, &replacement) ||
		!replacement.claimCandidate(40) ||
		!replacement.beginCommitAccountingLocked(&retired, &replacementCommit) {
		session.mu.Unlock()
		t.Fatal("replacement allocation failed")
	}
	replacementCommit.finishLocked(&replacement)
	manager.mu.Lock()
	if session.chargedBytes != baselineSession+90 ||
		manager.processCharged != baselineProcess+90 {
		manager.mu.Unlock()
		session.mu.Unlock()
		t.Fatalf("replace accounting mismatch: session=%d process=%d",
			session.chargedBytes, manager.processCharged)
	}
	manager.mu.Unlock()

	rollbackPlan := session.planHTTPResponseAllocationLocked(25, 10)
	var rollback CaptureAllocation
	if !session.beginCaptureAllocationLocked(rollbackPlan, &rollback) ||
		!rollback.rollbackLocked() || !rollback.rollbackLocked() {
		session.mu.Unlock()
		t.Fatal("rollback was not idempotent")
	}
	manager.mu.Lock()
	if session.chargedBytes != baselineSession+90 ||
		session.temporaryBytes != 0 ||
		manager.processCharged != baselineProcess+90 ||
		manager.processTemporary != 0 {
		manager.mu.Unlock()
		session.mu.Unlock()
		t.Fatal("rollback did not restore accounting")
	}
	manager.mu.Unlock()
	session.mu.Unlock()
}

func TestCaptureAllocationDenialAndEpochMismatchNeverPublish(t *testing.T) {
	manager := newTestManager(t, nil)
	_ = startTestSession(t, manager, 1, 1<<20, "selected")
	session := manager.active.Load()

	session.mu.Lock()
	manager.mu.Lock()
	baselineSession := session.chargedBytes
	baselineProcess := manager.processCharged
	session.quotaBytes = baselineSession + 99
	manager.mu.Unlock()

	deniedPlan := session.planBeginRecordAllocationLocked(100, 0)
	var denied CaptureAllocation
	if session.beginCaptureAllocationLocked(deniedPlan, &denied) {
		session.mu.Unlock()
		t.Fatal("allocation exceeding exact remaining session capacity succeeded")
	}

	session.quotaBytes = 1 << 20
	stalePlan := session.planBeginRecordAllocationLocked(80, 20)
	var allocation CaptureAllocation
	if !session.beginCaptureAllocationLocked(stalePlan, &allocation) || !allocation.claimCandidate(80) {
		session.mu.Unlock()
		t.Fatal("stale-epoch setup failed")
	}
	session.mutationEpoch++
	var commit captureCommit
	if allocation.beginCommitAccountingLocked(nil, &commit) {
		commit.abortLocked()
		session.mu.Unlock()
		t.Fatal("epoch-mismatched allocation entered publication")
	}
	if !allocation.rollbackLocked() {
		session.mu.Unlock()
		t.Fatal("epoch-mismatched allocation did not roll back")
	}
	manager.mu.Lock()
	if session.chargedBytes != baselineSession ||
		session.temporaryBytes != 0 ||
		manager.processCharged != baselineProcess ||
		manager.processTemporary != 0 {
		manager.mu.Unlock()
		session.mu.Unlock()
		t.Fatal("failed allocation changed final accounting")
	}
	manager.mu.Unlock()
	session.mu.Unlock()
}
