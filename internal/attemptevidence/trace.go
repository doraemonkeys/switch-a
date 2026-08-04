package attemptevidence

type Milestone string

const (
	MilestoneProbeReleased     Milestone = "internal_error.probe_released"
	MilestoneRuleMatched       Milestone = "internal_error.rule_matched"
	MilestoneDecision          Milestone = "internal_error.decision"
	MilestoneResponseFinalized Milestone = "internal_error.response_finalized"
	MilestoneHealthVerdict     Milestone = "internal_error.health_verdict"
)

var milestoneOrder = [...]Milestone{
	MilestoneProbeReleased,
	MilestoneRuleMatched,
	MilestoneDecision,
	MilestoneResponseFinalized,
	MilestoneHealthVerdict,
}

// TraceEvent repeats the bounded decision context on every milestone. This
// small redundancy lets operators diagnose a single sampled event without
// joining mutable state or logging raw upstream values.
type TraceEvent struct {
	Name     Milestone
	Semantic SemanticError
}

func TraceEvents(semantic SemanticError) []TraceEvent {
	events := make([]TraceEvent, len(milestoneOrder))
	for index, name := range milestoneOrder {
		events[index] = TraceEvent{Name: name, Semantic: semantic}
	}
	return events
}
