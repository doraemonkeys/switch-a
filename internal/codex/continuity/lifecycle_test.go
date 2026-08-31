package codexcontinuity

import (
	"testing"
	"time"
)

func TestRefreshCommittedIdleDeadline(t *testing.T) {
	now := time.Date(2026, time.August, 31, 1, 0, 0, 0, time.UTC)
	idleTTL := 30 * 24 * time.Hour

	pending := Binding{Lifecycle: LifecyclePending, UpdatedAt: now, ExpiresAt: now.Add(time.Hour)}
	if got, changed := RefreshCommittedIdleDeadline(pending, now.Add(time.Minute), idleTTL); changed || got != pending {
		t.Fatalf("pending refresh = %#v, changed %t", got, changed)
	}

	committed := Binding{Lifecycle: LifecycleCommitted, UpdatedAt: now, ExpiresAt: now.Add(idleTTL)}
	usedAt := now.Add(24 * time.Hour)
	refreshed, changed := RefreshCommittedIdleDeadline(committed, usedAt, idleTTL)
	if !changed || !refreshed.UpdatedAt.Equal(usedAt) || !refreshed.ExpiresAt.Equal(usedAt.Add(idleTTL)) {
		t.Fatalf("committed refresh = %#v, changed %t", refreshed, changed)
	}

	stale, changed := RefreshCommittedIdleDeadline(refreshed, now, idleTTL)
	if changed || stale != refreshed {
		t.Fatalf("stale refresh = %#v, changed %t", stale, changed)
	}
}
