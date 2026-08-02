package requestcapture

import (
	"errors"
	"fmt"
	"testing"
)

func addCompletedRecord(t *testing.T, manager *Manager, index int) string {
	t.Helper()
	gateway, recorder := beginTestHTTP(manager, fmt.Sprintf("gateway-%d", index), "selected", nil)
	completeHTTP(recorder, []byte(fmt.Sprintf("payload-%d", index)))
	gateway.Finish(GatewayOutcome{})
	return recorder.ID()
}

func TestListRecordsStableWatermarkPagination(t *testing.T) {
	manager := newTestManager(t, nil)
	session := startTestSession(t, manager, 10, 1<<20, "selected")
	ids := []string{
		addCompletedRecord(t, manager, 1),
		addCompletedRecord(t, manager, 2),
		addCompletedRecord(t, manager, 3),
	}
	first, err := readRecordPageForTest(t, manager, session.SessionID, ListQuery{Limit: 2})
	if err != nil {
		t.Fatalf("first ListRecords() error = %v", err)
	}
	if len(first.Records) != 2 || first.Records[0].RecordID != ids[2] || first.Records[1].RecordID != ids[1] {
		t.Fatalf("first page = %#v", first.Records)
	}
	if first.NextCursor == "" || first.SnapshotWatermark == "" {
		t.Fatalf("first pagination tokens = %#v", first)
	}

	newID := addCompletedRecord(t, manager, 4)
	second, err := readRecordPageForTest(t, manager, session.SessionID, ListQuery{
		Limit:             2,
		Cursor:            first.NextCursor,
		SnapshotWatermark: first.SnapshotWatermark,
	})
	if err != nil {
		t.Fatalf("second ListRecords() error = %v", err)
	}
	if len(second.Records) != 1 || second.Records[0].RecordID != ids[0] {
		t.Fatalf("second page = %#v", second.Records)
	}
	for _, record := range second.Records {
		if record.RecordID == newID {
			t.Fatal("record created after watermark appeared in page")
		}
	}
}

func TestListRecordsReportsEvictionGap(t *testing.T) {
	manager := newTestManager(t, nil)
	session := startTestSession(t, manager, 3, 1<<20, "selected")
	for index := 0; index < 3; index++ {
		addCompletedRecord(t, manager, index)
	}
	first, err := readRecordPageForTest(t, manager, session.SessionID, ListQuery{Limit: 1})
	if err != nil {
		t.Fatalf("first ListRecords() error = %v", err)
	}

	// Tighten retention through the session domain state to exercise eviction
	// between cursor pages without changing the public Start contract.
	active := manager.active.Load()
	active.mu.Lock()
	active.recordsPerProvider = 1
	active.enforceProviderRetentionLocked("selected")
	active.mu.Unlock()

	second, err := readRecordPageForTest(t, manager, session.SessionID, ListQuery{
		Limit:             2,
		Cursor:            first.NextCursor,
		SnapshotWatermark: first.SnapshotWatermark,
	})
	if err != nil {
		t.Fatalf("second ListRecords() error = %v", err)
	}
	if !second.EvictionGap.Detected || second.EvictionGap.RecordCount != 2 {
		t.Fatalf("eviction gap = %#v", second.EvictionGap)
	}
}

func TestListRecordsRejectsInvalidOrCrossGenerationCursor(t *testing.T) {
	manager := newTestManager(t, nil)
	firstSession := startTestSession(t, manager, 3, 1<<20, "selected")
	addCompletedRecord(t, manager, 1)
	page, err := readRecordPageForTest(t, manager, firstSession.SessionID, ListQuery{Limit: 1})
	if err != nil {
		t.Fatalf("ListRecords() error = %v", err)
	}
	if _, err := readRecordPageForTest(t, manager, firstSession.SessionID, ListQuery{Limit: 0, Cursor: "bad", SnapshotWatermark: "bad"}); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("invalid cursor error = %v", err)
	}
	if _, err := readRecordPageForTest(t, manager, firstSession.SessionID, ListQuery{Limit: DefaultMaxListLimit + 1}); err == nil {
		t.Fatal("oversized limit succeeded")
	}
	if err := manager.Stop(firstSession.SessionID); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	secondSession := startTestSession(t, manager, 3, 1<<20, "selected")
	if _, err := readRecordPageForTest(t, manager, secondSession.SessionID, ListQuery{
		Limit:             1,
		Cursor:            page.NextCursor,
		SnapshotWatermark: page.SnapshotWatermark,
	}); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("cross-generation cursor error = %v", err)
	}
}

func TestGetRecordValidationAndNotFound(t *testing.T) {
	manager := newTestManager(t, nil)
	session := startTestSession(t, manager, 2, 1<<20, "selected")
	if _, err := readRecordDetailForTest(t, manager, session.SessionID, "missing", manager.cfg.previewBytes+1); err == nil {
		t.Fatal("oversized preview succeeded")
	}
	if _, err := readRecordDetailForTest(t, manager, session.SessionID, "missing", 0); !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("missing record error = %v", err)
	}
	if _, err := readRecordDetailForTest(t, manager, "stale", "missing", 0); !errors.Is(err, ErrSessionMismatch) {
		t.Fatalf("stale session error = %v", err)
	}
}
