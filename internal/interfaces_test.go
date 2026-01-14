package internal

import (
	"testing"
	"time"
)

func TestRealClock_Now(t *testing.T) {
	clock := RealClock{}
	before := time.Now()
	now := clock.Now()
	after := time.Now()

	if now.Before(before) || now.After(after) {
		t.Errorf("RealClock.Now() returned %v, expected between %v and %v", now, before, after)
	}
}

func TestRealClock_NewTicker(t *testing.T) {
	clock := RealClock{}
	ticker := clock.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	if ticker == nil {
		t.Fatal("expected non-nil ticker")
	}

	// Verify ticker fires
	select {
	case <-ticker.C:
		// Success
	case <-time.After(100 * time.Millisecond):
		t.Error("ticker did not fire within expected time")
	}
}
