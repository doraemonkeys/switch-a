package upstreamtransport

import (
	"errors"
	"io"
	"sync"
)

const transformSnippetBytes = 512

// TransformationError retains physical source/derived fragments at a codec
// failure without requiring the sender to retain another whole representation.
type TransformationError struct {
	Stage           string
	OriginalSnippet string
	DerivedSnippet  string
	Cause           error
}

func (e *TransformationError) Error() string { return e.Cause.Error() }
func (e *TransformationError) Unwrap() error { return e.Cause }

type transformIOObservation struct {
	mu      sync.Mutex
	failure error
	recent  []byte
}

func (o *transformIOObservation) record(data []byte, err error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if err != nil && !errors.Is(err, io.EOF) {
		o.failure = err
	}
	if len(data) >= transformSnippetBytes {
		o.recent = append(o.recent[:0], data[len(data)-transformSnippetBytes:]...)
		return
	}
	if excess := len(o.recent) + len(data) - transformSnippetBytes; excess > 0 {
		o.recent = append(o.recent[:0], o.recent[excess:]...)
	}
	o.recent = append(o.recent, data...)
}
func (o *transformIOObservation) snapshot() (string, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return string(o.recent), o.failure
}
