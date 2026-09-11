package requestcapture

import (
	"errors"
	"fmt"
	"testing"
)

func TestCaptureRetentionKeepsRollingAtCountAndMemoryLimits(t *testing.T) {
	const (
		recordsPerProvider       = 100
		exchanges                = 1_000
		fiveGiB            int64 = 5 << 30
	)
	for _, test := range []struct {
		name      string
		quota     int64
		wantCount int
	}{
		{name: "100_records_with_5_GiB", quota: fiveGiB, wantCount: recordsPerProvider},
		{name: "memory_fills_before_100_records", quota: 256 << 10},
	} {
		t.Run(test.name, func(t *testing.T) {
			manager := newTestManager(t, func(cfg *Config) {
				cfg.ProcessCeilingBytes = fiveGiB
			})
			session := startTestSession(t, manager, recordsPerProvider, test.quota, "selected")
			payload := make([]byte, 8<<10)
			var firstID, lastID string
			for index := range exchanges {
				gateway, recorder := beginTestHTTP(manager, fmt.Sprintf("rolling-%d", index), "selected", nil)
				if !recorder.Valid() {
					t.Fatalf("capture stopped accepting exchanges at %d", index)
				}
				completeHTTP(recorder, payload)
				gateway.Finish(GatewayOutcome{})
				lastID = recorder.ID()
				if index == 0 {
					firstID = lastID
				}
				status := manager.Status()
				if status.ProcessMemory.ChargedBytes > test.quota ||
					status.Session.CompletedRecordCount > recordsPerProvider {
					t.Fatalf("retention exceeded limits at %d: %#v", index, status)
				}
			}
			status := manager.Status()
			retained := status.Session.CompletedRecordCount
			if retained == 0 || status.Session.EvictedRecordCount != uint64(exchanges-retained) ||
				status.Session.ActiveRecordCount != 0 || status.Session.DroppedExchangeCount != 0 ||
				status.Session.OverflowedRecordCount != 0 {
				t.Fatalf("capture did not keep rolling: %#v", status.Session)
			}
			if test.wantCount != 0 && retained != test.wantCount {
				t.Fatalf("retained %d records, want %d", retained, test.wantCount)
			}
			if test.wantCount == 0 && retained >= recordsPerProvider {
				t.Fatal("memory pressure did not trigger earlier eviction")
			}
			retainedSession := manager.active.Load()
			retainedSession.mu.Lock()
			_, firstErr := retainedSession.lookupRecordLocked(firstID)
			_, lastErr := retainedSession.lookupRecordLocked(lastID)
			retainedSession.mu.Unlock()
			if !errors.Is(firstErr, ErrRecordEvicted) {
				t.Fatalf("oldest record lookup = %v, want eviction", firstErr)
			}
			if lastErr != nil {
				t.Fatalf("latest record is unavailable: %v", lastErr)
			}
			if err := manager.Stop(session.SessionID); err != nil {
				t.Fatal(err)
			}
			if status := manager.Status(); status.ProcessMemory.ChargedBytes != 0 {
				t.Fatalf("stop retained capture memory: %#v", status.ProcessMemory)
			}
		})
	}
}
