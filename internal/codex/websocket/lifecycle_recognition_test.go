package codexws

import (
	"context"
	"net/http"
	"sync"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/codex/continuity"
)

type lifecycleRecordingContinuity struct {
	Continuity
	mu            sync.Mutex
	activations   int
	deactivations int
}

func (c *lifecycleRecordingContinuity) ActivateResponse(
	generation codexcontinuity.Generation,
	lease codexcontinuity.Lease,
) error {
	if err := c.Continuity.ActivateResponse(generation, lease); err != nil {
		return err
	}
	c.mu.Lock()
	c.activations++
	c.mu.Unlock()
	return nil
}

func (c *lifecycleRecordingContinuity) DeactivateResponse(
	generation codexcontinuity.Generation,
	binding codexcontinuity.Binding,
) error {
	if err := c.Continuity.DeactivateResponse(generation, binding); err != nil {
		return err
	}
	c.mu.Lock()
	c.deactivations++
	c.mu.Unlock()
	return nil
}

func (c *lifecycleRecordingContinuity) counts() (int, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.activations, c.deactivations
}

func TestConfirmedTerminalEventsDeactivateOnlyAfterSuccessfulWriteCommit(t *testing.T) {
	for _, terminalEvent := range []string{
		"response.completed",
		"response.incomplete",
		"response.failed",
	} {
		t.Run(terminalEvent, func(t *testing.T) {
			service := &lifecycleRecordingContinuity{Continuity: newTestContinuity(t)}
			runtime := testRuntime(t, service)
			operationID := "terminal-" + terminalEvent
			op, err := runtime.Begin(context.Background(), testRequest("terminal-client"), codexAPIType, operationID)
			if err != nil {
				t.Fatal(err)
			}
			candidate, applied := testCandidate(t, "route-a", "https://api.example.test/v1")
			if _, err := op.PrepareDial(context.Background(), make(http.Header), candidate, applied, mustURL(t, "wss://api.example.test/v1")); err != nil {
				t.Fatal(err)
			}
			if err := op.OpenConnection(); err != nil {
				t.Fatal(err)
			}
			defer op.CloseConnection()
			defer op.DiscardCookies()

			responseID := "response-" + terminalEvent
			created := []byte(`{"type":"response.created","response":{"id":"` + responseID + `"}}`)
			createdPermit, err := op.PrepareServerFrame(context.Background(), true, created)
			if err != nil {
				t.Fatal(err)
			}
			if err := createdPermit.Commit(context.Background()); err != nil {
				t.Fatal(err)
			}
			if activations, deactivations := service.counts(); activations != 1 || deactivations != 0 {
				t.Fatalf("created lifecycle calls = activate:%d deactivate:%d", activations, deactivations)
			}

			terminal := []byte(`{"type":"` + terminalEvent + `","response":{"id":"` + responseID + `"}}`)
			terminalPermit, err := op.PrepareServerFrame(context.Background(), true, terminal)
			if err != nil {
				t.Fatal(err)
			}
			if len(terminalPermit.deactivate) != 1 {
				t.Fatalf("terminal deactivation count = %d", len(terminalPermit.deactivate))
			}
			if activations, deactivations := service.counts(); activations != 1 || deactivations != 0 {
				t.Fatalf("pre-commit lifecycle calls = activate:%d deactivate:%d", activations, deactivations)
			}
			if err := terminalPermit.Commit(context.Background()); err != nil {
				t.Fatal(err)
			}
			if activations, deactivations := service.counts(); activations != 1 || deactivations != 1 {
				t.Fatalf("committed lifecycle calls = activate:%d deactivate:%d", activations, deactivations)
			}
		})
	}
}

func TestUnknownFutureLifecycleEventDoesNotDeactivateResponse(t *testing.T) {
	service := &lifecycleRecordingContinuity{Continuity: newTestContinuity(t)}
	runtime := testRuntime(t, service)
	op, err := runtime.Begin(context.Background(), testRequest("future-client"), codexAPIType, "future-lifecycle")
	if err != nil {
		t.Fatal(err)
	}
	candidate, applied := testCandidate(t, "route-a", "https://api.example.test/v1")
	if _, err := op.PrepareDial(context.Background(), make(http.Header), candidate, applied, mustURL(t, "wss://api.example.test/v1")); err != nil {
		t.Fatal(err)
	}
	if err := op.OpenConnection(); err != nil {
		t.Fatal(err)
	}
	defer op.CloseConnection()
	defer op.DiscardCookies()

	created := []byte(`{"type":"response.created","response":{"id":"future-response"}}`)
	permit, err := op.PrepareServerFrame(context.Background(), true, created)
	if err != nil {
		t.Fatal(err)
	}
	if err := permit.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	unknown, err := op.PrepareServerFrame(context.Background(), true, []byte(
		`{"type":"response.cancelled","response":{"id":"future-response"}}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(unknown.leases) != 0 || len(unknown.deactivate) != 0 {
		t.Fatalf("future event mutated lifecycle permit: %#v", unknown)
	}
	if err := unknown.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if activations, deactivations := service.counts(); activations != 1 || deactivations != 0 {
		t.Fatalf("future lifecycle calls = activate:%d deactivate:%d", activations, deactivations)
	}
}
