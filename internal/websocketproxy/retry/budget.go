package retry

import "github.com/doraemonkeys/switch-a/internal/errorrule"

// Budget counts logical attempts independently from physical authentication
// refreshes and cross-provider moves. A connection retry consumes one attempt.
type Budget struct {
	ledger errorrule.RetryLedger
	limit  uint
}

func NewBudget(maxAttempts int) Budget {
	return Budget{limit: uint(max(0, maxAttempts))}
}

func (b Budget) CanStart() bool {
	remaining, unlimited := b.ledger.GlobalRemaining(b.limit)
	return unlimited || remaining > 0
}

func (b Budget) CanRetry(providerID string, maxRetries int) bool {
	attempts := b.ProviderAttempts(providerID)
	return attempts > 0 && attempts-1 < maxRetries && b.CanStart()
}

func (b Budget) ProviderAttempts(providerID string) int {
	return int(b.ledger.ProviderAttemptsStarted(errorrule.ProviderID(providerID)))
}

func (b Budget) Attempts() int {
	return int(b.ledger.LogicalAttemptsStarted())
}

func (b *Budget) Start(providerID string) error {
	var next errorrule.RetryLedger
	var err error
	if b.ProviderAttempts(providerID) == 0 {
		next, err = b.ledger.StartAttempt(errorrule.ProviderID(providerID), b.limit)
	} else {
		next, err = b.ledger.StartLegacyRetry(errorrule.ProviderID(providerID), b.limit)
	}
	if err == nil {
		b.ledger = next
	}
	return err
}
