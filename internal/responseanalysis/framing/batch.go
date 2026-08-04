package framing

import (
	"errors"
	"math"
	"unsafe"

	"github.com/doraemonkeys/switch-a/internal/responseanalysis/allocation"
)

const frameSlotBytes = int(unsafe.Sizeof(Frame{}))

var (
	errBatchCapacityOverflow = errors.New("frame batch capacity overflows allocation accounting")
	errNilBatch              = errors.New("frame batch is nil")
	errNilBatchFrame         = errors.New("frame batch append source is nil")
)

// Batch move-owns both the emitted frames and the backing array that retains
// them. Frames is exposed for immediate borrowed iteration; callers that need a
// frame beyond Batch.Release must Take it explicitly.
type Batch struct {
	Frames []Frame

	grant    allocation.Grant
	reserver allocation.Reserver
}

func newBatch(reserver allocation.Reserver) Batch {
	return Batch{reserver: reserver}
}

// Take transfers one frame's payload grants out of the batch. The backing-array
// reservation remains with the batch until Release because the slice still
// occupies its complete allocated capacity.
func (b *Batch) Take(index int) (Frame, bool) {
	if b == nil || index < 0 || index >= len(b.Frames) {
		return Frame{}, false
	}
	frame := b.Frames[index]
	b.Frames[index] = Frame{}
	return frame, true
}

// Release relinquishes every frame not moved with Take, followed by the backing
// array grant. Clearing ownership before external cleanup makes repeated and
// reentrant cleanup paths harmless.
func (b *Batch) Release() {
	if b == nil {
		return
	}
	frames, grant := b.Frames, b.grant
	b.Frames = nil
	b.grant = nil
	b.reserver = nil
	for index := range frames {
		frames[index].Release()
	}
	if grant != nil {
		grant.Release()
	}
}

func (b *Batch) append(frame *Frame) error {
	if b == nil {
		return errNilBatch
	}
	if frame == nil {
		return errNilBatchFrame
	}
	if len(b.Frames) == cap(b.Frames) {
		if err := b.grow(len(b.Frames) + 1); err != nil {
			return err
		}
	}
	b.Frames = append(b.Frames, *frame)
	*frame = Frame{}
	return nil
}

func (b *Batch) grow(required int) error {
	if b.reserver == nil {
		return allocation.ErrNilReserver
	}
	capacity, err := nextBatchCapacity(cap(b.Frames), required)
	if err != nil {
		return err
	}
	reservedBytes := capacity * frameSlotBytes
	grant, err := b.reserver.Reserve(allocation.ClassChannelPayload, reservedBytes)
	if err != nil {
		return err
	}
	if grant == nil {
		return allocation.ErrNilGrant
	}

	next := make([]Frame, len(b.Frames), capacity)
	copy(next, b.Frames)
	previous := b.grant
	b.Frames = next
	b.grant = grant
	if previous != nil {
		previous.Release()
	}
	return nil
}

func nextBatchCapacity(current, required int) (int, error) {
	maximum := math.MaxInt / frameSlotBytes
	if current < 0 || required <= 0 || current > maximum || required > maximum {
		return 0, errBatchCapacityOverflow
	}
	if required <= current {
		return current, nil
	}
	capacity := current
	if capacity == 0 {
		capacity = 1
	}
	for capacity < required {
		if capacity > math.MaxInt/2 {
			capacity = required
			break
		}
		capacity *= 2
	}
	if capacity > maximum {
		capacity = required
	}
	return capacity, nil
}
