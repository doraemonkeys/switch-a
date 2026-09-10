package officialversion

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

type Store interface {
	OfficialVersion(context.Context) (State, error)
	SaveOfficialVersion(context.Context, State) error
	HasOfficialVersionFollowers(context.Context) (bool, error)
}
type Fetcher interface {
	Latest(context.Context) (Release, error)
}

type Service struct {
	store   Store
	fetcher Fetcher
	logger  *zap.Logger
	now     func() time.Time
	mu      sync.Mutex
}

func NewService(store Store, fetcher Fetcher, logger *zap.Logger) *Service {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Service{store: store, fetcher: fetcher, logger: logger, now: func() time.Time { return time.Now().UTC() }}
}
func (s *Service) State(ctx context.Context) (State, error) { return s.store.OfficialVersion(ctx) }

func (s *Service) Sync(ctx context.Context, force bool) (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.store.OfficialVersion(ctx)
	if err != nil {
		return state, err
	}
	if !force {
		enabled, err := s.store.HasOfficialVersionFollowers(ctx)
		if err != nil {
			return state, err
		}
		interval := SyncInterval
		if state.LastError != "" {
			interval = RetryInterval
		}
		if !enabled || !state.CheckedAt.IsZero() && s.now().Sub(state.CheckedAt) < interval {
			return state, nil
		}
	}
	operationID := uuid.NewString()
	s.logger.Debug("client_disguise.official_version_started", zap.String("operation_id", operationID), zap.Bool("manual", force))
	fetchCtx, cancel := context.WithTimeout(ctx, RequestTimeout)
	defer cancel()
	release, fetchErr := s.fetcher.Latest(fetchCtx)
	if fetchErr == nil && (!ValidVersion(release.Version) || release.Tag != tagPrefix+release.Version) {
		fetchErr = fmt.Errorf("invalid official stable release %q", release.Tag)
	}
	state.CheckedAt = s.now()
	state.LastError = ""
	previous := state.Release.Version
	if fetchErr != nil {
		state.LastError = fetchErr.Error()
	} else {
		// Synchronization failures or older release responses cannot erase a working
		// version. Choosing stable may initially replace a newer sampled alpha.
		if Compare(release.Version, previous) >= 0 {
			state.Release = release
		}
		state.SyncedAt = state.CheckedAt
	}
	saveErr := s.store.SaveOfficialVersion(ctx, state)
	err = errors.Join(fetchErr, saveErr)
	fields := []zap.Field{zap.String("operation_id", operationID), zap.String("previous_version", previous), zap.String("version", state.Release.Version), zap.Bool("manual", force)}
	if err != nil {
		s.logger.Warn("client_disguise.official_version_failed", append(fields, zap.Error(err))...)
	} else {
		s.logger.Info("client_disguise.official_version_checked", fields...)
	}
	return state, err
}

// The owner supplies cancellation; no request path fetches GitHub releases.
func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(PollInterval)
	defer ticker.Stop()
	for {
		if _, err := s.Sync(ctx, false); err != nil && ctx.Err() == nil {
			s.logger.Debug("client_disguise.official_version_poll_failed", zap.Error(err))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
