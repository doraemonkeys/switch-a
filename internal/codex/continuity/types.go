package codexcontinuity

import (
	"fmt"
	"strings"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/identity"
)

const (
	MaxOperationIDBytes   = 128
	MaxRouteTargetIDBytes = 256
	MaxDigestInputBytes   = 1 << 20
	MaxLookupCandidates   = 64
)

type Kind string

const (
	KindThreadID          Kind = "thread_id"
	KindSessionID         Kind = "session_id"
	KindConversationID    Kind = "conversation_id"
	KindWindowID          Kind = "window_id"
	KindTurnState         Kind = "turn_state"
	KindTurnMetadata      Kind = "turn_metadata"
	KindResponseReference Kind = "response_reference"
)

var allKinds = [...]Kind{
	KindThreadID,
	KindSessionID,
	KindConversationID,
	KindWindowID,
	KindTurnState,
	KindTurnMetadata,
	KindResponseReference,
}

func (k Kind) Validate() error {
	for _, supported := range allKinds {
		if k == supported {
			return nil
		}
	}
	return errorOf(ErrorInvalidInput, k, "", "binding kind is unsupported", nil)
}

func (k Kind) ClientClaimable() bool {
	switch k {
	case KindThreadID, KindSessionID, KindConversationID, KindWindowID, KindTurnMetadata:
		return true
	default:
		return false
	}
}

func (k Kind) ResponseIssuable() bool {
	return k == KindTurnState || k == KindResponseReference
}

func (k Kind) Namespace() (codexidentity.OpaqueNamespace, error) {
	switch k {
	case KindThreadID, KindSessionID, KindConversationID, KindWindowID:
		return codexidentity.OpaqueSessionIdentity, nil
	case KindTurnState:
		return codexidentity.OpaqueTurnState, nil
	case KindTurnMetadata:
		return codexidentity.OpaqueTurnMetadata, nil
	case KindResponseReference:
		return codexidentity.OpaqueResponseReference, nil
	default:
		return "", errorOf(ErrorInvalidInput, k, "", "binding kind is unsupported", nil)
	}
}

// Evidence accepts codexheaders.BindingCandidate.DigestInput rather than the
// wire value. That keeps field-category prefixing at the protocol boundary and
// ensures this package never needs to retain or expose the opaque bytes.
type Evidence struct {
	Kind        Kind
	DigestInput []byte
}

type Scope struct {
	CurrentClientScope    codexidentity.ClientScope
	ClientScopeCandidates []codexidentity.ClientScope
	ProtocolScope         codexidentity.ProtocolScope
	RouteTargetHint       string
}

type Owner struct {
	ClientScope     codexidentity.ClientScope
	ProtocolScope   codexidentity.ProtocolScope
	RouteTargetHint string
}

func (o Owner) Equal(other Owner) bool {
	// RouteTarget is only a routing hint. Ownership remains stable across
	// healthy targets that resolve to the same ProtocolScope.
	return o.ClientScope.Equal(other.ClientScope) && o.ProtocolScope.Equal(other.ProtocolScope)
}

type Lifecycle string

const (
	LifecyclePending   Lifecycle = "pending"
	LifecycleCommitted Lifecycle = "committed"
	LifecycleTombstone Lifecycle = "tombstone"
)

type Binding struct {
	Kind             Kind
	Digest           codexidentity.OpaqueDigest
	Owner            Owner
	Lifecycle        Lifecycle
	ClaimOperationID string
	CreatedAt        time.Time
	UpdatedAt        time.Time
	CommittedAt      *time.Time
	ExpiresAt        time.Time
	TombstoneUntil   *time.Time
}

func (b Binding) String() string {
	return fmt.Sprintf(
		"continuity-binding(kind=%s,lifecycle=%s,operation_id=%s,digest=redacted,owner=%s)",
		b.Kind,
		b.Lifecycle,
		safeLabel(b.ClaimOperationID),
		b.Owner.ProtocolScope,
	)
}

func (b Binding) GoString() string { return b.String() }

type Lease struct {
	binding Binding
	created bool
	origin  leaseOrigin
}

type leaseOrigin uint8

const (
	leaseOriginDurable leaseOrigin = iota
	leaseOriginProvenance
)

func (l Lease) Binding() Binding   { return l.binding }
func (l Lease) NewlyClaimed() bool { return l.created }

type ClaimRequest struct {
	Evidence    Evidence
	Scope       Scope
	OperationID string
}

type ResolveRequest struct {
	Evidence              Evidence
	ClientScopeCandidates []codexidentity.ClientScope
	OperationID           string
}

type ValidateRequest struct {
	Evidence              Evidence
	ClientScopeCandidates []codexidentity.ClientScope
	ProtocolScope         codexidentity.ProtocolScope
	OperationID           string
}

type CleanupResult struct {
	Expired    int64
	Tombstoned int64
	Deleted    int64
}

func validateLabel(value, field string, maximum int, optional bool) (string, error) {
	trimmed := strings.TrimSpace(value)
	if optional && trimmed == "" {
		return "", nil
	}
	if trimmed == "" || trimmed != value || len(value) > maximum || safeLabel(value) == "invalid" {
		return "", errorOf(ErrorInvalidInput, "", "", field+" is empty, non-canonical, or too large", nil)
	}
	return value, nil
}
