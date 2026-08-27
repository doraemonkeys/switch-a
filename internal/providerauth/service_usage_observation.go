package providerauth

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/model"
	"go.uber.org/zap"
)

const providerUsageObservationMinInterval = 30 * time.Second

// ObserveCredentialSessionUsage merges quota facts already carried by a Codex response.
// It never calls the upstream usage endpoint, so normal traffic can keep the
// admin snapshot fresh without a second account request.
func (s *Service) ObserveCredentialSessionUsage(
	ctx context.Context,
	sessionID string,
	snapshot *model.ProviderUsageSnapshot,
) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || snapshot == nil || snapshot.FetchedAt == nil || snapshot.FetchedAt.IsZero() {
		return nil
	}
	next, owner := s.beginProviderUsageObservation(sessionID, snapshot)
	if !owner {
		return nil
	}

	for next != nil {
		if err := s.persistObservedProviderUsage(ctx, sessionID, next); err != nil {
			s.finishProviderUsageObservation(sessionID)
			return err
		}
		next = s.takeQueuedProviderUsageObservation(sessionID)
	}
	return nil
}

func (s *Service) beginProviderUsageObservation(
	sessionID string,
	snapshot *model.ProviderUsageSnapshot,
) (*model.ProviderUsageSnapshot, bool) {
	cloned := model.CloneProviderUsageSnapshot(snapshot)
	s.usageObservationMu.Lock()
	defer s.usageObservationMu.Unlock()

	if inFlight := s.inFlightUsageObservations[sessionID]; inFlight != nil {
		if usageSnapshotNewer(cloned, inFlight.latest) {
			inFlight.latest = cloned
		}
		return nil, false
	}
	s.inFlightUsageObservations[sessionID] = &inFlightProviderUsageObservation{}
	return cloned, true
}

func (s *Service) takeQueuedProviderUsageObservation(providerID string) *model.ProviderUsageSnapshot {
	s.usageObservationMu.Lock()
	defer s.usageObservationMu.Unlock()
	if inFlight := s.inFlightUsageObservations[providerID]; inFlight != nil {
		next := inFlight.latest
		if next == nil {
			// Deleting while holding the same lock used by producers prevents an
			// observation from being queued between an empty check and cleanup.
			delete(s.inFlightUsageObservations, providerID)
			return nil
		}
		inFlight.latest = nil
		return next
	}
	return nil
}

func (s *Service) finishProviderUsageObservation(providerID string) {
	s.usageObservationMu.Lock()
	delete(s.inFlightUsageObservations, providerID)
	s.usageObservationMu.Unlock()
}

func (s *Service) persistObservedProviderUsage(
	ctx context.Context,
	sessionID string,
	observed *model.ProviderUsageSnapshot,
) error {
	store, ok := s.credentialStore.(CredentialStore)
	if !ok {
		return nil
	}
	ownedCtx, release, err := s.withCredentialSessionMutations(ctx, []string{sessionID})
	if err != nil {
		return fmt.Errorf("acquire usage observation mutation for session %q: %w", sessionID, err)
	}
	defer release()

	session, err := store.GetCredentialSession(ownedCtx, sessionID)
	if err != nil {
		return fmt.Errorf("reload credential session %q before usage observation: %w", sessionID, err)
	}
	if session == nil || session.Kind != credentialsession.KindChatGPT {
		return nil
	}

	merged, changed, reason := mergeObservedProviderUsage(providerUsageSnapshot(session.AuthState.UsageSnapshot), observed)
	if !changed {
		s.logger.Debug("skipped provider usage response observation",
			zap.String("session_id", sessionID),
			zap.String("reason", reason),
		)
		return nil
	}

	updatedState := session.AuthState.Clone()
	updatedState.UsageSnapshot = credentialSessionUsageSnapshot(merged)
	if merged.PlanType != "" {
		updatedState.PlanType = merged.PlanType
	}
	if err := s.persistCredentialSessionAuthState(ownedCtx, sessionID, updatedState); err != nil {
		return err
	}
	s.logger.Debug("persisted provider usage response observation",
		zap.String("session_id", sessionID),
		zap.Timep("fetched_at", merged.FetchedAt),
		zap.Bool("primary_window_observed", observed.FiveHour != nil),
		zap.Bool("secondary_window_observed", observed.OneWeek != nil),
	)
	return nil
}

func mergeObservedProviderUsage(
	current *model.ProviderUsageSnapshot,
	observed *model.ProviderUsageSnapshot,
) (*model.ProviderUsageSnapshot, bool, string) {
	if observed == nil || observed.FetchedAt == nil || observed.FetchedAt.IsZero() {
		return model.CloneProviderUsageSnapshot(current), false, "missing_observation_time"
	}
	if current != nil && current.FetchedAt != nil {
		if !observed.FetchedAt.After(*current.FetchedAt) {
			return model.CloneProviderUsageSnapshot(current), false, "stale_observation"
		}
		if observed.FetchedAt.Sub(*current.FetchedAt) < providerUsageObservationMinInterval {
			return model.CloneProviderUsageSnapshot(current), false, "observation_interval"
		}
	}

	merged := model.CloneProviderUsageSnapshot(current)
	if merged == nil {
		merged = &model.ProviderUsageSnapshot{}
	}
	merged.FetchedAt = cloneUsageObservationTime(observed.FetchedAt)
	if planType := strings.TrimSpace(observed.PlanType); planType != "" {
		merged.PlanType = planType
	}
	if observed.FiveHour != nil {
		merged.FiveHour = cloneProviderUsageWindow(observed.FiveHour)
	}
	if observed.OneWeek != nil {
		merged.OneWeek = cloneProviderUsageWindow(observed.OneWeek)
	}
	return merged, true, ""
}

func usageSnapshotNewer(candidate, current *model.ProviderUsageSnapshot) bool {
	if candidate == nil || candidate.FetchedAt == nil {
		return false
	}
	return current == nil || current.FetchedAt == nil || candidate.FetchedAt.After(*current.FetchedAt)
}

func cloneUsageObservationTime(source *time.Time) *time.Time {
	if source == nil {
		return nil
	}
	value := source.UTC()
	return &value
}
