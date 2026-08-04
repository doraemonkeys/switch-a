package pending

import (
	"errors"
	"fmt"
	"sync"

	"github.com/doraemonkeys/switch-a/internal/responseanalysis/allocation"
)

var (
	ErrAccountClosed                  = errors.New("response analysis memory account is closed")
	ErrInvalidGzipDecoderWorkingSet   = errors.New("gzip decoder working-set reservation must use the fixed capacity")
	ErrGzipDecoderWorkingSetExhausted = errors.New("gzip decoder working-set reservation is already consumed")
)

type ProcessBudget struct {
	mu       sync.Mutex
	limit    int
	used     int
	peak     int
	accounts map[*requestAccount]struct{}
}

func NewProcessBudget(limit int) (*ProcessBudget, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("process memory limit must be positive")
	}
	return &ProcessBudget{limit: limit, accounts: make(map[*requestAccount]struct{})}, nil
}

func (b *ProcessBudget) Limit() int {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.limit
}

func (b *ProcessBudget) Used() int {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.used
}

func (b *ProcessBudget) Peak() int {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.peak
}

type requestAccount struct {
	process                    *ProcessBudget
	limit                      int
	used                       int
	peak                       int
	closed                     bool
	gzipDecoderWorksetConsumed bool
	grants                     map[*grantState]struct{}
}

const (
	maxRequestMemoryLimit      = 1024 * 1024
	gzipDecoderWorkingSetBytes = 256 * 1024
)

func newRequestAccount(process *ProcessBudget, limit int) (*requestAccount, error) {
	if process == nil {
		return nil, fmt.Errorf("process memory budget is required")
	}
	if limit <= 0 {
		return nil, fmt.Errorf("request memory limit must be positive")
	}
	if limit > maxRequestMemoryLimit {
		return nil, fmt.Errorf("request memory limit cannot exceed %d bytes", maxRequestMemoryLimit)
	}
	account := &requestAccount{
		process: process,
		limit:   limit,
		grants:  make(map[*grantState]struct{}),
	}
	process.mu.Lock()
	process.accounts[account] = struct{}{}
	process.mu.Unlock()
	return account, nil
}

func (a *requestAccount) Reserve(class allocation.Class, capacity int) (allocation.Grant, error) {
	if a == nil || a.process == nil {
		return nil, ErrAccountClosed
	}
	if capacity < 0 && class != allocation.ClassDecoderWorkingSet {
		return nil, fmt.Errorf("allocation capacity cannot be negative")
	}
	if capacity == 0 && class != allocation.ClassDecoderWorkingSet {
		return emptyGrant{}, nil
	}

	process := a.process
	process.mu.Lock()
	defer process.mu.Unlock()
	return a.reserveLocked(class, capacity)
}

// reserveUpTo atomically chooses a bounded capacity without a check-then-reserve
// race. Raw-prefix blocks use it so arbitrarily small upstream reads cannot
// create unbounded per-read allocation metadata near a memory ceiling.
func (a *requestAccount) reserveUpTo(class allocation.Class, preferred, minimum int) (allocation.Grant, int, error) {
	if a == nil || a.process == nil {
		return nil, 0, ErrAccountClosed
	}
	if preferred <= 0 || minimum <= 0 || minimum > preferred {
		return nil, 0, fmt.Errorf("invalid allocation capacity range [%d,%d]", minimum, preferred)
	}
	process := a.process
	process.mu.Lock()
	defer process.mu.Unlock()
	if a.closed {
		return nil, 0, ErrAccountClosed
	}
	if class == allocation.ClassDecoderWorkingSet {
		return nil, 0, fmt.Errorf("%w: variable-capacity reservation is not permitted", ErrInvalidGzipDecoderWorkingSet)
	}

	capacity := min(preferred, process.limit-process.used)
	capacity = min(capacity, a.limit-a.used)
	if capacity < minimum {
		_, err := a.reserveLocked(class, minimum)
		return nil, 0, err
	}
	reserved, err := a.reserveLocked(class, capacity)
	return reserved, capacity, err
}

func (a *requestAccount) reserveLocked(class allocation.Class, capacity int) (allocation.Grant, error) {
	if a.closed {
		return nil, ErrAccountClosed
	}

	requestCharged := true
	if class == allocation.ClassDecoderWorkingSet {
		if capacity != gzipDecoderWorkingSetBytes {
			return nil, fmt.Errorf(
				"%w: got %d bytes, want %d",
				ErrInvalidGzipDecoderWorkingSet,
				capacity,
				gzipDecoderWorkingSetBytes,
			)
		}
		if a.gzipDecoderWorksetConsumed {
			return nil, ErrGzipDecoderWorkingSetExhausted
		}
		// A request owns one gzip decoder, so this one-shot claim is the narrow
		// process-only analogue of fixed scratch. Keeping the claim consumed after
		// release prevents sequential decoders from turning a fixed exception into
		// an arbitrary working-memory bypass.
		requestCharged = false
	}
	if requestCharged && capacity > a.limit-a.used {
		return nil, &allocation.Denial{
			Reason:            allocation.DenialRequestMemoryExhausted,
			Class:             class,
			RequestedCapacity: capacity,
		}
	}
	if capacity > a.process.limit-a.process.used {
		return nil, &allocation.Denial{
			Reason:            allocation.DenialProcessMemoryExhausted,
			Class:             class,
			RequestedCapacity: capacity,
		}
	}
	if !requestCharged {
		a.gzipDecoderWorksetConsumed = true
	}

	state := &grantState{account: a, capacity: capacity, requestCharged: requestCharged}
	a.grants[state] = struct{}{}
	if requestCharged {
		a.used += capacity
		a.peak = max(a.peak, a.used)
	}
	a.process.used += capacity
	a.process.peak = max(a.process.peak, a.process.used)
	return &grant{state: state}, nil
}

func (a *requestAccount) close() {
	if a == nil || a.process == nil {
		return
	}
	process := a.process
	process.mu.Lock()
	defer process.mu.Unlock()
	if a.closed {
		return
	}
	a.closed = true
	for state := range a.grants {
		state.released = true
		process.used -= state.capacity
		delete(a.grants, state)
	}
	a.used = 0
	delete(process.accounts, a)
}

func (a *requestAccount) snapshot() (used, peak int) {
	if a == nil || a.process == nil {
		return 0, 0
	}
	a.process.mu.Lock()
	defer a.process.mu.Unlock()
	return a.used, a.peak
}

type grantState struct {
	account        *requestAccount
	capacity       int
	requestCharged bool
	released       bool
}

type grant struct {
	state *grantState
}

func (g *grant) Release() {
	if g == nil || g.state == nil || g.state.account == nil || g.state.account.process == nil {
		return
	}
	state := g.state
	process := state.account.process
	process.mu.Lock()
	defer process.mu.Unlock()
	if state.released {
		return
	}
	state.released = true
	delete(state.account.grants, state)
	if state.requestCharged {
		state.account.used -= state.capacity
	}
	process.used -= state.capacity
}

type emptyGrant struct{}

func (emptyGrant) Release() {}
