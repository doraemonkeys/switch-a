package codexcontinuity

type routeTargetPreferenceState uint8

const (
	routeTargetPreferenceUnseen routeTargetPreferenceState = iota
	routeTargetPreferenceConsistent
	routeTargetPreferenceConflicted
)

// RouteTargetPreference accumulates routing evidence without making the
// traversal order observable. Its zero value is ready to use.
type RouteTargetPreference struct {
	state routeTargetPreferenceState
	value string
}

// Add returns the preference after observing hint. Empty hints provide no
// evidence, while disagreement is permanent so later owners cannot restore a
// preference that the complete owner set does not support.
func (p RouteTargetPreference) Add(hint string) RouteTargetPreference {
	if hint == "" || p.state == routeTargetPreferenceConflicted {
		return p
	}
	if p.state == routeTargetPreferenceUnseen {
		return RouteTargetPreference{
			state: routeTargetPreferenceConsistent,
			value: hint,
		}
	}
	if p.value != hint {
		return RouteTargetPreference{state: routeTargetPreferenceConflicted}
	}
	return p
}

// Value returns the consistent non-empty hint, if the observed evidence has
// exactly one distinct RouteTarget.
func (p RouteTargetPreference) Value() (string, bool) {
	return p.value, p.state == routeTargetPreferenceConsistent
}

// Conflicted reports whether different non-empty hints were observed.
func (p RouteTargetPreference) Conflicted() bool {
	return p.state == routeTargetPreferenceConflicted
}
