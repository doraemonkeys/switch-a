// Package allocation defines the memory-reservation capability used by
// response analysis without making the analysis packages own request budgets.
package allocation

import (
	"errors"
	"fmt"
)

// Class identifies why capacity is retained. The account implementation may
// use it for structured diagnostics, but all classes share the same request and
// process ceilings.
type Class string

const (
	ClassRawPrefix         Class = "raw_retained_prefix"
	ClassDecodedBuffer     Class = "decoded_buffer"
	ClassFramingBuffer     Class = "framing_buffer"
	ClassSemanticFields    Class = "semantic_fields"
	ClassChannelPayload    Class = "channel_payload"
	ClassDecoderWorkingSet Class = "decoder_working_set"
)

// DenialReason is intentionally wire-stable so the response coordinator can
// map a denial to the frozen fail-open fact without inspecting error text.
type DenialReason string

const (
	DenialRequestMemoryExhausted DenialReason = "request_probe_memory_exhausted"
	DenialProcessMemoryExhausted DenialReason = "process_probe_memory_exhausted"
)

var (
	ErrNilReserver = errors.New("allocation reserver is nil")
	ErrNilGrant    = errors.New("allocation reserver returned a nil grant")
)

// Grant owns one reservation. Release must be idempotent because frames are
// value types and defensive cleanup paths may observe copied ownership handles.
type Grant interface {
	Release()
}

// Reserver is implemented by the request-scoped account owned by the response
// coordinator. Capacity is the complete allocation capacity, never a growth
// delta, so callers can account the old and new buffers concurrently.
type Reserver interface {
	Reserve(class Class, capacity int) (Grant, error)
}

// Denial records which ceiling rejected a reservation while retaining enough
// context for structured diagnostics. RequestedCapacity is always the complete
// capacity of the proposed allocation.
type Denial struct {
	Reason            DenialReason
	Class             Class
	RequestedCapacity int
	Cause             error
}

func (e *Denial) Error() string {
	if e == nil {
		return "response-analysis allocation denied"
	}
	message := fmt.Sprintf("%s: class=%s capacity=%d", e.Reason, e.Class, e.RequestedCapacity)
	if e.Cause != nil {
		return message + ": " + e.Cause.Error()
	}
	return message
}

func (e *Denial) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// DenialReasonOf extracts only recognized denial reasons. Invalid or foreign
// errors cannot manufacture a stable fail-open fact.
func DenialReasonOf(err error) (DenialReason, bool) {
	var denial *Denial
	if !errors.As(err, &denial) {
		return "", false
	}
	switch denial.Reason {
	case DenialRequestMemoryExhausted, DenialProcessMemoryExhausted:
		return denial.Reason, true
	default:
		return "", false
	}
}

// NoopReserver is the explicit unaccounted implementation used by transitional
// callers and narrow unit tests. Production response coordination supplies its
// request account through Reserver instead.
type NoopReserver struct{}

func (NoopReserver) Reserve(Class, int) (Grant, error) {
	return noopGrant{}, nil
}

type noopGrant struct{}

func (noopGrant) Release() {}
