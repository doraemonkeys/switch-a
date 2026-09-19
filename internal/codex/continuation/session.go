package continuation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/continuity"
	"github.com/doraemonkeys/switch-a/internal/codex/identity"
)

type Store interface {
	Lookup(context.Context, Binding) (Binding, bool, error)
	Save(context.Context, []Binding) error
}

type PolicyLookup func(context.Context, string) (Policy, bool, error)
type Trace struct {
	OperationID string
	Milestone   string
	Decision    Decision
	Err         error
}

type Service struct {
	Store   Store
	Policy  PolicyLookup
	Observe func(Trace)
}

type observation struct {
	key    Binding
	source *Route
	rank   int
}

type Session struct {
	mu          sync.Mutex
	service     *Service
	clientID    string
	operationID string
	evidence    map[string]observation
	policies    map[string]Policy
	current     *Route
	dirty       bool
}

func (s *Service) Begin(clientID, operationID string) *Session {
	return &Session{service: s, clientID: clientID, operationID: operationID, evidence: make(map[string]observation), policies: make(map[string]Policy)}
}

func ProtocolKey(scope codexidentity.ProtocolScope) string {
	encoded, err := scope.MarshalBinary()
	if err != nil {
		return ""
	}
	return hex.EncodeToString(encoded)
}

// Observe freezes entrance evidence once. A physical replay cannot turn the
// previous attempt into a new conversation source.
func (s *Session) Observe(ctx context.Context, evidence codexcontinuity.Evidence, resolution codexcontinuity.Resolution) error {
	if s == nil || evidence.Kind == codexcontinuity.KindSessionID {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sum := sha256.Sum256(evidence.DigestInput)
	key := Binding{ClientID: s.clientID, Kind: string(evidence.Kind), Digest: hex.EncodeToString(sum[:])}
	index := key.Kind + ":" + key.Digest
	if _, exists := s.evidence[index]; exists {
		return nil
	}
	entry := observation{key: key, rank: evidenceRank(evidence.Kind)}
	if s.service != nil && s.service.Store != nil {
		stored, exists, err := s.service.Store.Lookup(ctx, key)
		if err != nil {
			return fmt.Errorf("resolve Codex conversation route: %w", err)
		}
		if exists {
			entry.source = &Route{ProviderID: stored.ProviderID, ProtocolScope: stored.ProtocolScope, Policy: Policy{Outbound: stored.Outbound}}
		}
	}
	if entry.source == nil && resolution.Owner != nil {
		entry.source = &Route{ProviderID: resolution.Owner.RouteTargetHint, ProtocolScope: ProtocolKey(resolution.Owner.ProtocolScope)}
	}
	if entry.source == nil && !evidence.Kind.ClientClaimable() {
		// A previously issued reference with no local history is external state;
		// accepting it is the destination's explicit continuation choice.
		entry.source = &Route{}
	}
	if entry.source != nil {
		policy, err := s.sourcePolicy(ctx, entry.source.ProviderID, entry.source.Policy)
		if err != nil {
			return err
		}
		entry.source.Policy = policy
	}
	s.evidence[index] = entry
	s.dirty = true
	return nil
}

func (s *Session) sourcePolicy(ctx context.Context, id string, fallback Policy) (Policy, error) {
	if policy, ok := s.policies[id]; ok {
		return policy, nil
	}
	policy := fallback.Effective()
	if id != "" && s.service != nil && s.service.Policy != nil {
		current, exists, err := s.service.Policy(ctx, id)
		if err != nil {
			return Policy{}, fmt.Errorf("resolve Codex source policy: %w", err)
		}
		if exists {
			policy = current.Effective()
		}
	}
	s.policies[id] = policy
	return policy, nil
}

func (s *Session) sourcesLocked() []Route {
	var sources []Route
	rank := 0
	for _, entry := range s.evidence {
		if entry.source == nil || entry.rank < rank {
			continue
		}
		if entry.rank > rank {
			sources = nil
			rank = entry.rank
		}
		sources = append(sources, *entry.source)
	}
	if len(sources) == 0 && s.current != nil {
		return []Route{*s.current}
	}
	return sources
}

func (s *Session) PreferredProviderID() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var preferred string
	for _, source := range s.sourcesLocked() {
		if source.ProviderID == "" || preferred != "" && preferred != source.ProviderID {
			return ""
		}
		preferred = source.ProviderID
	}
	return preferred
}

func (s *Session) Evaluate(providerID string, scope codexidentity.ProtocolScope, policy Policy) Decision {
	if s == nil {
		return Decision{Allowed: true, Reason: "no_conversation", TargetProviderID: providerID}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	target := Route{ProviderID: providerID, ProtocolScope: ProtocolKey(scope), Policy: policy.Effective()}
	s.policies[providerID] = target.Policy
	decision := Decision{Allowed: true, Reason: "new_conversation", TargetProviderID: providerID}
	for _, source := range s.sourcesLocked() {
		decision = Evaluate(source, target)
		if !decision.Allowed {
			break
		}
	}
	s.trace("candidate", decision, nil)
	return decision
}

// CommitVisible advances the serving relationship only when the transport has
// established business visibility, not when it merely selects or dials a route.
func (s *Session) CommitVisible(ctx context.Context, providerID string, scope codexidentity.ProtocolScope, policy Policy) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	route := Route{ProviderID: providerID, ProtocolScope: ProtocolKey(scope), Policy: policy.Effective()}
	if !s.dirty && s.current != nil && *s.current == route {
		return nil
	}
	bindings := make([]Binding, 0, len(s.evidence))
	for _, entry := range s.evidence {
		binding := entry.key
		binding.ProviderID, binding.ProtocolScope, binding.Outbound, binding.UpdatedAt = route.ProviderID, route.ProtocolScope, route.Policy.Outbound, time.Now().UTC()
		bindings = append(bindings, binding)
	}
	if s.service != nil && s.service.Store != nil {
		if err := s.service.Store.Save(ctx, bindings); err != nil {
			s.trace("commit", Decision{TargetProviderID: providerID, Reason: "persistence_failed"}, err)
			return fmt.Errorf("commit Codex conversation route: %w", err)
		}
	}
	s.current = &route
	for key, entry := range s.evidence {
		entry.source = &route
		s.evidence[key] = entry
	}
	s.dirty = false
	s.trace("commit", Decision{Allowed: true, TargetProviderID: providerID, Reason: "serving_route_committed"}, nil)
	return nil
}

func (s *Session) trace(milestone string, decision Decision, err error) {
	if s.service != nil && s.service.Observe != nil {
		s.service.Observe(Trace{OperationID: s.operationID, Milestone: milestone, Decision: decision, Err: err})
	}
}

func (s *Session) Authorize(ctx context.Context, candidate codexidentity.CandidateSnapshot) (Policy, error) {
	if s == nil {
		return Policy{}.Effective(), nil
	}
	s.mu.Lock()
	policy, err := s.sourcePolicy(ctx, candidate.RouteTargetID(), Policy{})
	s.mu.Unlock()
	if err != nil {
		return Policy{}, err
	}
	decision := s.Evaluate(candidate.RouteTargetID(), candidate.ProtocolScope(), policy)
	if !decision.Allowed {
		return Policy{}, &Denied{Decision: decision}
	}
	return policy, nil
}

// A thread identifies the conversation across changing windows and opaque
// references. Session-ID identifies a running client session and is deliberately
// excluded from route bindings: several new conversations can share it.
const (
	rankOther = iota + 1
	rankTurnMetadata
	rankTurnState
	rankResponseReference
	rankWindow
	rankConversation
	rankThread
)

func evidenceRank(kind codexcontinuity.Kind) int {
	switch kind {
	case codexcontinuity.KindThreadID:
		return rankThread
	case codexcontinuity.KindConversationID:
		return rankConversation
	case codexcontinuity.KindWindowID:
		return rankWindow
	case codexcontinuity.KindResponseReference:
		return rankResponseReference
	case codexcontinuity.KindTurnState:
		return rankTurnState
	case codexcontinuity.KindTurnMetadata:
		return rankTurnMetadata
	default:
		return rankOther
	}
}
