package codexcontinuity

import "time"

// RefreshCommittedIdleDeadline advances only: an older concurrent settlement
// must never shorten the retention established by a newer successful use.
func RefreshCommittedIdleDeadline(binding Binding, usedAt time.Time, idleTTL time.Duration) (Binding, bool) {
	if binding.Lifecycle != LifecycleCommitted {
		return binding, false
	}
	usedAt = usedAt.UTC()
	deadline := usedAt.Add(idleTTL)
	if !deadline.After(binding.ExpiresAt) {
		return binding, false
	}
	binding.UpdatedAt = usedAt
	binding.ExpiresAt = deadline
	return binding, true
}
