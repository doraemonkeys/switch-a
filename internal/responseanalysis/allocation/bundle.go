package allocation

import "errors"

// BundleGrantCapacity covers four normalized semantic strings, their
// SemanticFields owner, and TokenUsage, CacheCreation, and service-tier owners.
// Keeping the array inline makes grant grouping allocation-free.
const BundleGrantCapacity = 8

var (
	ErrBundleFull      = errors.New("allocation grant bundle is full")
	ErrNilBundleTarget = errors.New("allocation grant bundle target is nil")
)

// Bundle is a fixed-capacity, move-oriented owner for related grants. Its zero
// value is ready for use. Copies are cleanup-safe because Grant requires an
// idempotent Release, but callers should use Take or MoveTo to express ownership.
type Bundle struct {
	grants [BundleGrantCapacity]Grant
	count  uint8
}

// Add transfers one grant into the bundle. A nil optional grant needs no slot.
// On capacity failure the caller retains ownership of grant.
func (b *Bundle) Add(grant Grant) error {
	if grant == nil {
		return nil
	}
	if b == nil || int(b.count) == len(b.grants) {
		return ErrBundleFull
	}
	b.grants[b.count] = grant
	b.count++
	return nil
}

// Len reports the number of grants currently owned by the bundle.
func (b *Bundle) Len() int {
	if b == nil {
		return 0
	}
	return int(b.count)
}

// MoveTo appends all grants to destination and clears the source only after
// capacity validation, so a failed move leaves both ownership sets unchanged.
func (b *Bundle) MoveTo(destination *Bundle) error {
	if destination == nil {
		return ErrNilBundleTarget
	}
	if b == nil || b == destination || b.count == 0 {
		return nil
	}
	if destination.Len()+b.Len() > BundleGrantCapacity {
		return ErrBundleFull
	}
	for index := uint8(0); index < b.count; index++ {
		destination.grants[destination.count] = b.grants[index]
		destination.count++
		b.grants[index] = nil
	}
	b.count = 0
	return nil
}

// Take moves all ownership into a returned value and resets the receiver.
func (b *Bundle) Take() Bundle {
	if b == nil {
		return Bundle{}
	}
	taken := *b
	*b = Bundle{}
	return taken
}

// Release relinquishes every grant exactly once for this owner and clears each
// slot before invoking external cleanup, making reentrant and repeated release
// paths safe.
func (b *Bundle) Release() {
	if b == nil {
		return
	}
	for index := uint8(0); index < b.count; index++ {
		grant := b.grants[index]
		b.grants[index] = nil
		if grant != nil {
			grant.Release()
		}
	}
	b.count = 0
}
