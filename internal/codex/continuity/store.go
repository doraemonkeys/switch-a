package codexcontinuity

import (
	"context"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/identity"
)

type StoreDecision string

const (
	StoreClaimed   StoreDecision = "claimed"
	StoreOwned     StoreDecision = "owned"
	StoreUnknown   StoreDecision = "unknown"
	StoreExpired   StoreDecision = "expired"
	StoreConflict  StoreDecision = "conflict"
	StoreCapacity  StoreDecision = "capacity"
	StoreCommitted StoreDecision = "committed"
	StoreAbandoned StoreDecision = "abandoned"
)

type StoreResult struct {
	Decision StoreDecision
	Binding  Binding
}

type StoreClaim struct {
	Kind                  Kind
	CurrentDigest         codexidentity.OpaqueDigest
	DigestCandidates      []codexidentity.OpaqueDigest
	Owner                 Owner
	ClientScopeCandidates []codexidentity.ClientScope
	OperationID           string
	Now                   time.Time
	Limits                Limits
}

type StoreLookup struct {
	Kind                  Kind
	DigestCandidates      []codexidentity.OpaqueDigest
	ClientScopeCandidates []codexidentity.ClientScope
	ProtocolScope         *codexidentity.ProtocolScope
	OperationID           string
	Now                   time.Time
	Limits                Limits
}

type StoreCommit struct {
	Binding Binding
	Now     time.Time
	Limits  Limits
}

type StoreAbandon struct {
	Binding Binding
	Now     time.Time
	Limits  Limits
}

type StoreCleanup struct {
	Now    time.Time
	Policy map[Kind]Limits
}

// Store is defined where lifecycle semantics are consumed. Implementations
// must make each method transactional; a plain read followed by an insert does
// not satisfy Claim's concurrent-uniqueness contract.
type Store interface {
	Claim(context.Context, StoreClaim) (StoreResult, error)
	Lookup(context.Context, StoreLookup) (StoreResult, error)
	Commit(context.Context, StoreCommit) (StoreResult, error)
	Abandon(context.Context, StoreAbandon) (StoreResult, error)
	Cleanup(context.Context, StoreCleanup) (CleanupResult, error)
	RequiredHMACVersions(context.Context) ([]string, error)
}

type OpaqueDigester interface {
	OpaqueDigest(codexidentity.OpaqueNamespace, []byte) (codexidentity.OpaqueDigest, error)
	OpaqueDigestCandidates(codexidentity.OpaqueNamespace, []byte) ([]codexidentity.OpaqueDigest, error)
}

type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }
