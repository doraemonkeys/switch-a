// Package codexprovenance preserves the source resolution of client state across
// physical upstream attempts, without retaining opaque values or selecting routes.
package codexprovenance

import (
	"context"
	"crypto/sha256"
	"errors"
	"slices"
	"sync"

	codexcontinuity "github.com/doraemonkeys/switch-a/internal/codex/continuity"
	codexidentity "github.com/doraemonkeys/switch-a/internal/codex/identity"
	"github.com/doraemonkeys/switch-a/internal/model"
)

type Resolver interface {
	Resolve(context.Context, codexcontinuity.ResolveRequest) (codexcontinuity.Resolution, error)
}

type Config struct {
	Resolver              Resolver
	RecoveryPolicy        model.ConversationRecoveryPolicy
	ClientScopeCandidates []codexidentity.ClientScope
	APIType               string
	OperationID           string
}

type requestKey struct {
	Kind   codexcontinuity.Kind
	Digest [sha256.Size]byte
}

// Ledger belongs to one operation. Serializing first observations prevents a WS
// replay or concurrent echo from replacing the entrance resolution after an outage.
type Ledger struct {
	mu                    sync.Mutex
	resolver              Resolver
	recoveryPolicy        model.ConversationRecoveryPolicy
	clientScopeCandidates []codexidentity.ClientScope
	apiType               string
	operationID           string
	requests              map[requestKey]codexcontinuity.Resolution
}

func NewLedger(config Config) *Ledger {
	return &Ledger{
		resolver: config.Resolver, recoveryPolicy: config.RecoveryPolicy,
		clientScopeCandidates: slices.Clone(config.ClientScopeCandidates),
		apiType:               config.APIType, operationID: config.OperationID,
		requests: make(map[requestKey]codexcontinuity.Resolution),
	}
}

func (l *Ledger) AllowsAccountSwitch() bool {
	return l.recoveryPolicy == model.ConversationRecoverySwitchAccountPreserveConversation
}

func (l *Ledger) ObserveRequest(ctx context.Context, evidence codexcontinuity.Evidence) (codexcontinuity.Resolution, error) {
	if err := evidence.Kind.Validate(); err != nil {
		return codexcontinuity.Resolution{}, err
	}
	if len(evidence.DigestInput) == 0 || len(evidence.DigestInput) > codexcontinuity.MaxDigestInputBytes {
		return codexcontinuity.Resolution{}, l.failure(codexcontinuity.ErrorInvalidInput, "digest input is empty or too large")
	}
	key := keyFor(evidence)
	l.mu.Lock()
	defer l.mu.Unlock()
	if resolution, exists := l.requests[key]; exists {
		return cloneResolution(resolution), nil
	}
	if l.resolver == nil {
		return codexcontinuity.Resolution{}, l.failure(codexcontinuity.ErrorInvalidInput, "source resolver is required")
	}
	resolution, err := l.resolver.Resolve(ctx, codexcontinuity.ResolveRequest{
		Evidence: evidence, ClientScopeCandidates: slices.Clone(l.clientScopeCandidates), OperationID: l.operationID,
	})
	if err != nil {
		return codexcontinuity.Resolution{}, err
	}
	if err := l.validateResolution(resolution); err != nil {
		return codexcontinuity.Resolution{}, err
	}
	l.requests[key] = cloneResolution(resolution)
	return cloneResolution(resolution), nil
}

// LookupRequest never learns response-only state. A provider echo keeps the exact
// original resolution even if the store has changed since the request was admitted.
func (l *Ledger) LookupRequest(evidence codexcontinuity.Evidence) (codexcontinuity.Resolution, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	resolution, exists := l.requests[keyFor(evidence)]
	return cloneResolution(resolution), exists
}

func (l *Ledger) ValidateOwner(resolution codexcontinuity.Resolution, target codexidentity.ProtocolScope) error {
	if err := l.validateResolution(resolution); err != nil {
		return err
	}
	if resolution.Owner == nil {
		return nil
	}
	if !l.AllowsAccountSwitch() && !resolution.Owner.ProtocolScope.Equal(target) {
		return l.failure(codexcontinuity.ErrorConflict, "source owner belongs to another protocol scope")
	}
	return nil
}

func (l *Ledger) validateResolution(resolution codexcontinuity.Resolution) error {
	switch resolution.Status {
	case codexcontinuity.ResolutionOwned:
		if resolution.Owner == nil {
			return l.failure(codexcontinuity.ErrorInvalidTransition, "owned resolution has no owner")
		}
	case codexcontinuity.ResolutionExpired:
	case codexcontinuity.ResolutionUnknown, codexcontinuity.ResolutionUnavailable:
		if resolution.Owner != nil {
			return l.failure(codexcontinuity.ErrorInvalidTransition, "opaque resolution has an unexpected owner")
		}
	default:
		return l.failure(codexcontinuity.ErrorInvalidTransition, "source resolution status is invalid")
	}
	if resolution.Owner == nil {
		return nil
	}
	owner := resolution.Owner
	if !slices.ContainsFunc(l.clientScopeCandidates, owner.ClientScope.Equal) {
		return l.failure(codexcontinuity.ErrorConflict, "source owner belongs to another client scope")
	}
	if owner.ProtocolScope.APIType() != l.apiType {
		return l.failure(codexcontinuity.ErrorConflict, "source owner belongs to another API type")
	}
	return nil
}

func (l *Ledger) failure(kind codexcontinuity.ErrorKind, reason string) error {
	return &codexcontinuity.Error{Kind: kind, OperationID: l.operationID, Reason: reason}
}

// IsOpaqueDegradation excludes conflicts, invalid transitions, capacity limits,
// and arbitrary failures so recovery cannot disguise a different continuity bug.
func IsOpaqueDegradation(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	return codexcontinuity.IsError(err, codexcontinuity.ErrorUnknown) ||
		codexcontinuity.IsError(err, codexcontinuity.ErrorExpired) ||
		codexcontinuity.IsError(err, codexcontinuity.ErrorUnavailable)
}

func keyFor(evidence codexcontinuity.Evidence) requestKey {
	return requestKey{Kind: evidence.Kind, Digest: sha256.Sum256(evidence.DigestInput)}
}

func cloneResolution(resolution codexcontinuity.Resolution) codexcontinuity.Resolution {
	if resolution.Owner != nil {
		owner := *resolution.Owner
		resolution.Owner = &owner
	}
	return resolution
}
