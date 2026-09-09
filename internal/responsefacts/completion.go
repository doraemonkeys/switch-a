package responsefacts

import (
	"sync"
	"time"
)

type Completion struct {
	EventType  string    `json:"event_type"`
	ObservedAt time.Time `json:"observed_at"`
}

// CompletionObserver survives cancellation of the downstream writer. It records
// only decoded upstream protocol events, never an inference from a closed socket.
type CompletionObserver struct {
	mu    sync.Mutex
	value Completion
}

func (o *CompletionObserver) Observe(event string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.value = Completion{EventType: event, ObservedAt: time.Now()}
}

func (o *CompletionObserver) Snapshot() Completion {
	if o == nil {
		return Completion{}
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.value
}
