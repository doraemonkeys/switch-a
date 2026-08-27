package codexcontinuity

import (
	"fmt"
	"sync"

	"github.com/doraemonkeys/switch-a/internal/codex/identity"
	"github.com/google/uuid"
)

type Generation string

type GenerationIDSource interface {
	NewGenerationID() string
}

type uuidGenerationIDSource struct{}

func (uuidGenerationIDSource) NewGenerationID() string { return uuid.NewString() }

type connectionGeneration struct {
	sessionID string
	scope     codexidentity.ProtocolScope
	active    map[codexidentity.OpaqueDigest]struct{}
}

type generationRegistry struct {
	mu       sync.RWMutex
	ids      GenerationIDSource
	entries  map[Generation]*connectionGeneration
	clock    Clock
	observer Observer
}

func newGenerationRegistry(ids GenerationIDSource, clock Clock, observer Observer) *generationRegistry {
	if ids == nil {
		ids = uuidGenerationIDSource{}
	}
	return &generationRegistry{
		ids:      ids,
		entries:  make(map[Generation]*connectionGeneration),
		clock:    clock,
		observer: observer,
	}
}

func (r *generationRegistry) open(sessionID string, scope codexidentity.ProtocolScope) (Generation, error) {
	if _, err := validateLabel(sessionID, "session ID", MaxOperationIDBytes, false); err != nil {
		return "", err
	}
	if _, err := scope.MarshalBinary(); err != nil {
		return "", errorOf(ErrorInvalidInput, "", sessionID, "protocol scope is invalid", err)
	}
	generation := Generation(r.ids.NewGenerationID())
	if _, err := validateLabel(string(generation), "connection generation", MaxOperationIDBytes, false); err != nil {
		return "", err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.entries[generation]; exists {
		return "", errorOf(ErrorUnavailable, "", sessionID, "generation source produced a duplicate", nil)
	}
	r.entries[generation] = &connectionGeneration{
		sessionID: sessionID,
		scope:     scope,
		active:    make(map[codexidentity.OpaqueDigest]struct{}),
	}
	observe(r.observer, Event{
		At:            r.clock.Now().UTC(),
		Action:        "generation_open",
		Outcome:       "active",
		SessionID:     sessionID,
		Generation:    generation,
		ProtocolScope: scope.String(),
	})
	return generation, nil
}

func (r *generationRegistry) activate(generation Generation, binding Binding) error {
	if binding.Kind != KindResponseReference {
		return errorOf(ErrorInvalidInput, binding.Kind, binding.ClaimOperationID, "only response references can be activated", nil)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, exists := r.entries[generation]
	if !exists {
		return errorOf(ErrorInactiveGeneration, binding.Kind, binding.ClaimOperationID, "connection generation is inactive", nil)
	}
	if !entry.scope.Equal(binding.Owner.ProtocolScope) {
		return errorOf(ErrorConflict, binding.Kind, binding.ClaimOperationID, "connection generation belongs to another protocol scope", nil)
	}
	entry.active[binding.Digest] = struct{}{}
	observe(r.observer, Event{
		At:            r.clock.Now().UTC(),
		Action:        "response_activate",
		Outcome:       "active",
		OperationID:   binding.ClaimOperationID,
		SessionID:     entry.sessionID,
		Generation:    generation,
		BindingKind:   binding.Kind,
		Lifecycle:     binding.Lifecycle,
		KeyVersion:    binding.Digest.KeyVersion(),
		ProtocolScope: binding.Owner.ProtocolScope.String(),
	})
	return nil
}

func (r *generationRegistry) deactivate(generation Generation, binding Binding) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, exists := r.entries[generation]
	if !exists {
		return errorOf(ErrorInactiveGeneration, binding.Kind, binding.ClaimOperationID, "connection generation is inactive", nil)
	}
	delete(entry.active, binding.Digest)
	return nil
}

func (r *generationRegistry) close(generation Generation) {
	r.mu.Lock()
	entry, exists := r.entries[generation]
	if exists {
		delete(r.entries, generation)
	}
	r.mu.Unlock()
	if exists {
		observe(r.observer, Event{
			At:            r.clock.Now().UTC(),
			Action:        "generation_close",
			Outcome:       "inactive",
			SessionID:     entry.sessionID,
			Generation:    generation,
			ProtocolScope: entry.scope.String(),
		})
	}
}

func (g Generation) String() string {
	if g == "" {
		return "connection-generation(invalid)"
	}
	return fmt.Sprintf("connection-generation(%s)", safeLabel(string(g)))
}
