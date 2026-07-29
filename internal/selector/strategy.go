// Package selector provides provider selection strategies.
package selector

import (
	"math"
	"math/rand/v2"
	"sort"

	"github.com/doraemonkeys/switch-a/internal/model"
)

// Strategy constants.
const (
	StrategyPriority = "priority"
	StrategyRandom   = "random"
	StrategyWeight   = "weight"
)

// MaxWeight is the maximum allowed weight value to prevent integer overflow.
// Individual weights are capped at this value during weighted selection.
const MaxWeight = 1_000_000

// SelectByPriority sorts providers by priority (ascending) and returns the first available.
// Lower priority value = higher precedence.
func SelectByPriority(providers []*model.Provider) *model.Provider {
	if len(providers) == 0 {
		return nil
	}

	// Sort by priority (ascending), then by ID for stability
	sorted := make([]*model.Provider, len(providers))
	copy(sorted, providers)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Priority != sorted[j].Priority {
			return sorted[i].Priority < sorted[j].Priority
		}
		return sorted[i].ID < sorted[j].ID
	})

	return sorted[0]
}

// SelectByRandom returns a random provider from the list.
func SelectByRandom(providers []*model.Provider) *model.Provider {
	if len(providers) == 0 {
		return nil
	}
	return providers[rand.IntN(len(providers))]
}

// clampWeight ensures weight is within valid bounds [1, MaxWeight].
func clampWeight(weight int) int64 {
	if weight <= 0 {
		return 1
	}
	if weight > MaxWeight {
		return MaxWeight
	}
	return int64(weight)
}

// selectByWeightGeneric performs weighted random selection on a slice of items.
// getWeight returns the weight for each item (defaults to 1 if <= 0, capped at MaxWeight).
// Uses int64 arithmetic to prevent integer overflow with large weights.
func selectByWeightGeneric[T any](items []T, getWeight func(T) int) (T, bool) {
	var zero T
	if len(items) == 0 {
		return zero, false
	}

	var totalWeight int64
	for _, item := range items {
		w := clampWeight(getWeight(item))
		// Check for overflow before adding
		if totalWeight > math.MaxInt64-w {
			// Cap at MaxInt64 to prevent overflow
			totalWeight = math.MaxInt64
			break
		}
		totalWeight += w
	}

	// Note: totalWeight is always >= len(items) since each item has weight >= 1
	r := rand.Int64N(totalWeight)
	var cumulative int64
	for _, item := range items {
		w := clampWeight(getWeight(item))
		cumulative += w
		if r < cumulative {
			return item, true
		}
	}

	// Unreachable: r < totalWeight and cumulative reaches totalWeight,
	// so the loop must return before completing. Panic to surface bugs.
	panic("unreachable: weighted selection exhausted without selecting")
}

// SelectByWeight returns a provider using weighted random selection.
// Weight determines probability of selection.
func SelectByWeight(providers []*model.Provider) *model.Provider {
	if len(providers) == 0 {
		return nil
	}
	result, _ := selectByWeightGeneric(providers, func(p *model.Provider) int { return p.Weight })
	return result
}

// SelectProvider applies the given strategy to select a provider.
func SelectProvider(providers []*model.Provider, strategy string) *model.Provider {
	switch strategy {
	case StrategyRandom:
		return SelectByRandom(providers)
	case StrategyWeight:
		return SelectByWeight(providers)
	case StrategyPriority:
		fallthrough
	default:
		return SelectByPriority(providers)
	}
}

// groupCandidate represents a group with its available providers.
type groupCandidate struct {
	GroupID   string
	Priority  int
	Weight    int
	Strategy  string
	Providers []*model.Provider
}

// SelectGroup applies the given strategy to select a group.
func SelectGroup(groups []*groupCandidate, strategy string) *groupCandidate {
	if len(groups) == 0 {
		return nil
	}

	switch strategy {
	case StrategyRandom:
		return groups[rand.IntN(len(groups))]
	case StrategyWeight:
		result, _ := selectByWeightGeneric(groups, func(g *groupCandidate) int { return g.Weight })
		return result
	case StrategyPriority:
		fallthrough
	default:
		// Sort by priority ascending
		sorted := make([]*groupCandidate, len(groups))
		copy(sorted, groups)
		sort.Slice(sorted, func(i, j int) bool {
			if sorted[i].Priority != sorted[j].Priority {
				return sorted[i].Priority < sorted[j].Priority
			}
			return sorted[i].GroupID < sorted[j].GroupID
		})
		return sorted[0]
	}
}
