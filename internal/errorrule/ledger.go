package errorrule

import (
	"errors"
	"fmt"
	"maps"
	"math"
)

var (
	ErrGlobalAttemptLimit = errors.New("global attempt limit exhausted")
	ErrCounterOverflow    = errors.New("retry ledger counter overflow")
)

type ProviderRuleKey struct {
	ProviderID ProviderID
	RuleID     RuleID
}

// RetryLedger uses copy-on-write transitions. A rejected permit can retain the
// old value, making it impossible to charge a retry before fetch dispatch.
type RetryLedger struct {
	logicalAttemptsStarted  uint
	providerAttemptsStarted map[ProviderID]uint
	legacyRetriesScheduled  map[ProviderID]uint
	ruleRetriesScheduled    map[ProviderRuleKey]uint
}

func (l RetryLedger) LogicalAttemptsStarted() uint {
	return l.logicalAttemptsStarted
}

func (l RetryLedger) ProviderAttemptsStarted(providerID ProviderID) uint {
	return l.providerAttemptsStarted[providerID]
}

func (l RetryLedger) LegacyRetriesScheduled(providerID ProviderID) uint {
	return l.legacyRetriesScheduled[providerID]
}

func (l RetryLedger) RuleRetriesScheduled(key ProviderRuleKey) uint {
	return l.ruleRetriesScheduled[key]
}

func (l RetryLedger) GlobalRemaining(globalMaxAttempts uint) (remaining uint, unlimited bool) {
	if globalMaxAttempts == 0 {
		return 0, true
	}
	if l.logicalAttemptsStarted >= globalMaxAttempts {
		return 0, false
	}
	return globalMaxAttempts - l.logicalAttemptsStarted, false
}

func (l RetryLedger) StartAttempt(providerID ProviderID, globalMaxAttempts uint) (RetryLedger, error) {
	return l.start(providerID, globalMaxAttempts, nil, false)
}

func (l RetryLedger) StartLegacyRetry(providerID ProviderID, globalMaxAttempts uint) (RetryLedger, error) {
	return l.start(providerID, globalMaxAttempts, nil, true)
}

func (l RetryLedger) StartRuleRetry(key ProviderRuleKey, globalMaxAttempts uint) (RetryLedger, error) {
	if err := key.RuleID.Validate(); err != nil {
		return RetryLedger{}, err
	}
	return l.start(key.ProviderID, globalMaxAttempts, &key, false)
}

func (l RetryLedger) start(
	providerID ProviderID,
	globalMaxAttempts uint,
	ruleKey *ProviderRuleKey,
	legacyRetry bool,
) (RetryLedger, error) {
	if err := validateProviderID(providerID); err != nil {
		return RetryLedger{}, err
	}
	if remaining, unlimited := l.GlobalRemaining(globalMaxAttempts); !unlimited && remaining == 0 {
		return RetryLedger{}, ErrGlobalAttemptLimit
	}
	if l.logicalAttemptsStarted == math.MaxUint || l.providerAttemptsStarted[providerID] == math.MaxUint {
		return RetryLedger{}, ErrCounterOverflow
	}
	if legacyRetry && l.legacyRetriesScheduled[providerID] == math.MaxUint {
		return RetryLedger{}, ErrCounterOverflow
	}
	if ruleKey != nil && l.ruleRetriesScheduled[*ruleKey] == math.MaxUint {
		return RetryLedger{}, ErrCounterOverflow
	}

	next := l.clone()
	next.logicalAttemptsStarted++
	next.providerAttemptsStarted[providerID]++
	if legacyRetry {
		next.legacyRetriesScheduled[providerID]++
	}
	if ruleKey != nil {
		next.ruleRetriesScheduled[*ruleKey]++
	}
	return next, nil
}

func (l RetryLedger) RuleRetriesRemaining(key ProviderRuleKey, maxRetries int) int {
	if maxRetries <= 0 {
		return 0
	}
	scheduled := l.ruleRetriesScheduled[key]
	if scheduled >= uint(maxRetries) {
		return 0
	}
	return maxRetries - int(scheduled)
}

func (l RetryLedger) clone() RetryLedger {
	return RetryLedger{
		logicalAttemptsStarted:  l.logicalAttemptsStarted,
		providerAttemptsStarted: cloneCounterMap(l.providerAttemptsStarted),
		legacyRetriesScheduled:  cloneCounterMap(l.legacyRetriesScheduled),
		ruleRetriesScheduled:    cloneCounterMap(l.ruleRetriesScheduled),
	}
}

func cloneCounterMap[K comparable](source map[K]uint) map[K]uint {
	clone := make(map[K]uint, len(source)+1)
	maps.Copy(clone, source)
	return clone
}

func (key ProviderRuleKey) Validate() error {
	if err := validateProviderID(key.ProviderID); err != nil {
		return err
	}
	if err := key.RuleID.Validate(); err != nil {
		return fmt.Errorf("rule retry key: %w", err)
	}
	return nil
}
