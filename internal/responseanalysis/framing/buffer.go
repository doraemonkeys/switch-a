package framing

import (
	"errors"

	"github.com/doraemonkeys/switch-a/internal/responseanalysis/allocation"
)

const (
	initialOwnedBufferCapacity        = 64
	largeOwnedBufferCapacityThreshold = 64 << 10
	largeOwnedBufferGrowthBlock       = 4 << 10
)

var (
	errOwnedBufferLimit          = errors.New("owned buffer capacity limit exceeded")
	errOwnedBufferMove           = errors.New("owned buffer move is invalid")
	errOwnedBufferMoveTargetBusy = errors.New("owned buffer move target already owns storage")
)

// ownedBuffer keeps the reservation and allocation in one ownership unit. A
// replacement allocation is fully reserved while the old grant remains live,
// which accounts the real realloc peak instead of charging only the delta.
type ownedBuffer struct {
	data     []byte
	grant    allocation.Grant
	reserver allocation.Reserver
	class    allocation.Class
}

func newOwnedBuffer(reserver allocation.Reserver, class allocation.Class) ownedBuffer {
	return ownedBuffer{reserver: reserver, class: class}
}

func (b *ownedBuffer) bytes() []byte {
	return b.data
}

func (b *ownedBuffer) reset() {
	b.data = b.data[:0]
}

func (b *ownedBuffer) appendByte(value byte, maxCapacity int) error {
	return b.appendBytes([]byte{value}, maxCapacity)
}

func (b *ownedBuffer) appendBytes(value []byte, maxCapacity int) error {
	if len(value) == 0 {
		return nil
	}
	if maxCapacity < 0 || len(b.data) > maxCapacity || len(value) > maxCapacity-len(b.data) {
		return errOwnedBufferLimit
	}
	required := len(b.data) + len(value)
	if required <= cap(b.data) {
		b.data = append(b.data, value...)
		return nil
	}
	if b.reserver == nil {
		return allocation.ErrNilReserver
	}

	capacity := nextOwnedCapacity(cap(b.data), required, maxCapacity)
	if capacity < required {
		return errOwnedBufferLimit
	}
	grant, err := b.reserver.Reserve(b.class, capacity)
	if err != nil {
		return err
	}
	if grant == nil {
		return allocation.ErrNilGrant
	}

	next := make([]byte, len(b.data), capacity)
	copy(next, b.data)
	next = append(next, value...)
	previous := b.grant
	b.data = next
	b.grant = grant
	if previous != nil {
		previous.Release()
	}
	return nil
}

// appendByteAndBytes reserves the complete replacement before mutating the
// buffer. SSE multiline assembly needs this atomic form because value may still
// be owned by the line buffer when a larger data allocation is requested.
func (b *ownedBuffer) appendByteAndBytes(first byte, value []byte, maxCapacity int) error {
	if maxCapacity < 0 || len(b.data) >= maxCapacity || len(value) > maxCapacity-len(b.data)-1 {
		return errOwnedBufferLimit
	}
	required := len(b.data) + 1 + len(value)
	if required <= cap(b.data) {
		b.data = append(b.data, first)
		b.data = append(b.data, value...)
		return nil
	}
	if b.reserver == nil {
		return allocation.ErrNilReserver
	}

	capacity := nextOwnedCapacity(cap(b.data), required, maxCapacity)
	if capacity < required {
		return errOwnedBufferLimit
	}
	grant, err := b.reserver.Reserve(b.class, capacity)
	if err != nil {
		return err
	}
	if grant == nil {
		return allocation.ErrNilGrant
	}

	next := make([]byte, len(b.data), capacity)
	copy(next, b.data)
	next = append(next, first)
	next = append(next, value...)
	previous := b.grant
	b.data = next
	b.grant = grant
	if previous != nil {
		previous.Release()
	}
	return nil
}

func nextOwnedCapacity(current, required, maximum int) int {
	if required > largeOwnedBufferCapacityThreshold {
		return blockRoundedOwnedCapacity(required, maximum)
	}
	capacity := min(max(current, initialOwnedBufferCapacity), maximum)
	for capacity < required {
		if capacity > maximum/2 {
			capacity = maximum
			break
		}
		capacity *= 2
	}
	return capacity
}

// blockRoundedOwnedCapacity bounds the real old-plus-new replacement peak for
// large framing buffers. Continuing geometric growth above 64 KiB would
// reserve a 128-KiB replacement for a roughly 72-KiB SSE event while the
// 64-KiB owner is still live; small fixed blocks avoid per-byte replacements
// without that overshoot.
func blockRoundedOwnedCapacity(required, maximum int) int {
	if required >= maximum {
		return maximum
	}
	remainder := required % largeOwnedBufferGrowthBlock
	if remainder == 0 {
		return required
	}
	growth := largeOwnedBufferGrowthBlock - remainder
	if growth > maximum-required {
		return maximum
	}
	return required + growth
}

func (b *ownedBuffer) transfer() ([]byte, allocation.Grant) {
	data, grant := b.data, b.grant
	b.data = nil
	b.grant = nil
	return data, grant
}

// moveCompactedTo transfers the allocation without creating a second retained
// capacity. Compacting the selected range to offset zero keeps cap(data) equal
// to the charged allocation, which makes later growth and accounting exact.
func (b *ownedBuffer) moveCompactedTo(destination *ownedBuffer, start, end int) error {
	if b == nil || destination == nil || b == destination ||
		start < 0 || end < start || end > len(b.data) || b.class != destination.class {
		return errOwnedBufferMove
	}
	if len(destination.data) != 0 || destination.grant != nil {
		return errOwnedBufferMoveTargetBusy
	}

	length := end - start
	copy(b.data[:length], b.data[start:end])
	b.data = b.data[:length]
	destination.data = b.data
	destination.grant = b.grant
	b.data = nil
	b.grant = nil
	return nil
}

func (b *ownedBuffer) release() {
	grant := b.grant
	b.data = nil
	b.grant = nil
	if grant != nil {
		grant.Release()
	}
}

// ownedText accounts immutable framing strings separately from their mutable
// line buffer. The old value remains charged until the replacement string has
// been allocated, matching the peak behavior of overwrite events.
type ownedText struct {
	value    string
	grant    allocation.Grant
	reserver allocation.Reserver
	class    allocation.Class
}

func newOwnedText(reserver allocation.Reserver, class allocation.Class) ownedText {
	return ownedText{reserver: reserver, class: class}
}

func (t *ownedText) replace(value []byte) error {
	if len(value) == 0 {
		t.release()
		return nil
	}
	if t.reserver == nil {
		return allocation.ErrNilReserver
	}
	grant, err := t.reserver.Reserve(t.class, len(value))
	if err != nil {
		return err
	}
	if grant == nil {
		return allocation.ErrNilGrant
	}

	next := string(value)
	previous := t.grant
	t.value = next
	t.grant = grant
	if previous != nil {
		previous.Release()
	}
	return nil
}

func (t *ownedText) transfer() (string, allocation.Grant) {
	value, grant := t.value, t.grant
	t.value = ""
	t.grant = nil
	return value, grant
}

func (t *ownedText) release() {
	grant := t.grant
	t.value = ""
	t.grant = nil
	if grant != nil {
		grant.Release()
	}
}
