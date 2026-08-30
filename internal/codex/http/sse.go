package codexhttp

import (
	"context"
	"errors"
	"fmt"

	"github.com/doraemonkeys/switch-a/internal/codex/headers"
)

// SSEGate retains only the incomplete event suffix between transport writes.
// Complete event bytes are replayed from the exact buffered representation.
type SSEGate struct {
	attempt       *Attempt
	buffered      []byte
	maxEventBytes int
}

var ErrSSEEventTooLarge = errors.New("SSE event exceeds the buffered event limit")

type SSEEvent struct {
	wire       []byte
	visibility *Visibility
}

func (e SSEEvent) ReplayBytes() []byte     { return e.wire }
func (e SSEEvent) Visibility() *Visibility { return e.visibility }
func (g *SSEGate) BufferedBytes() int      { return len(g.buffered) }
func (g *SSEGate) Append(payload []byte) error {
	if g == nil {
		return dependencyError("response_sse", errors.New("SSE gate is unavailable"))
	}
	if g.maxEventBytes <= 0 {
		return dependencyError("response_sse", errors.New("SSE event buffer limit is invalid"))
	}
	if len(payload) > g.maxEventBytes-len(g.buffered) {
		return dependencyError(
			"response_sse",
			fmt.Errorf("%w: limit=%d", ErrSSEEventTooLarge, g.maxEventBytes),
		)
	}
	g.buffered = append(g.buffered, payload...)
	return nil
}
func (g *SSEGate) Discard() { g.buffered = nil }
func (g *SSEGate) Consume(consumedBytes int) {
	if g == nil || consumedBytes <= 0 {
		return
	}
	if consumedBytes >= len(g.buffered) {
		g.buffered = nil
		return
	}
	copy(g.buffered, g.buffered[consumedBytes:])
	g.buffered = g.buffered[:len(g.buffered)-consumedBytes]
}

func (a *Attempt) NewSSEGate(maxEventBytes int) *SSEGate {
	if a == nil || a.operation == nil || a.operation.apiType != codexAPIType {
		return nil
	}
	return &SSEGate{attempt: a, maxEventBytes: maxEventBytes}
}

// PrepareNext creates response-side pending ownership for the first complete
// event before any of its buffered bytes are exposed to the client.
func (g *SSEGate) PrepareNext(ctx context.Context, final bool) (SSEEvent, bool, error) {
	if g == nil || g.attempt == nil || len(g.buffered) == 0 {
		return SSEEvent{}, false, nil
	}
	scan := codexheaders.ScanServerSSE(g.buffered, final)
	messages := scan.Messages()
	if len(messages) == 0 {
		return SSEEvent{}, false, nil
	}
	wire := messages[0].ReplayBytes()
	if len(wire) == 0 || len(wire) > scan.ConsumedBytes() {
		return SSEEvent{}, false, dependencyError("response_sse", errors.New("invalid sse scan boundary"))
	}
	o := g.attempt.operation
	o.mu.Lock()
	leases, err := o.prepareServerMessageLocked(
		ctx,
		g.attempt.protocolScope,
		g.attempt.routeTargetID,
		messages[0],
	)
	o.mu.Unlock()
	if err != nil {
		return SSEEvent{}, false, err
	}
	return SSEEvent{
		wire:       wire,
		visibility: &Visibility{operation: o, leases: leases},
	}, true, nil
}
