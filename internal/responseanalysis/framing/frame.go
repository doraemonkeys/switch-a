// Package framing incrementally turns decoded HTTP response bytes into bounded
// protocol events. It deliberately knows nothing about provider semantics.
package framing

import (
	"errors"
	"fmt"

	"github.com/doraemonkeys/switch-a/internal/responseanalysis/allocation"
)

// Kind identifies the wire framing selected by the response media type.
type Kind uint8

const (
	KindJSON Kind = iota + 1
	KindSSE
)

// FailureReason is stable evidence consumed by the response-analysis layer.
type FailureReason string

const (
	FailureUnsupportedEncoding   FailureReason = "unsupported_content_encoding"
	FailureContentDecoding       FailureReason = "content_decoding_failed"
	FailureMalformedFrame        FailureReason = "malformed_protocol_frame"
	FailureDecodedEventTooLarge  FailureReason = "decoded_event_too_large"
	FailureSemanticFieldTooLarge FailureReason = "semantic_field_too_large"
	FailureInternal              FailureReason = "analysis_internal_error"
)

// Error preserves a stable external reason without discarding the diagnostic
// cause needed by structured runtime logs.
type Error struct {
	Reason FailureReason
	Cause  error
}

func (e *Error) Error() string {
	if e == nil {
		return "response framing failure"
	}
	if e.Cause == nil {
		return string(e.Reason)
	}
	return fmt.Sprintf("%s: %v", e.Reason, e.Cause)
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// ReasonOf separates stable classification from concrete decoder/parser errors.
func ReasonOf(err error) FailureReason {
	var framingError *Error
	if errors.As(err, &framingError) && framingError.Reason != "" {
		return framingError.Reason
	}
	return FailureInternal
}

// Frame is one atomic JSON response candidate or one parsed SSE event. JSON
// syntax belongs to the bounded semantic scanner so framing does not allocate
// hidden parser state. Data ownership transfers and is never reused.
type Frame struct {
	Event string
	Data  []byte
	Done  bool

	eventGrant allocation.Grant
	dataGrant  allocation.Grant
}

// Release relinquishes all capacity transferred with the frame. Callers must
// treat Frame as move-only after release; repeated cleanup remains safe because
// grants are idempotent and the local handles are cleared first.
func (f *Frame) Release() {
	if f == nil {
		return
	}
	eventGrant, dataGrant := f.eventGrant, f.dataGrant
	f.Event = ""
	f.Data = nil
	f.Done = false
	f.eventGrant = nil
	f.dataGrant = nil
	if eventGrant != nil {
		eventGrant.Release()
	}
	if dataGrant != nil {
		dataGrant.Release()
	}
}

// ReleaseFrames is the failure-safe cleanup path for a batch whose ownership
// was not transferred onward.
func ReleaseFrames(frames []Frame) {
	for index := range frames {
		frames[index].Release()
	}
}

// Framer is deliberately incremental so one implementation serves arbitrary
// network read boundaries and deterministic split-point tests. The returned
// Batch must be released even when err is non-nil because it may own frames that
// completed before a later event failed.
type Framer interface {
	Feed(chunk []byte, eof bool) (Batch, error)
}

// Stream is the concrete protocol-framing dispatcher returned by New. Keeping
// the variant behind this struct follows the return-struct rule without making
// callers own a producer-defined interface value.
type Stream struct {
	framer Framer
}

func (s *Stream) Feed(chunk []byte, eof bool) (Batch, error) {
	if s == nil || s.framer == nil {
		return Batch{}, &Error{Reason: FailureInternal, Cause: errors.New("framing stream is nil")}
	}
	return s.framer.Feed(chunk, eof)
}

// Release abandons any partial event retained by the selected framer. It is
// separate from EOF because a coordinator may stop semantic analysis while it
// continues forwarding the raw response body.
func (s *Stream) Release() {
	if s == nil || s.framer == nil {
		return
	}
	framer := s.framer
	// Stream relinquishes its handle before invoking variant cleanup so a grant
	// callback cannot reenter Release and observe the same owner twice.
	s.framer = nil
	if releaser, ok := framer.(interface{ Release() }); ok {
		releaser.Release()
	}
}

// New returns a transitional unaccounted stream. Production response handling
// uses NewWithReserver so every retained capacity has a request-owned grant.
func New(kind Kind, maxEventBytes int) *Stream {
	return newStream(kind, maxEventBytes, allocation.NoopReserver{})
}

func NewWithReserver(kind Kind, maxEventBytes int, reserver allocation.Reserver) (*Stream, error) {
	if reserver == nil {
		return nil, &Error{Reason: FailureInternal, Cause: allocation.ErrNilReserver}
	}
	return newStream(kind, maxEventBytes, reserver), nil
}

func newStream(kind Kind, maxEventBytes int, reserver allocation.Reserver) *Stream {
	switch kind {
	case KindJSON:
		return &Stream{framer: newJSON(maxEventBytes, reserver)}
	case KindSSE:
		return &Stream{framer: newSSE(maxEventBytes, reserver)}
	default:
		return &Stream{framer: &invalidFramer{}}
	}
}

type invalidFramer struct{}

func (*invalidFramer) Feed([]byte, bool) (Batch, error) {
	return Batch{}, &Error{Reason: FailureInternal, Cause: errors.New("unknown framing kind")}
}
