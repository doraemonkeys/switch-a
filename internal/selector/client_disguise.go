package selector

import (
	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/model"
	"strings"
)

// Platform exclusions describe ordinary candidate filtering, with the original
// platform evidence retained for operators and client configuration links.
type PlatformSelectionError struct{ Exclusions []model.DisguiseExclusion }

func (e *PlatformSelectionError) Error() string {
	reasons := make([]string, 0, len(e.Exclusions))
	for _, entry := range e.Exclusions {
		reasons = append(reasons, entry.ProviderID+": "+entry.Reason)
	}
	return "no provider matches client platform: " + strings.Join(reasons, ", ") + "; settings: /admin/client-disguise"
}
func (e *PlatformSelectionError) Unwrap() error { return internal.ErrNoProvider }
func disguiseSelectionFailure(scope *ProviderSelectionEligibility) error {
	if scope == nil || scope.req == nil || scope.req.ClientDisguise == nil {
		return nil
	}
	exclusions := scope.req.ClientDisguise.Exclusions()
	if len(exclusions) == 0 {
		return nil
	}
	return &PlatformSelectionError{Exclusions: exclusions}
}
