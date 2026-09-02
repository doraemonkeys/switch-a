package selector

import (
	"math/rand/v2"
	"sort"

	"github.com/doraemonkeys/switch-a/internal/model"
)

type rootCandidateKind string

const (
	rootCandidateExplicitGroup      rootCandidateKind = "explicit_group"
	rootCandidateStandaloneProvider rootCandidateKind = "standalone_provider"
)

func (kind rootCandidateKind) String() string {
	return string(kind)
}

// rootCandidate is deliberately a tagged union. Treating standalone providers
// as synthetic groups erases their priority and weight before selection.
type rootCandidate struct {
	kind               rootCandidateKind
	explicitGroup      *groupCandidate
	standaloneProvider *model.Provider
}

func newExplicitGroupRootCandidate(group *groupCandidate) *rootCandidate {
	return &rootCandidate{
		kind:          rootCandidateExplicitGroup,
		explicitGroup: group,
	}
}

func newStandaloneRootCandidate(provider *model.Provider) *rootCandidate {
	return &rootCandidate{
		kind:               rootCandidateStandaloneProvider,
		standaloneProvider: provider,
	}
}

func (c *rootCandidate) id() string {
	if c == nil {
		return ""
	}
	if c.kind == rootCandidateStandaloneProvider {
		return c.standaloneProvider.ID
	}
	return c.explicitGroup.GroupID
}

func (c *rootCandidate) priority() int {
	if c.kind == rootCandidateStandaloneProvider {
		return c.standaloneProvider.Priority
	}
	return c.explicitGroup.Priority
}

func (c *rootCandidate) weight() int {
	if c.kind == rootCandidateStandaloneProvider {
		return c.standaloneProvider.Weight
	}
	return c.explicitGroup.Weight
}

func selectRootCandidate(candidates []*rootCandidate, strategy string) *rootCandidate {
	if len(candidates) == 0 {
		return nil
	}

	switch strategy {
	case StrategyRandom:
		return candidates[rand.IntN(len(candidates))]
	case StrategyWeight:
		result, _ := selectByWeightGeneric(candidates, func(candidate *rootCandidate) int {
			return candidate.weight()
		})
		return result
	case StrategyPriority:
		fallthrough
	default:
		sorted := append([]*rootCandidate(nil), candidates...)
		sort.Slice(sorted, func(i, j int) bool {
			if sorted[i].priority() != sorted[j].priority() {
				return sorted[i].priority() < sorted[j].priority()
			}
			if sorted[i].id() != sorted[j].id() {
				return sorted[i].id() < sorted[j].id()
			}
			return sorted[i].kind < sorted[j].kind
		})
		return sorted[0]
	}
}

func removeRootCandidate(candidates []*rootCandidate, selected *rootCandidate) []*rootCandidate {
	result := make([]*rootCandidate, 0, len(candidates)-1)
	for _, candidate := range candidates {
		if candidate != selected {
			result = append(result, candidate)
		}
	}
	return result
}
