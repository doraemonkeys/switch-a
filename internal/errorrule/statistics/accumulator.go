// Package statistics accumulates internal-error rule hits without putting
// SQLite work on response-processing goroutines.
package statistics

import (
	"context"
	"fmt"
	"hash/fnv"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/doraemonkeys/switch-a/internal/errorrule"
)

const StatsShardCount = 32

type Handle struct {
	RuleID     errorrule.RuleID
	Generation errorrule.RuleGeneration
}

func HandleFor(rule errorrule.Rule) Handle {
	return Handle{RuleID: rule.ID, Generation: rule.Generation()}
}

func (h Handle) Validate() error {
	if err := h.RuleID.Validate(); err != nil {
		return fmt.Errorf("statistics handle: %w", err)
	}
	if h.Generation.IsZero() {
		return fmt.Errorf("statistics handle generation is required")
	}
	return nil
}

type Delta struct {
	Handle    Handle
	HitCount  uint64
	LastHitAt time.Time
}

type ApplyResult struct {
	Missing []Handle
}

// Sink is defined where the hot-path accumulator consumes persistence. A sink
// must apply the complete batch transactionally and must not create rows.
type Sink interface {
	ApplyRuleStatDeltas(context.Context, []Delta) (ApplyResult, error)
}

type counterState struct {
	hitCount  uint64
	lastHitAt int64
}

type counter struct {
	state atomic.Pointer[counterState]
}

func (c *counter) add(hitCount uint64, lastHitAt int64) {
	for {
		current := c.state.Load()
		next := counterState{hitCount: hitCount, lastHitAt: lastHitAt}
		if current != nil {
			next.hitCount = saturatingAdd(current.hitCount, hitCount)
			if current.lastHitAt > next.lastHitAt {
				next.lastHitAt = current.lastHitAt
			}
		}
		if c.state.CompareAndSwap(current, &next) {
			return
		}
	}
}

func (c *counter) drain() *counterState {
	return c.state.Swap(nil)
}

func saturatingAdd(left, right uint64) uint64 {
	if math.MaxUint64-left < right {
		return math.MaxUint64
	}
	return left + right
}

type shard struct {
	mu       sync.RWMutex
	counters map[Handle]*counter
}

type Accumulator struct {
	sink   Sink
	shards [StatsShardCount]shard
}

func New(sink Sink) (*Accumulator, error) {
	if sink == nil {
		return nil, fmt.Errorf("statistics sink is required")
	}
	accumulator := &Accumulator{sink: sink}
	for index := range accumulator.shards {
		accumulator.shards[index].counters = make(map[Handle]*counter)
	}
	return accumulator, nil
}

// Hit performs only in-memory work. The immutable handle keeps late hits tied
// to the exact lifecycle generation observed by the request snapshot.
func (a *Accumulator) Hit(handle Handle, at time.Time) error {
	if err := handle.Validate(); err != nil {
		return err
	}
	if at.IsZero() {
		return fmt.Errorf("statistics hit time is required")
	}
	counter := a.counterFor(handle)
	counter.add(1, at.UTC().UnixNano())
	return nil
}

func (a *Accumulator) counterFor(handle Handle) *counter {
	shard := &a.shards[shardIndex(handle)]
	shard.mu.RLock()
	existing := shard.counters[handle]
	shard.mu.RUnlock()
	if existing != nil {
		return existing
	}

	shard.mu.Lock()
	defer shard.mu.Unlock()
	if existing = shard.counters[handle]; existing != nil {
		return existing
	}
	existing = &counter{}
	shard.counters[handle] = existing
	return existing
}

func shardIndex(handle Handle) uint32 {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(handle.RuleID))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(handle.Generation.String()))
	return hash.Sum32() % StatsShardCount
}

func (a *Accumulator) Flush(ctx context.Context) error {
	deltas, drained := a.drain()
	if len(deltas) == 0 {
		return nil
	}
	result, err := a.sink.ApplyRuleStatDeltas(ctx, deltas)
	if err != nil {
		a.restore(drained)
		return err
	}
	// Successful update-only persistence intentionally drops deltas reported as
	// missing and evicts their dormant counters. A concurrent late hit may have
	// reached the same counter after drain, but it belongs to the retired
	// generation too and must not survive for a later flush.
	for _, handle := range result.Missing {
		if _, belongsToBatch := drained[handle]; belongsToBatch {
			a.Retire(handle)
		}
	}
	return nil
}

type drainedCounter struct {
	counter *counter
	state   *counterState
}

func (a *Accumulator) drain() ([]Delta, map[Handle]drainedCounter) {
	deltas := make([]Delta, 0)
	drained := make(map[Handle]drainedCounter)
	for index := range a.shards {
		shard := &a.shards[index]
		shard.mu.RLock()
		for handle, counter := range shard.counters {
			state := counter.drain()
			if state == nil || state.hitCount == 0 {
				continue
			}
			drained[handle] = drainedCounter{counter: counter, state: state}
			deltas = append(deltas, Delta{
				Handle:    handle,
				HitCount:  state.hitCount,
				LastHitAt: time.Unix(0, state.lastHitAt).UTC(),
			})
		}
		shard.mu.RUnlock()
	}
	return deltas, drained
}

// restore is used only after a failed transaction, when persistence has not
// classified any generation as retired.
func (a *Accumulator) restore(drained map[Handle]drainedCounter) {
	for _, item := range drained {
		item.counter.add(item.state.hitCount, item.state.lastHitAt)
	}
}

func (a *Accumulator) Pending(handle Handle) errorrule.RuleStats {
	stats := errorrule.RuleStats{RuleID: handle.RuleID}
	shard := &a.shards[shardIndex(handle)]
	shard.mu.RLock()
	counter := shard.counters[handle]
	shard.mu.RUnlock()
	if counter == nil {
		return stats
	}
	state := counter.state.Load()
	if state == nil {
		return stats
	}
	stats.HitCount = state.hitCount
	lastHitAt := time.Unix(0, state.lastHitAt).UTC()
	stats.LastHitAt = &lastHitAt
	return stats
}

func (a *Accumulator) Retire(handle Handle) {
	shard := &a.shards[shardIndex(handle)]
	shard.mu.Lock()
	delete(shard.counters, handle)
	shard.mu.Unlock()
}

func (a *Accumulator) Overlay(persisted []errorrule.RuleStats, rules []errorrule.Rule) []errorrule.RuleStats {
	handles := make(map[errorrule.RuleID]Handle, len(rules))
	for _, rule := range rules {
		handles[rule.ID] = HandleFor(rule)
	}
	result := make([]errorrule.RuleStats, len(persisted))
	for index, stored := range persisted {
		result[index] = stored
		handle, exists := handles[stored.RuleID]
		if !exists {
			continue
		}
		pending := a.Pending(handle)
		result[index].HitCount = saturatingAdd(result[index].HitCount, pending.HitCount)
		if pending.LastHitAt != nil && (result[index].LastHitAt == nil || pending.LastHitAt.After(*result[index].LastHitAt)) {
			latest := *pending.LastHitAt
			result[index].LastHitAt = &latest
		}
	}
	return result
}
