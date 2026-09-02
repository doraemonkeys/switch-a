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
	s.syncSessionExpiryTaskLocked(now)
	s.mu.Unlock()
	return nil
}

func (s *Service) syncSessionExpiryTaskLocked(now time.Time) {
	s.sessionExpiryEpoch++
	if s.sessionExpiryTask != nil {
		s.sessionExpiryTask.Stop()
		s.sessionExpiryTask = nil
	}
	if s.shutdown {
		return
	}

	var earliest time.Time
	for _, pending := range s.pendingByLoginID {
		if earliest.IsZero() || pending.expiresAt.Before(earliest) {
			earliest = pending.expiresAt
		}
	}
	for _, completed := range s.completed {
		if earliest.IsZero() || completed.expiresAt.Before(earliest) {
			earliest = completed.expiresAt
		}
	}
	for _, providerImport := range s.providerImports {
		if providerImport.claimed {
			continue
		}
		if earliest.IsZero() || providerImport.expiresAt.Before(earliest) {
			earliest = providerImport.expiresAt
		}
	}
	if earliest.IsZero() {
		return
	}
	delay := max(earliest.Sub(now), 0)
	epoch := s.sessionExpiryEpoch
	s.sessionExpiryTask = s.scheduleAfter(delay, func() {
		s.expireSessions(epoch)
	})
}

func (s *Service) expireSessions(epoch uint64) {
	s.callbackLifecycleMu.Lock()
	defer s.callbackLifecycleMu.Unlock()

	s.mu.Lock()
	if s.shutdown || epoch != s.sessionExpiryEpoch {
		s.mu.Unlock()
		return
	}
	s.sessionExpiryTask = nil
	now := s.clock.Now()
	s.pruneExpiredSessionsLocked(now)
	shouldStop := s.callbackActive && len(s.pendingByLoginID) == 0
	s.syncSessionExpiryTaskLocked(now)
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
	s.syncSessionExpiryTaskLocked(now)
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
	s.syncSessionExpiryTaskLocked(s.clock.Now())
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
	s.sessionExpiryEpoch++
	if s.sessionExpiryTask != nil {
		s.sessionExpiryTask.Stop()
		s.sessionExpiryTask = nil
	}
	clear(s.pendingByState)
	clear(s.pendingByLoginID)
	clear(s.completed)
	for importID := range s.providerImports {
		s.deleteChatGPTProviderImportLocked(importID)
	}
	callbackActive := s.callbackActive
	s.mu.Unlock()

	if !callbackActive {
		return nil
	}
	return s.stopCallbackEndpoint(ctx)
}
