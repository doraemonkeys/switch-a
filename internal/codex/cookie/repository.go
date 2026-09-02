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
	IdleExpiresAt     time.Time
	AbsoluteExpiresAt time.Time
}

type BindingUse struct {
	Disposition BindingDisposition
	Record      BindingRecord
	Refresh     bool
}

type BindingLookup struct {
	HandleDigests []codexkeyring.Digest
	ClientScopes  []codexidentity.ClientScope
	At            time.Time
	Policy        Policy
}

type MergeResult struct {
	Upserted    int
	Deleted     int
	Reencrypted int
	Evicted     int
}

type CleanupRequest struct {
	At                   time.Time
	Policy               Policy
	ReachableAuthorities []codexidentity.CookieAuthority
}

type CleanupResult struct {
	ExpiredBindings   int
	ExpiredCookies    int
	OrphanAuthorities int
	EmptyAuthorities  int
}

// Repository is defined at the consuming service boundary. Implementations
// must serialize each merge in one durable transaction; process-local locking
// may reduce contention but is not the correctness mechanism.
type Repository interface {
	UseBinding(context.Context, BindingLookup) (BindingUse, error)
	CreateBinding(context.Context, BindingRecord, Policy) error
	Load(context.Context, CookieScope, time.Time) (Snapshot, error)
	Touch(context.Context, CookieScope, []CookieKey, time.Time) error
	Merge(context.Context, CookieScope, []Mutation, time.Time, Policy) (MergeResult, error)
	Cleanup(context.Context, CleanupRequest) (CleanupResult, error)
}
