package requestobservation

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/model"
)

func TestSessionReplacesRequestedReasoning(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		state      model.ReasoningObservationState
		effort     *string
	}{
		{"high", `{"reasoning":{"effort":"high"}}`, model.ReasoningObservationCaptured, text("high")},
		{"low", `{"reasoning":{"effort":"low"}}`, model.ReasoningObservationCaptured, text("low")},
		{"omitted", `{"input":[]}`, model.ReasoningObservationAbsent, nil},
		{"empty", `{"reasoning":{"effort":""}}`, model.ReasoningObservationCaptured, text("")},
		{"null", `{"reasoning":{"effort":null}}`, model.ReasoningObservationInvalid, nil},
		{"wrong type", `{"reasoning":{"effort":3}}`, model.ReasoningObservationInvalid, nil},
		{"duplicate", `{"reasoning":{"effort":"low","effort":"high"}}`, model.ReasoningObservationAmbiguous, text("high")},
		{"invalid duplicate", `{"reasoning":{"effort":"high","effort":false}}`, model.ReasoningObservationInvalid, nil},
		{"spelling", `{"reasoning":{"effort":" HIGH "}}`, model.ReasoningObservationCaptured, text(" HIGH ")},
		{"too long", fmt.Sprintf(`{"reasoning":{"effort":%q}}`, strings.Repeat("x", model.MaxReasoningValueRunes+1)), model.ReasoningObservationInvalid, nil},
		{"large input before effort", `{"input":[{"content":"` + strings.Repeat("x", 2*1024*1024) + `"}],"reasoning":{"effort":"high"}}`, model.ReasoningObservationCaptured, text("high")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := New(true)
			before := session.Snapshot()
			if before.ClientRequestIndex != 0 || *before.Reasoning.State != model.ReasoningObservationPending {
				t.Fatalf("initial = %+v", before)
			}
			first := session.ObserveResponseCreate([]byte(`{"reasoning":{"effort":"medium"}}`))
			got := session.ObserveResponseCreate([]byte(tc.body))
			if got.ClientRequestIndex != 2 || *got.Reasoning.State != tc.state {
				t.Fatalf("observation = %+v, state = %v", got, got.Reasoning.State)
			}
			if (got.Reasoning.Effort == nil) != (tc.effort == nil) || tc.effort != nil && *got.Reasoning.Effort != *tc.effort {
				t.Fatalf("effort = %v, want %v", got.Reasoning.Effort, tc.effort)
			}
			if first.ClientRequestIndex != 1 || *first.Reasoning.Effort != "medium" {
				t.Fatal("later requests mutated an earlier snapshot")
			}
			if *session.Snapshot().Reasoning.State != tc.state {
				t.Fatal("published and stored observations differ")
			}
		})
	}
}

func TestSessionUnsupportedAndNil(t *testing.T) {
	if got := New(false).Snapshot(); *got.Reasoning.State != model.ReasoningObservationUnsupported {
		t.Fatalf("unsupported = %+v", got)
	}
	var session *Session
	if got := session.Snapshot(); got.Reasoning.State != nil || got.ClientRequestIndex != 0 {
		t.Fatalf("nil session = %+v", got)
	}
}

func TestSessionConcurrentSnapshots(t *testing.T) {
	session := New(true)
	var wg sync.WaitGroup
	wg.Go(func() {
		for range 100 {
			session.ObserveResponseCreate([]byte(`{"reasoning":{"effort":"high"}}`))
		}
	})
	for range 100 {
		got := session.Snapshot()
		if got.ClientRequestIndex > 0 && (got.Reasoning.Effort == nil || *got.Reasoning.Effort != "high") {
			t.Fatalf("inconsistent snapshot = %+v", got)
		}
	}
	wg.Wait()
	if session.Snapshot().ClientRequestIndex != 100 {
		t.Fatal("lost request observations")
	}
}

func text(value string) *string { return &value }
