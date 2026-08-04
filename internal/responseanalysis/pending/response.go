package pending

import (
	"net/http"
	"sync"
)

type resultCell[V any] struct {
	once  sync.Once
	ready chan struct{}
	value V
}

func newResultCell[V any]() resultCell[V] {
	return resultCell[V]{ready: make(chan struct{})}
}

func (c *resultCell[V]) publish(value V) {
	c.once.Do(func() {
		c.value = value
		close(c.ready)
	})
}

func (c *resultCell[V]) wait() V {
	<-c.ready
	return c.value
}

type commandKind uint8

const (
	commandCommit commandKind = iota + 1
	commandDiscard
)

type command[T any] struct {
	kind         commandKind
	cause        TransitionCause
	accepted     chan struct{}
	commitReply  chan commitResult[T]
	discardReply chan discardResult
}

type commitResult[T any] struct {
	forwarding *ForwardingResponse[T]
	err        error
}

type discardResult struct {
	receipt DiscardReceipt
	err     error
}

type shared[T any] struct {
	commands   chan command[T]
	boundary   resultCell[Boundary[T]]
	semantic   resultCell[SemanticMilestone[T]]
	completion resultCell[Completion[T]]
	forwarding ForwardingResponse[T]
	clone      func(T) T
}

func newShared[T any](commandCapacity int, clone func(T) T) *shared[T] {
	state := &shared[T]{
		commands:   make(chan command[T], commandCapacity),
		boundary:   newResultCell[Boundary[T]](),
		semantic:   newResultCell[SemanticMilestone[T]](),
		completion: newResultCell[Completion[T]](),
		clone:      clone,
	}
	state.forwarding.shared = state
	return state
}

type Response[T any] struct {
	shared *shared[T]
}

func (p *Response[T]) AwaitBoundary() Boundary[T] {
	if p == nil || p.shared == nil {
		return Boundary[T]{}
	}
	return p.shared.cloneBoundary(p.shared.boundary.wait())
}

func (p *Response[T]) Commit(cause TransitionCause) (*ForwardingResponse[T], error) {
	if err := cause.validate(); err != nil {
		return nil, err
	}
	if p == nil || p.shared == nil {
		return nil, &AlreadyResolved{}
	}
	reply := make(chan commitResult[T], 1)
	accepted := make(chan struct{})
	request := command[T]{kind: commandCommit, cause: cause, accepted: accepted, commitReply: reply}
	select {
	case <-p.shared.completion.ready:
		completion := p.shared.completion.value
		return nil, &AlreadyResolved{State: completion.State}
	default:
	}
	select {
	case p.shared.commands <- request:
	case <-p.shared.completion.ready:
		completion := p.shared.completion.value
		return nil, &AlreadyResolved{State: completion.State}
	}
	select {
	case <-accepted:
	case <-p.shared.completion.ready:
		select {
		case <-accepted:
		default:
			completion := p.shared.completion.value
			return nil, &AlreadyResolved{State: completion.State}
		}
	}
	result := <-reply
	return result.forwarding, result.err
}

func (p *Response[T]) Discard(cause TransitionCause) (DiscardReceipt, error) {
	if err := cause.validate(); err != nil {
		return DiscardReceipt{}, err
	}
	if p == nil || p.shared == nil {
		return DiscardReceipt{}, &AlreadyResolved{}
	}
	reply := make(chan discardResult, 1)
	accepted := make(chan struct{})
	request := command[T]{kind: commandDiscard, cause: cause, accepted: accepted, discardReply: reply}
	select {
	case <-p.shared.completion.ready:
		completion := p.shared.completion.value
		return DiscardReceipt{}, &AlreadyResolved{State: completion.State}
	default:
	}
	select {
	case p.shared.commands <- request:
	case <-p.shared.completion.ready:
		completion := p.shared.completion.value
		return DiscardReceipt{}, &AlreadyResolved{State: completion.State}
	}
	select {
	case <-accepted:
	case <-p.shared.completion.ready:
		select {
		case <-accepted:
		default:
			completion := p.shared.completion.value
			return DiscardReceipt{}, &AlreadyResolved{State: completion.State}
		}
	}
	result := <-reply
	return result.receipt, result.err
}

type ForwardingResponse[T any] struct {
	shared *shared[T]
}

func (f *ForwardingResponse[T]) AwaitSemanticOrCompletion() SemanticMilestone[T] {
	if f == nil || f.shared == nil {
		return SemanticMilestone[T]{}
	}
	return f.shared.cloneSemantic(f.shared.semantic.wait())
}

func (f *ForwardingResponse[T]) Wait() Completion[T] {
	if f == nil || f.shared == nil {
		return Completion[T]{}
	}
	return f.shared.cloneCompletion(f.shared.completion.wait())
}

func (s *shared[T]) cloneObservation(value T) T {
	if s == nil || s.clone == nil {
		return value
	}
	return s.clone(value)
}

func (s *shared[T]) cloneBoundary(value Boundary[T]) Boundary[T] {
	clone := value
	if value.HasObservation {
		clone.Observation = s.cloneObservation(value.Observation)
	}
	return clone
}

func (s *shared[T]) cloneSemantic(value SemanticMilestone[T]) SemanticMilestone[T] {
	clone := value
	if value.Matched {
		clone.Observation = s.cloneObservation(value.Observation)
	}
	return clone
}

func (s *shared[T]) cloneCompletion(value Completion[T]) Completion[T] {
	clone := value
	clone.Header = cloneHeader(value.Header)
	clone.Trailer = cloneHeader(value.Trailer)
	if value.HasSemanticObservation {
		clone.SemanticObservation = s.cloneObservation(value.SemanticObservation)
	}
	if value.HasUsageObservation {
		clone.UsageObservation = s.cloneObservation(value.UsageObservation)
	}
	return clone
}

func cloneHeader(source http.Header) http.Header {
	if source == nil {
		return nil
	}
	clone := make(http.Header, len(source))
	for key, values := range source {
		clone[key] = append([]string(nil), values...)
	}
	return clone
}
