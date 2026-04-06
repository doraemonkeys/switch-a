package proxy

import (
	"time"

	"switch-a/internal/model"
)

// Keep the attempt cutover boundary explicit in process memory so tests, mocks,
// and alternate stores never depend on database defaults to distinguish
// normalized evidence from legacy rows.
func newNormalizedRequestAttempt(requestID, providerID string, createdAt time.Time) model.RequestAttempt {
	return model.RequestAttempt{
		RequestID:        requestID,
		ProviderID:       providerID,
		SemanticsVersion: model.RequestSemanticsVersionNormalizedV1,
		CreatedAt:        createdAt,
	}
}
