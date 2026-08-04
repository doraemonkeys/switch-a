package selector

import (
	"sync"
	"sync/atomic"
)

// ConcurrencyLimiter owns provider lifecycle generations and their slot counts.
// Retirement detaches a generation; outstanding leases keep the old counter
// alive until their exact capability is released.
type ConcurrencyLimiter struct {
	lifecycle      sync.RWMutex
	mu             sync.Mutex
	generations    map[string]*providerGeneration
	nextGeneration uint64
}

// NewConcurrencyLimiter creates a generation-aware concurrency limiter.
func NewConcurrencyLimiter() *ConcurrencyLimiter {
	return &ConcurrencyLimiter{
		generations: make(map[string]*providerGeneration),
	}
}

// Acquire reserves one slot and returns its unique ownership capability. A
// non-positive limit still returns an explicit no-op lease so every successful
// selection has the same cleanup contract.
func (l *ConcurrencyLimiter) Acquire(providerID string, limit int) (*SlotLease, bool) {
	guard := l.beginLifecycleRead()
	defer guard.Release()
	return l.acquireUnderLifecycle(providerID, limit)
}

func (l *ConcurrencyLimiter) acquireUnderLifecycle(providerID string, limit int) (*SlotLease, bool) {
	if l == nil || providerID == "" {
		return nil, false
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	l.initializeLocked()

	generation := l.generations[providerID]
	if generation == nil {
		generation = l.newGenerationLocked(providerID)
		l.generations[providerID] = generation
	}
	return l.acquireLocked(generation, limit)
}

// acquireInGeneration is used by active-continuity selection. It prevents an
// old active request from silently crossing into a recreated provider that
// happens to reuse the same external ID.
func (l *ConcurrencyLimiter) acquireInGeneration(current *SlotLease, limit int) (*SlotLease, bool) {
	guard := l.beginLifecycleRead()
	defer guard.Release()
	return l.acquireInGenerationUnderLifecycle(current, limit)
}

func (l *ConcurrencyLimiter) acquireInGenerationUnderLifecycle(current *SlotLease, limit int) (*SlotLease, bool) {
	if l == nil || current == nil || current.state == nil {
		return nil, false
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	l.initializeLocked()
	if !l.isCurrentLocked(current) {
		return nil, false
	}
	return l.acquireLocked(current.state.generation, limit)
}

func (l *ConcurrencyLimiter) acquireLocked(generation *providerGeneration, limit int) (*SlotLease, bool) {
	counted := limit > 0
	if counted && generation.active.Load() >= int64(limit) {
		return nil, false
	}
	if counted {
		generation.active.Add(1)
	}
	return &SlotLease{state: &slotLeaseState{
		generation: generation,
		counted:    counted,
		owner:      l,
	}}, true
}

// prepare atomically establishes a dispatch boundary against retirement. The
// transition callback lets reservations compete with release under the same
// lifecycle lock without exposing limiter internals.
func (l *ConcurrencyLimiter) prepare(lease *SlotLease, transition func() bool) bool {
	guard := l.beginLifecycleRead()
	defer guard.Release()
	return l.prepareUnderLifecycle(lease, transition)
}

func (l *ConcurrencyLimiter) prepareUnderLifecycle(lease *SlotLease, transition func() bool) bool {
	if l == nil || lease == nil || lease.state == nil {
		return false
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.isCurrentLocked(lease) {
		return false
	}
	if transition == nil {
		return true
	}
	return transition()
}

func (l *ConcurrencyLimiter) isCurrent(lease *SlotLease) bool {
	if l == nil || lease == nil || lease.state == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.isCurrentLocked(lease)
}

func (l *ConcurrencyLimiter) isCurrentLocked(lease *SlotLease) bool {
	state := lease.state
	return state.owner == l &&
		!state.released.Load() &&
		l.generations[state.generation.providerID] == state.generation
}

// Current reports active limited leases in the provider's current generation.
// Detached generations are intentionally invisible to administrative status.
func (l *ConcurrencyLimiter) Current(providerID string) int64 {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	generation := l.generations[providerID]
	if generation == nil {
		return 0
	}
	return generation.active.Load()
}

// retireGeneration is intentionally internal: durable mutations must use the
// callback boundary below so retirement cannot be separated from persistence.
// Tests use this primitive only to model external lifecycle invalidation.
func (l *ConcurrencyLimiter) retireGeneration(providerID string) {
	if l == nil || providerID == "" {
		return
	}
	l.lifecycle.Lock()
	defer l.lifecycle.Unlock()
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.generations, providerID)
}

// mutateWithRetiredGenerations makes the durable mutation and generation
// retirement one selector-visible boundary. Holding the lifecycle lock across
// persistence prevents a lease or dispatch permit from being created against
// the old store snapshot after retirement but before the write completes.
func (l *ConcurrencyLimiter) mutateWithRetiredGenerations(
	providerIDs []string,
	retireAll bool,
	mutation func() error,
) error {
	if l == nil {
		return mutation()
	}

	l.lifecycle.Lock()
	defer l.lifecycle.Unlock()
	l.mu.Lock()
	l.initializeLocked()
	if retireAll {
		clear(l.generations)
	} else {
		for _, providerID := range providerIDs {
			if providerID != "" {
				delete(l.generations, providerID)
			}
		}
	}
	l.mu.Unlock()
	return mutation()
}

type lifecycleReadLease struct {
	limiter  *ConcurrencyLimiter
	released atomic.Bool
}

func (l *ConcurrencyLimiter) beginLifecycleRead() *lifecycleReadLease {
	lease := &lifecycleReadLease{limiter: l}
	if l != nil {
		l.lifecycle.RLock()
	}
	return lease
}

func (l *lifecycleReadLease) Release() bool {
	if l == nil || !l.released.CompareAndSwap(false, true) {
		return false
	}
	if l.limiter != nil {
		l.limiter.lifecycle.RUnlock()
	}
	return true
}

func (l *ConcurrencyLimiter) initializeLocked() {
	if l.generations == nil {
		l.generations = make(map[string]*providerGeneration)
	}
}

func (l *ConcurrencyLimiter) newGenerationLocked(providerID string) *providerGeneration {
	l.nextGeneration++
	return &providerGeneration{
		providerID: providerID,
		number:     l.nextGeneration,
	}
}

func (l *ConcurrencyLimiter) releaseLease(state *slotLeaseState) bool {
	if l == nil || state == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.releaseLeaseLocked(state)
}

func (l *ConcurrencyLimiter) releaseWithTransition(lease *SlotLease, transition func() bool) bool {
	if l == nil || lease == nil || lease.state == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if transition != nil && !transition() {
		return false
	}
	return l.releaseLeaseLocked(lease.state)
}

func (l *ConcurrencyLimiter) releaseLeaseLocked(state *slotLeaseState) bool {
	if !state.released.CompareAndSwap(false, true) {
		return false
	}
	if state.counted {
		state.generation.active.Add(-1)
	}
	return true
}
