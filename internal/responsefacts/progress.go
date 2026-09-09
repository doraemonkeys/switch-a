// Package responsefacts records protocol events independently of connection and write outcomes.
package responsefacts

import "time"

const (
	ResponseCreate     = "response.create"
	ResponseCreated    = "response.created"
	ResponseCompleted  = "response.completed"
	ResponseDone       = "response.done"
	ResponseFailed     = "response.failed"
	ResponseIncomplete = "response.incomplete"
	StatusCompleted    = "completed"
)

type Response struct {
	Round      uint64    `json:"round"`
	ResponseID string    `json:"response_id,omitempty"`
	EventType  string    `json:"event_type,omitempty"`
	Status     string    `json:"status,omitempty"`
	ObservedAt time.Time `json:"observed_at,omitzero"`
}

// Completed describes the upstream event only; it says nothing about delivery.
func (r Response) Completed() bool {
	return (r.EventType == ResponseCompleted || r.EventType == ResponseDone) &&
		(r.Status == "" || r.Status == StatusCompleted)
}

type Progress struct {
	Current            Response `json:"current"`
	LastCompleted      Response `json:"last_completed,omitzero"`
	CompletedResponses uint64   `json:"completed_responses"`
}

// Tracker is owned by the semantic observer's lock. Response IDs keep late or
// duplicate events from completing a newer request waiting to be uploaded.
type Tracker struct {
	progress  Progress
	pending   []uint64
	rounds    map[string]uint64
	completed map[uint64]bool
}

func (t *Tracker) Snapshot() Progress { return t.progress }

func (t *Tracker) Observe(fromUpstream bool, eventType, responseID, status string, at time.Time) bool {
	if !fromUpstream {
		if eventType != ResponseCreate {
			return false
		}
		t.begin(at)
		return true
	}
	switch eventType {
	case ResponseCreated, ResponseCompleted, ResponseDone, ResponseFailed, ResponseIncomplete:
	default:
		return false
	}
	round := t.rounds[responseID]
	if responseID == "" || round == 0 {
		if len(t.pending) == 0 {
			// Some streams omit the ID on created, then supply it on the terminal.
			// Reuse that observed round instead of inventing another request.
			current := t.progress.Current
			if current.Round > 0 && eventType != ResponseCreated &&
				(responseID == "" || (current.ResponseID == "" && current.EventType == ResponseCreated)) {
				round = t.progress.Current.Round
			} else {
				t.begin(at)
			}
		}
		if round == 0 {
			round, t.pending = t.pending[0], t.pending[1:]
		}
		if responseID != "" {
			if t.rounds == nil {
				t.rounds = make(map[string]uint64)
			}
			t.rounds[responseID] = round
		}
	}
	response := Response{Round: round, ResponseID: responseID, EventType: eventType, Status: status, ObservedAt: at}
	if round == t.progress.Current.Round && responseID == "" {
		response.ResponseID = t.progress.Current.ResponseID
	}
	if response.Completed() && !t.completed[round] {
		if t.completed == nil {
			t.completed = make(map[uint64]bool)
		}
		t.completed[round] = true
		t.progress.CompletedResponses++
		t.progress.LastCompleted = response
	}
	if round != t.progress.Current.Round {
		return false
	}
	// A duplicate created event cannot undo a terminal observation.
	if eventType == ResponseCreated && isTerminal(t.progress.Current.EventType) {
		return false
	}
	t.progress.Current = response
	return true
}

func isTerminal(eventType string) bool {
	switch eventType {
	case ResponseCompleted, ResponseDone, ResponseFailed, ResponseIncomplete:
		return true
	default:
		return false
	}
}

func (t *Tracker) begin(at time.Time) {
	round := t.progress.Current.Round + 1
	t.progress.Current = Response{Round: round, ObservedAt: at}
	t.pending = append(t.pending, round)
}
