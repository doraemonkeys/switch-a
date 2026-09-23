package providercookie

import (
	"context"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/identity"
	"github.com/doraemonkeys/switch-a/internal/codex/keyring"
)

type BindingDisposition string

const (
	BindingUnknown       BindingDisposition = "unknown"
	BindingExpired       BindingDisposition = "expired"
	BindingOwnerMismatch BindingDisposition = "owner_mismatch"
	BindingValid         BindingDisposition = "valid"
)

type BindingRecord struct {
	HandleDigest      codexkeyring.Digest
	JarID             JarID
	ClientScope       codexidentity.ClientScope
	CreatedAt         time.Time
	LastAccessAt      time.Time
	LastReturnedAt    time.Time
	IdleExpiresAt     time.Time
	AbsoluteExpiresAt time.Time
}

type BindingUse struct {
	Disposition BindingDisposition
	Record      BindingRecord
	Refresh     bool
	Release     func()
}

type BindingLookup struct {
	HandleDigests []codexkeyring.Digest
	ClientScopes  []codexidentity.ClientScope
	At            time.Time
	Policy        Policy
}

type MergeResult struct {
	Upserted          int
	Deleted           int
	Reencrypted       int
	Evicted           int
	ReclaimedBindings int
}

type CreatedJar struct {
	Merge   MergeResult
	Release func()
}

type CleanupRequest struct {
	At                   time.Time
	Policy               Policy
	ReachableAuthorities []codexidentity.CookieAuthority
}

type CleanupResult struct {
	ExpiredBindings   int
	EmptyBindings     int
	ExpiredCookies    int
	OrphanAuthorities int
	EmptyAuthorities  int
}

// Repository is defined at the consuming service boundary. Create and merge
// must be atomic transactions. UseBinding and CreateJar acquire live leases
// before releasing the writer lock: atomic writes alone cannot protect a jar
// from reclamation between lookup and request completion. Release is idempotent.
type Repository interface {
	UseBinding(context.Context, BindingLookup) (BindingUse, error)
	CreateJar(context.Context, BindingRecord, codexidentity.CookieAuthority, []Mutation, Policy) (CreatedJar, error)
	Load(context.Context, CookieScope, time.Time) (Snapshot, error)
	Touch(context.Context, CookieScope, []CookieKey, time.Time) error
	Merge(context.Context, CookieScope, []Mutation, time.Time, Policy) (MergeResult, error)
	Cleanup(context.Context, CleanupRequest) (CleanupResult, error)
}
