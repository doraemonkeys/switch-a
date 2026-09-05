package requestingress

import (
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
)

func TestReviewStorageFailureAfterEOFNotifiesObservers(t *testing.T) {
	storage := &testStorage{readErr: io.ErrUnexpectedEOF}
	var mu sync.Mutex
	var events []Event
	var finishes []Snapshot
	h := startTest(t, io.NopCloser(strings.NewReader("body")), -1, Options{
		MemoryBytes: -1, CreateStorage: storage.create,
		Trace:    func(e Event) { mu.Lock(); events = append(events, e); mu.Unlock() },
		OnFinish: func(s Snapshot) { mu.Lock(); finishes = append(finishes, s); mu.Unlock() },
	})
	waitTest(t, h)
	reader := openTest(t, h)
	if _, err := reader.Read(make([]byte, 4)); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatal(err)
	}
	if h.Snapshot().State != Failed {
		t.Fatal(h.Snapshot())
	}
	_ = reader.Close()
	_ = h.Close()
	mu.Lock()
	defer mu.Unlock()
	failedEvent := false
	for _, e := range events {
		failedEvent = failedEvent || e.Name == "failed"
	}
	t.Logf("final state=%s; finish count=%d, last finish=%s; failed trace=%v", h.Snapshot().State, len(finishes), finishes[len(finishes)-1].State, failedEvent)
	if !failedEvent {
		t.Fatal("storage failure after complete has no failed transition notification")
	}
}
