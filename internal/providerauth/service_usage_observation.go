package providerauth

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"
	"go.uber.org/zap"
)

const providerUsageObservationMinInterval = 30 * time.Second

// ObserveProviderUsage merges quota facts already carried by a Codex response.
// It never calls the upstream usage endpoint, so normal traffic can keep the
// admin snapshot fresh without a second account request.
func (s *Service) ObserveProviderUsage(
	ctx context.Context,
	providerID string,
	snapshot *model.ProviderUsageSnapshot,
) error {
	providerID = strings.TrimSpace(providerID)
	if providerID == "" || snapshot == nil || snapshot.FetchedAt == nil || snapshot.FetchedAt.IsZero() {
		return nil
	}

	next, owner := s.beginProviderUsageObservation(providerID, snapshot)
	if !owner {
		return nil
	}

	for next != nil {
		if err := s.persistObservedProviderUsage(ctx, providerID, next); err != nil {
			s.finishProviderUsageObservation(providerID)
			return err
		}
		next = s.takeQueuedProviderUsageObservation(providerID)
	}
	return nil
}

func (s *Service) beginProviderUsageObservation(
	providerID string,
	snapshot *model.ProviderUsageSnapshot,
) (*model.ProviderUsageSnapshot, bool) {
	cloned := model.CloneProviderUsageSnapshot(snapshot)
	s.usageObservationMu.Lock()
	defer s.usageObservationMu.Unlock()

	if inFlight := s.inFlightUsageObservations[providerID]; inFlight != nil {
		if usageSnapshotNewer(cloned, inFlight.latest) {
			inFlight.latest = cloned
		}
		return nil, false
	}
	s.inFlightUsageObservations[providerID] = &inFlightProviderUsageObservation{}
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
	providerID string,
	observed *model.ProviderUsageSnapshot,
) error {
	reader, ok := s.credentialStore.(providerCredentialReader)
	if !ok {
		return nil
	}
	ownedCtx, release, err := s.withProviderCredentialMutations(ctx, []string{providerID})
	if err != nil {
		return fmt.Errorf("acquire usage observation mutation for provider %q: %w", providerID, err)
	}
	defer release()

	provider, err := reader.GetProvider(ownedCtx, providerID)
	if err != nil {
		return fmt.Errorf("reload provider %q before usage observation: %w", providerID, err)
	}
	if provider == nil || model.NormalizeProviderCredentialType(provider.CredentialType) != providerCredentialTypeChatGPT {
		return nil
	}

	authState := providerAuthStateSnapshot(provider)
	if authState == nil {
		return nil
	}
	merged, changed, reason := mergeObservedProviderUsage(authState.UsageSnapshot, observed)
	if !changed {
		s.logger.Debug("skipped provider usage response observation",
			zap.String("provider_id", providerID),
			zap.String("reason", reason),
		)
		return nil
	}

	updatedState := authState.Clone()
	updatedState.UsageSnapshot = merged
	if merged.PlanType != "" {
		updatedState.PlanType = merged.PlanType
	}
	if err := s.persistProviderAuthState(ownedCtx, providerID, updatedState); err != nil {
		return err
	}
	s.logger.Debug("persisted provider usage response observation",
		zap.String("provider_id", providerID),
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
