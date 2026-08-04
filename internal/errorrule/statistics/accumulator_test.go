package statistics

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/errorrule"
)

const (
	testRuleID     = errorrule.RuleID("11111111-1111-4111-8111-111111111111")
	testGeneration = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
)

type recordingSink struct {
	mu      sync.Mutex
	batches [][]Delta
	fail    error
	missing []Handle
}

func (s *recordingSink) ApplyRuleStatDeltas(_ context.Context, deltas []Delta) (ApplyResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	batch := append([]Delta(nil), deltas...)
	s.batches = append(s.batches, batch)
	if s.fail != nil {
		return ApplyResult{}, s.fail
	}
	return ApplyResult{Missing: append([]Handle(nil), s.missing...)}, nil
}

func mustHandle(t *testing.T, ruleID errorrule.RuleID, generation string) Handle {
	t.Helper()
	parsed, err := errorrule.ParseRuleGeneration(generation)
	if err != nil {
		t.Fatal(err)
	}
	return Handle{RuleID: ruleID, Generation: parsed}
}

func TestAccumulatorConcurrentHitsFlushExactDelta(t *testing.T) {
	sink := &recordingSink{}
	accumulator, err := New(sink)
	if err != nil {
		t.Fatal(err)
	}
	handle := mustHandle(t, testRuleID, testGeneration)
	const workers = 32
	const hitsPerWorker = 200
	base := time.Date(2026, 8, 3, 8, 0, 0, 0, time.UTC)

	var wait sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		worker := worker
		wait.Add(1)
		go func() {
			defer wait.Done()
			for hit := 0; hit < hitsPerWorker; hit++ {
				if err := accumulator.Hit(handle, base.Add(time.Duration(worker+hit)*time.Nanosecond)); err != nil {
					t.Errorf("Hit() error = %v", err)
				}
			}
		}()
	}
	wait.Wait()
	if err := accumulator.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(sink.batches) != 1 || len(sink.batches[0]) != 1 {
		t.Fatalf("batches = %#v", sink.batches)
	}
	delta := sink.batches[0][0]
	if delta.HitCount != workers*hitsPerWorker {
		t.Fatalf("hit count = %d", delta.HitCount)
	}
	wantLast := base.Add(time.Duration(workers-1+hitsPerWorker-1) * time.Nanosecond)
	if !delta.LastHitAt.Equal(wantLast) {
		t.Fatalf("last hit = %v, want %v", delta.LastHitAt, wantLast)
	}
	if pending := accumulator.Pending(handle); pending.HitCount != 0 {
		t.Fatalf("pending after flush = %#v", pending)
	}
}

func TestAccumulatorFailedFlushRestoresExactDelta(t *testing.T) {
	temporary := errors.New("temporary")
	sink := &recordingSink{fail: temporary}
	accumulator, _ := New(sink)
	handle := mustHandle(t, testRuleID, testGeneration)
	first := time.Date(2026, 8, 3, 1, 0, 0, 0, time.UTC)
	second := first.Add(time.Second)
	_ = accumulator.Hit(handle, first)
	_ = accumulator.Hit(handle, second)

	if err := accumulator.Flush(context.Background()); !errors.Is(err, temporary) {
		t.Fatalf("Flush() error = %v", err)
	}
	pending := accumulator.Pending(handle)
	if pending.HitCount != 2 || pending.LastHitAt == nil || !pending.LastHitAt.Equal(second) {
		t.Fatalf("restored pending = %#v", pending)
	}
	sink.fail = nil
	if err := accumulator.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := sink.batches[1][0].HitCount; got != 2 {
		t.Fatalf("retried delta = %d", got)
	}
}

func TestAccumulatorMissingGenerationDropsLateDelta(t *testing.T) {
	handle := mustHandle(t, testRuleID, testGeneration)
	sink := &recordingSink{missing: []Handle{handle}}
	accumulator, _ := New(sink)
	_ = accumulator.Hit(handle, time.Now())
	if err := accumulator.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if pending := accumulator.Pending(handle); pending.HitCount != 0 {
		t.Fatalf("retired pending = %#v", pending)
	}
	accumulator.Retire(handle)
	if pending := accumulator.Pending(handle); pending.RuleID != handle.RuleID || pending.HitCount != 0 {
		t.Fatalf("pending after retirement = %#v", pending)
	}
}

func TestAccumulatorOverlayAndGenerationIsolation(t *testing.T) {
	oldHandle := mustHandle(t, testRuleID, testGeneration)
	newHandle := mustHandle(t, testRuleID, "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
	accumulator, _ := New(&recordingSink{})
	oldTime := time.Date(2026, 8, 3, 1, 0, 0, 0, time.UTC)
	newTime := oldTime.Add(time.Hour)
	_ = accumulator.Hit(oldHandle, oldTime)
	_ = accumulator.Hit(newHandle, newTime)

	rule := errorrule.NewRule(errorrule.RuleSpec{}, errorrule.RuleMetadata{
		ID: testRuleID, Generation: newHandle.Generation,
	})
	persistedTime := oldTime.Add(time.Minute)
	overlay := accumulator.Overlay([]errorrule.RuleStats{{
		RuleID: testRuleID, HitCount: math.MaxUint64, LastHitAt: &persistedTime,
	}}, []errorrule.Rule{rule})
	if overlay[0].HitCount != math.MaxUint64 || overlay[0].LastHitAt == nil || !overlay[0].LastHitAt.Equal(newTime) {
		t.Fatalf("overlay = %#v", overlay[0])
	}
}

func TestAccumulatorValidationAndShutdownFlush(t *testing.T) {
	if _, err := New(nil); err == nil {
		t.Fatal("New(nil) error = nil")
	}
	sink := &recordingSink{}
	accumulator, _ := New(sink)
	if err := accumulator.Hit(Handle{}, time.Now()); err == nil {
		t.Fatal("invalid handle accepted")
	}
	handle := mustHandle(t, testRuleID, testGeneration)
	if err := accumulator.Hit(handle, time.Time{}); err == nil {
		t.Fatal("zero timestamp accepted")
	}
	_ = accumulator.Hit(handle, time.Now())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := accumulator.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(sink.batches) != 1 {
		t.Fatalf("shutdown flush batches = %d", len(sink.batches))
	}
}
