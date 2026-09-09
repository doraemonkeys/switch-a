package responsefacts

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestTrackerKeepsEarlierCompletionOutOfNextRound(t *testing.T) {
	var tracker Tracker
	at := time.Unix(1, 0)
	tracker.Observe(false, ResponseCreate, "", "", at)
	tracker.Observe(true, ResponseCreated, "first", "", at)
	tracker.Observe(true, ResponseCompleted, "first", StatusCompleted, at)
	if !tracker.Snapshot().Current.Completed() {
		t.Fatal("first response did not complete")
	}
	tracker.Observe(false, ResponseCreate, "", "", at)
	tracker.Observe(true, ResponseCompleted, "first", StatusCompleted, at)
	progress := tracker.Snapshot()
	if progress.Current.Completed() || progress.Current.Round != 2 || progress.CompletedResponses != 1 || progress.LastCompleted.ResponseID != "first" {
		t.Fatalf("old completion contaminated new upload: %+v", progress)
	}
	tracker.Observe(true, ResponseCreated, "second", "", at)
	tracker.Observe(true, ResponseCompleted, "second", StatusCompleted, at)
	if tracker.Snapshot().CompletedResponses != 2 || !tracker.Snapshot().Current.Completed() {
		t.Fatal(tracker.Snapshot())
	}
	tracker.Observe(true, ResponseCreated, "second", "", at)
	if !tracker.Snapshot().Current.Completed() {
		t.Fatal("duplicate created erased completion")
	}
}

func TestTrackerBindsOutstandingRequestsAndKeepsTerminalStatus(t *testing.T) {
	var tracker Tracker
	at := time.Unix(1, 0)
	for range 2 {
		tracker.Observe(false, ResponseCreate, "", "", at)
	}
	tracker.Observe(true, ResponseCreated, "one", "", at)
	tracker.Observe(true, ResponseCreated, "two", "", at)
	tracker.Observe(true, ResponseCompleted, "one", StatusCompleted, at)
	if tracker.Snapshot().Current.Completed() {
		t.Fatal("first response completed the second request")
	}
	tracker.Observe(true, ResponseDone, "two", "cancelled", at)
	if tracker.Snapshot().Current.Completed() || tracker.Snapshot().Current.Status != "cancelled" {
		t.Fatal(tracker.Snapshot())
	}
	if tracker.Observe(false, "session.update", "", "", at) || tracker.Observe(true, "response.delta", "", "", at) {
		t.Fatal("unrelated event changed round")
	}
	for _, event := range []string{ResponseFailed, ResponseIncomplete} {
		tracker.Observe(true, event, "two", "failed", at)
		if tracker.Snapshot().Current.Completed() {
			t.Fatal("failure counted as completion")
		}
	}
}

func TestTrackerHandlesUnsolicitedAndAnonymousResponses(t *testing.T) {
	at := time.Unix(1, 0)
	var tracker Tracker
	tracker.Observe(true, ResponseCompleted, "server", "", at)
	if !tracker.Snapshot().Current.Completed() {
		t.Fatal(tracker.Snapshot())
	}
	tracker.Observe(false, ResponseCreate, "", "", at)
	tracker.Observe(true, ResponseCreated, "", "", at)
	tracker.Observe(true, ResponseDone, "", "", at)
	if !tracker.Snapshot().Current.Completed() || tracker.Snapshot().Current.Round != 2 {
		t.Fatal(tracker.Snapshot())
	}
}

func TestWriteReportsPartialFailureWithoutReceiptInference(t *testing.T) {
	var result Write
	result.Record(12, nil)
	result.Record(3, errors.New("socket closed"))
	if result.Calls != 2 || result.SuccessfulCalls != 1 || result.FailedCalls != 1 || result.ConfirmedBytes != 15 || result.LastError != "socket closed" {
		t.Fatal(result)
	}
}

func TestCompletionObservationSurvivesIndependentWriteFailure(t *testing.T) {
	var missing *CompletionObserver
	if missing.Snapshot().EventType != "" {
		t.Fatal("nil observer invented completion")
	}
	var observer CompletionObserver
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() { observer.Observe(ResponseCompleted); _ = observer.Snapshot() })
	}
	wg.Wait()
	var result Write
	result.Record(0, errors.New("broken pipe"))
	if observer.Snapshot().EventType != ResponseCompleted || observer.Snapshot().ObservedAt.IsZero() || result.SuccessfulCalls != 0 {
		t.Fatal("independent facts lost")
	}
}

func TestTrackerAnonymousCreatedDoesNotInventAnotherRound(t *testing.T) {
	var tracker Tracker
	at := time.Unix(1, 0)
	tracker.Observe(false, ResponseCreate, "", "", at)
	tracker.Observe(true, ResponseCreated, "", "", at)
	tracker.Observe(true, ResponseCompleted, "identified", StatusCompleted, at)
	if progress := tracker.Snapshot(); progress.Current.Round != 1 || progress.LastCompleted.ResponseID != "identified" || progress.CompletedResponses != 1 {
		t.Fatal(progress)
	}
	tracker.Observe(false, ResponseCreate, "", "", at)
	tracker.Observe(true, ResponseCreated, "second", "", at)
	tracker.Observe(true, ResponseDone, "", "", at)
	if tracker.Snapshot().LastCompleted.ResponseID != "second" {
		t.Fatal(tracker.Snapshot())
	}
	tracker.Observe(false, ResponseCreate, "", "", at)
	tracker.Observe(true, ResponseFailed, "third", "failed", at)
	tracker.Observe(true, ResponseCreated, "third", "", at)
	if tracker.Snapshot().Current.EventType != ResponseFailed {
		t.Fatal("late created erased failure")
	}
}
