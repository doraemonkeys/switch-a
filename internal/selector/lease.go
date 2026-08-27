package selector

import (
	"reflect"
	"sync/atomic"

	"github.com/doraemonkeys/switch-a/internal/codex/identity"
	"github.com/doraemonkeys/switch-a/internal/model"
)

type providerGeneration struct {
	providerID string
	number     uint64
	active     atomic.Int64
}

type slotLeaseState struct {
	generation *providerGeneration
	owner      *ConcurrencyLimiter
	counted    bool
	released   atomic.Bool
}

// SlotLease is a copy-safe capability for one exact provider generation. Copies
// share release state, so only the first Release owns the counter transition.
type SlotLease struct {
	state *slotLeaseState
}

// Release relinquishes this exact slot once. It returns true only for the caller
// that won ownership of the release transition.
func (l *SlotLease) Release() bool {
	if l == nil || l.state == nil {
		return false
	}
	if l.state.owner != nil {
		return l.state.owner.releaseLease(l.state)
	}
	if !l.state.released.CompareAndSwap(false, true) {
		return false
	}
	if l.state.counted {
		l.state.generation.active.Add(-1)
	}
	return true
}

// Held reports whether the capability still owns its slot.
func (l *SlotLease) Held() bool {
	return l != nil && l.state != nil && !l.state.released.Load()
}

// ProviderID returns the provider identity captured by this lifecycle generation.
func (l *SlotLease) ProviderID() string {
	if l == nil || l.state == nil || l.state.generation == nil {
		return ""
	}
	return l.state.generation.providerID
}

// Generation returns the process-local lifecycle generation captured at acquire.
func (l *SlotLease) Generation() uint64 {
	if l == nil || l.state == nil || l.state.generation == nil {
		return 0
	}
	return l.state.generation.number
}

type providerLeaseState struct {
	provider               atomic.Pointer[model.Provider]
	candidate              codexidentity.CandidateSnapshot
	candidateResolved      bool
	slot                   SlotLease
	dispatchPermitReserved atomic.Bool
}

// ProviderLease binds a selected provider snapshot to the exact slot capability
// that authorizes its dispatch and cleanup.
type ProviderLease struct {
	state *providerLeaseState
}

func newProviderLease(provider *model.Provider, slot *SlotLease) *ProviderLease {
	return newProviderLeaseWithCandidate(provider, slot, codexidentity.CandidateSnapshot{}, false)
}

func newProviderLeaseWithCandidate(
	provider *model.Provider,
	slot *SlotLease,
	candidate codexidentity.CandidateSnapshot,
	resolved bool,
) *ProviderLease {
	if provider == nil || slot == nil {
		return nil
	}
	state := &providerLeaseState{
		slot:              *slot,
		candidate:         candidate,
		candidateResolved: resolved,
	}
	state.provider.Store(provider)
	return &ProviderLease{state: state}
}

// Provider returns the live provider snapshot associated with this capability.
// Reservations replace the snapshot only while they still own activation.
func (l *ProviderLease) Provider() *model.Provider {
	if l == nil || l.state == nil {
		return nil
	}
	return l.state.provider.Load()
}

// CandidateSnapshot returns the immutable route/session identity resolved at
// selection time. Authentication consumers must use this snapshot instead of
// re-reading Provider credential projections.
func (l *ProviderLease) CandidateSnapshot() (codexidentity.CandidateSnapshot, bool) {
	if l == nil || l.state == nil || !l.state.candidateResolved {
		return codexidentity.CandidateSnapshot{}, false
	}
	return l.state.candidate, true
}

func (l *ProviderLease) replaceProvider(provider *model.Provider) {
	if l == nil || l.state == nil || provider == nil {
		return
	}
	l.state.provider.Store(provider)
}

// Slot returns the underlying copy-safe cleanup capability.
func (l *ProviderLease) Slot() *SlotLease {
	if l == nil || l.state == nil {
		return nil
	}
	return &l.state.slot
}

// Release relinquishes the provider's exact concurrency slot once.
func (l *ProviderLease) Release() bool {
	if l == nil || l.state == nil {
		return false
	}
	return l.state.slot.Release()
}

// Held reports whether this provider capability still owns its slot.
func (l *ProviderLease) Held() bool {
	return l != nil && l.state != nil && l.state.slot.Held()
}

// ProviderID derives identity from the slot generation rather than a mutable
// provider snapshot.
func (l *ProviderLease) ProviderID() string {
	if l == nil || l.state == nil {
		return ""
	}
	return l.state.slot.ProviderID()
}

// Generation returns the captured provider lifecycle generation.
func (l *ProviderLease) Generation() uint64 {
	if l == nil || l.state == nil {
		return 0
	}
	return l.state.slot.Generation()
}

// CapabilityIdentity returns an opaque, process-local identity for this exact
// lease. Copies share the same identity; separately acquired slots in one
// provider generation do not.
func (l *ProviderLease) CapabilityIdentity() uintptr {
	if l == nil || l.state == nil {
		return 0
	}
	return reflect.ValueOf(l.state).Pointer()
}
