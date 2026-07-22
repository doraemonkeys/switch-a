package providerauth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"
)

const callbackEndpointShutdownTimeout = 5 * time.Second

var errProviderAuthServiceShutdown = errors.New("provider auth service is shut down")

func (s *Service) beginChatGPTLogin(login pendingLogin) error {
	s.callbackLifecycleMu.Lock()
	defer s.callbackLifecycleMu.Unlock()

	s.mu.Lock()
	if s.shutdown {
		s.mu.Unlock()
		return errProviderAuthServiceShutdown
	}
	s.pruneExpiredSessionsLocked(s.clock.Now())
	s.mu.Unlock()

	// Start is idempotent. Calling it for every new session also repairs an
	// endpoint that stopped unexpectedly between otherwise overlapping logins.
	if err := s.callback.Start(); err != nil {
		return fmt.Errorf("start oauth callback endpoint: %w", err)
	}

	now := s.clock.Now()
	login.expiresAt = now.Add(loginSessionTTL)
	s.mu.Lock()
	s.callbackActive = true
	s.storePendingLoginLocked(login)
	s.syncLoginExpiryTaskLocked(now)
	s.mu.Unlock()
	return nil
}

func (s *Service) syncLoginExpiryTaskLocked(now time.Time) {
	s.loginExpiryEpoch++
	if s.loginExpiryTask != nil {
		s.loginExpiryTask.Stop()
		s.loginExpiryTask = nil
	}
	if s.shutdown || !s.callbackActive || len(s.pendingByLoginID) == 0 {
		return
	}

	var earliest time.Time
	for _, pending := range s.pendingByLoginID {
		if earliest.IsZero() || pending.expiresAt.Before(earliest) {
			earliest = pending.expiresAt
		}
	}
	delay := earliest.Sub(now)
	if delay < 0 {
		delay = 0
	}
	epoch := s.loginExpiryEpoch
	s.loginExpiryTask = s.scheduleAfter(delay, func() {
		s.expireLoginSessions(epoch)
	})
}

func (s *Service) expireLoginSessions(epoch uint64) {
	s.callbackLifecycleMu.Lock()
	defer s.callbackLifecycleMu.Unlock()

	s.mu.Lock()
	if s.shutdown || epoch != s.loginExpiryEpoch {
		s.mu.Unlock()
		return
	}
	s.loginExpiryTask = nil
	now := s.clock.Now()
	s.pruneExpiredSessionsLocked(now)
	shouldStop := s.callbackActive && len(s.pendingByLoginID) == 0
	s.syncLoginExpiryTaskLocked(now)
	s.mu.Unlock()

	if shouldStop {
		if err := s.stopCallbackEndpointWithTimeout(); err != nil {
			s.logger.Warn("failed to stop expired oauth callback endpoint", zap.Error(err))
		}
	}
}

func (s *Service) requestCallbackEndpointReconcile() {
	go func() {
		if err := s.reconcileCallbackEndpoint(); err != nil {
			s.logger.Warn("failed to stop idle oauth callback endpoint", zap.Error(err))
		}
	}()
}

func (s *Service) reconcileCallbackEndpoint() error {
	s.callbackLifecycleMu.Lock()
	defer s.callbackLifecycleMu.Unlock()

	s.mu.Lock()
	if s.shutdown {
		s.mu.Unlock()
		return nil
	}
	now := s.clock.Now()
	s.pruneExpiredSessionsLocked(now)
	shouldStop := s.callbackActive && len(s.pendingByLoginID) == 0
	s.syncLoginExpiryTaskLocked(now)
	s.mu.Unlock()

	if !shouldStop {
		return nil
	}
	return s.stopCallbackEndpointWithTimeout()
}

func (s *Service) stopCallbackEndpointWithTimeout() error {
	ctx, cancel := context.WithTimeout(context.Background(), callbackEndpointShutdownTimeout)
	defer cancel()
	return s.stopCallbackEndpoint(ctx)
}

// stopCallbackEndpoint requires callbackLifecycleMu so a new login cannot race
// between releasing the fixed port and recording the endpoint as inactive.
func (s *Service) stopCallbackEndpoint(ctx context.Context) error {
	err := s.callback.Shutdown(ctx)
	s.mu.Lock()
	s.callbackActive = false
	s.syncLoginExpiryTaskLocked(s.clock.Now())
	s.mu.Unlock()
	return err
}

// Shutdown prevents new login sessions, cancels pending expiry work, and
// releases the callback port if a login flow currently owns it.
func (s *Service) Shutdown(ctx context.Context) error {
	s.callbackLifecycleMu.Lock()
	defer s.callbackLifecycleMu.Unlock()

	s.mu.Lock()
	if s.shutdown {
		s.mu.Unlock()
		return nil
	}
	s.shutdown = true
	s.loginExpiryEpoch++
	if s.loginExpiryTask != nil {
		s.loginExpiryTask.Stop()
		s.loginExpiryTask = nil
	}
	callbackActive := s.callbackActive
	s.mu.Unlock()

	if !callbackActive {
		return nil
	}
	return s.stopCallbackEndpoint(ctx)
}
