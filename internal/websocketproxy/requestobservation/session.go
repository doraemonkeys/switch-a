// Package requestobservation owns client-request facts across physical WebSocket attempts.
package requestobservation

import (
	"bytes"
	"context"
	"sync"

	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/requestingress/semantic"
)

// Snapshot describes the most recent logical response.create, not a provider replay.
type Snapshot struct {
	ClientRequestIndex uint64
	Reasoning          model.RequestedReasoningObservation
}

type Session struct {
	mu      sync.Mutex
	current Snapshot
}

func New(supported bool) *Session {
	state := model.ReasoningObservationUnsupported
	if supported {
		state = model.ReasoningObservationPending
	}
	return &Session{current: Snapshot{Reasoning: model.RequestedReasoningObservation{State: &state}}}
}

// ObserveResponseCreate must run once at client admission, before disguise or
// delivery. Replaying a physical transmission must not advance the logical request.
func (s *Session) ObserveResponseCreate(data []byte) Snapshot {
	// The complete message is already owned by the transport. Cancellation must
	// not discard observable fields from a request that has already arrived.
	result := semantic.Project(context.Background(), bytes.NewReader(data), semantic.Options{
		ReasoningContract: semantic.ReasoningCodex,
	})
	s.mu.Lock()
	defer s.mu.Unlock()
	s.current.ClientRequestIndex++
	// Replace the whole observation so omitted or invalid values cannot inherit
	// the previous turn's settings.
	s.current.Reasoning = result.Reasoning.Value
	return s.current
}

func (s *Session) Snapshot() Snapshot {
	if s == nil {
		return Snapshot{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.current
}
