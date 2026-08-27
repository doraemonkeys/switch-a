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

func (s *Service) refreshAndPersistChatGPTCredentialCoordinated(
	ctx context.Context,
	routeTargetID string,
	snapshot *credentialsession.Snapshot,
	credential *model.ChatGPTProviderCredential,
	force bool,
) (*model.ChatGPTProviderCredential, error) {
	if snapshot == nil {
		return nil, fmt.Errorf("credential session snapshot is required")
	}
	ownedCtx, release, err := s.withCredentialSessionMutations(ctx, []string{snapshot.SessionID})
	if err != nil {
		return nil, fmt.Errorf("acquire credential mutation for session %q: %w", snapshot.SessionID, err)
	}
	defer release()

	latestSnapshot, latestCredential, err := s.reloadChatGPTCredentialSession(ownedCtx, routeTargetID, snapshot.SessionID)
	if err != nil {
		return nil, err
	}
	s.logger.Debug("reloaded credential session before refresh",
		zap.String("route_target_id", routeTargetID),
		zap.String("session_id", snapshot.SessionID),
		zap.Int64("version", latestSnapshot.Version),
		zap.Bool("credential_changed", latestCredential.RefreshToken != credential.RefreshToken),
	)
	if !force && latestCredential.ExpiresAt.After(s.clock.Now().Add(proactiveRefreshWindow)) {
		return latestCredential, nil
	}
	return s.refreshAndPersistChatGPTCredentialDirect(ownedCtx, routeTargetID, &latestSnapshot, latestCredential)
}

func (s *Service) withCredentialSessionMutations(
	ctx context.Context,
	sessionIDs []string,
) (context.Context, func(), error) {
	store, ok := s.credentialStore.(CredentialStore)
	if !ok {
		return nil, nil, fmt.Errorf("credential session store is unavailable")
	}
	normalized := make([]string, 0, len(sessionIDs))
	for _, sessionID := range sessionIDs {
		if sessionID = strings.TrimSpace(sessionID); sessionID != "" {
			normalized = append(normalized, sessionID)
		}
	}
	if len(normalized) == 0 {
		return ctx, func() {}, nil
	}
	return store.WithCredentialSessionMutations(ctx, normalized)
}

// InvalidateCredentialSessions invalidates refresh generations by SessionID;
// route-target IDs are never accepted as cache keys.
func (s *Service) InvalidateCredentialSessions(sessionIDs []string) {
	s.refreshMu.Lock()
	invalidated := 0
	for _, sessionID := range sessionIDs {
		sessionID = strings.TrimSpace(sessionID)
		if sessionID == "" {
			continue
		}
		if _, exists := s.recentChatGPTRefreshes[sessionID]; exists {
			delete(s.recentChatGPTRefreshes, sessionID)
			invalidated++
		}
		if _, exists := s.inFlightRefreshes[sessionID]; exists {
			delete(s.inFlightRefreshes, sessionID)
			invalidated++
		}
	}
	s.refreshMu.Unlock()
	s.logger.Debug("invalidated credential session refresh generations",
		zap.Int("requested_session_count", len(sessionIDs)),
		zap.Int("invalidated_generation_count", invalidated),
	)
}

func (s *Service) reloadChatGPTCredentialSession(
	ctx context.Context,
	routeTargetID string,
	sessionID string,
) (credentialsession.Snapshot, *model.ChatGPTProviderCredential, error) {
	store, ok := s.credentialStore.(CredentialStore)
	if !ok {
		return credentialsession.Snapshot{}, nil, fmt.Errorf("credential session store is unavailable")
	}
	session, err := store.GetCredentialSession(ctx, sessionID)
	if err != nil {
		return credentialsession.Snapshot{}, nil, fmt.Errorf("reload credential session %q: %w", sessionID, err)
	}
	snapshot, err := session.Snapshot()
	if err != nil {
		return credentialsession.Snapshot{}, nil, err
	}
	if snapshot.Kind != credentialsession.KindChatGPT {
		return credentialsession.Snapshot{}, nil, &ProviderAuthStateError{
			ProviderID: routeTargetID,
			SessionID:  snapshot.SessionID,
			Status:     ProviderAuthStatusNotConnected,
			Reason:     ProviderAuthReasonCredentialInvalid,
		}
	}
	credential, err := decodeChatGPTCredentialSession(&snapshot)
	if err != nil || credential == nil || !credential.Ready() {
		lastError := ""
		if err != nil {
			lastError = err.Error()
		}
		return credentialsession.Snapshot{}, nil, &ProviderAuthStateError{
			ProviderID: routeTargetID,
			SessionID:  snapshot.SessionID,
			Status:     ProviderAuthStatusNotConnected,
			Reason:     ProviderAuthReasonCredentialInvalid,
			LastError:  lastError,
		}
	}
	if snapshot.AuthState.Status != credentialsession.AuthStatusActive {
		return credentialsession.Snapshot{}, nil, &ProviderAuthStateError{
			ProviderID: routeTargetID,
			SessionID:  snapshot.SessionID,
			Status:     snapshot.AuthState.Status,
			Reason:     snapshot.AuthState.StatusReason,
			LastError:  snapshot.AuthState.LastError,
		}
	}
	return snapshot, credential, nil
}

// RefreshCredentialSession makes the mutation owner explicit for admin callers.
func (s *Service) RefreshCredentialSession(ctx context.Context, snapshot credentialsession.Snapshot) (bool, error) {
	if snapshot.Kind != credentialsession.KindChatGPT {
		return false, nil
	}
	_, err := s.ensureFreshChatGPTSessionCredential(ctx, "", &snapshot, true)
	return true, err
}

func (s *Service) ensureFreshChatGPTSessionCredential(ctx context.Context, routeTargetID string, snapshot *credentialsession.Snapshot, force bool) (*model.ChatGPTProviderCredential, error) {
	credential, err := s.validatedChatGPTSessionCredential(routeTargetID, snapshot)
	if err != nil {
		return nil, err
	}
	return s.ensureFreshValidatedChatGPTSessionCredential(ctx, routeTargetID, snapshot, credential, force)
}

func (s *Service) validatedChatGPTSessionCredential(routeTargetID string, snapshot *credentialsession.Snapshot) (*model.ChatGPTProviderCredential, error) {
	if snapshot == nil || strings.TrimSpace(snapshot.SessionID) == "" {
		return nil, fmt.Errorf("credential session is required")
	}

	credential, err := decodeChatGPTCredentialSession(snapshot)
	if err != nil {
		return nil, &ProviderAuthStateError{
			ProviderID: routeTargetID,
			SessionID:  snapshot.SessionID,
			Status:     ProviderAuthStatusNotConnected,
			Reason:     ProviderAuthReasonCredentialInvalid,
			LastError:  err.Error(),
		}
	}
	if credential == nil {
		return nil, &ProviderAuthStateError{
			ProviderID: routeTargetID,
			SessionID:  snapshot.SessionID,
			Status:     ProviderAuthStatusNotConnected,
			Reason:     ProviderAuthReasonLoginRequired,
		}
	}

	authState := snapshot.AuthState
	if authState.Status != credentialsession.AuthStatusActive {
		return nil, &ProviderAuthStateError{
			ProviderID: routeTargetID,
			SessionID:  snapshot.SessionID,
			Status:     authState.Status,
			Reason:     authState.StatusReason,
			LastError:  authState.LastError,
		}
	}

	decodedCredential := cloneChatGPTCredential(credential)
	credential = s.reuseRecentChatGPTRefresh(snapshot.SessionID, decodedCredential)
	if !credential.Ready() {
		return nil, &ProviderAuthStateError{
			ProviderID: routeTargetID,
			SessionID:  snapshot.SessionID,
			Status:     ProviderAuthStatusNotConnected,
			Reason:     ProviderAuthReasonCredentialInvalid,
		}
	}
	return credential, nil
}

func (s *Service) ensureFreshValidatedChatGPTSessionCredential(
	ctx context.Context,
	routeTargetID string,
	snapshot *credentialsession.Snapshot,
	credential *model.ChatGPTProviderCredential,
	force bool,
) (*model.ChatGPTProviderCredential, error) {
	now := s.clock.Now()
	if !force && credential.ExpiresAt.After(now.Add(proactiveRefreshWindow)) {
		return credential, nil
	}

	refreshed, err := s.refreshAndPersistChatGPTCredential(ctx, routeTargetID, snapshot, credential, force)
	if err != nil {
		return nil, err
	}
	return refreshed, nil
}

func (s *Service) refreshAndPersistChatGPTCredential(
	ctx context.Context,
	routeTargetID string,
	snapshot *credentialsession.Snapshot,
	credential *model.ChatGPTProviderCredential,
	force bool,
) (*model.ChatGPTProviderCredential, error) {
	refreshKey := strings.TrimSpace(snapshot.SessionID)
	if refreshKey == "" {
		return nil, fmt.Errorf("credential session ID is required")
	}

	call, leader := s.beginChatGPTRefresh(refreshKey)
	if leader {
		refreshed, err := s.refreshAndPersistChatGPTCredentialCoordinated(ctx, routeTargetID, snapshot, credential, force)
		s.finishChatGPTRefresh(refreshKey, call, refreshed, err)
		return refreshed, err
	}

	select {
	case <-call.done:
		return cloneChatGPTCredential(call.credential), call.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *Service) refreshAndPersistChatGPTCredentialDirect(
	ctx context.Context,
	routeTargetID string,
	snapshot *credentialsession.Snapshot,
	credential *model.ChatGPTProviderCredential,
) (*model.ChatGPTProviderCredential, error) {
	refreshed, err := s.refreshChatGPTCredential(ctx, credential)
	if err != nil {
		return nil, s.persistChatGPTRefreshFailure(ctx, routeTargetID, snapshot, credential, err)
	}
	if err := s.persistChatGPTCredentialSession(ctx, snapshot, refreshed); err != nil {
		return nil, err
	}
	s.storeRecentChatGPTRefresh(snapshot.SessionID, refreshed)
	return refreshed, nil
}

func (s *Service) beginChatGPTRefresh(sessionID string) (*inFlightChatGPTRefresh, bool) {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()

	s.pruneRecentChatGPTRefreshesLocked(s.clock.Now())
	if call, ok := s.inFlightRefreshes[sessionID]; ok {
		return call, false
	}

	// Refresh tokens are single-use, so concurrent callers for the same session
	// must collapse onto one outbound refresh exchange.
	call := &inFlightChatGPTRefresh{done: make(chan struct{})}
	s.inFlightRefreshes[sessionID] = call
	return call, true
}

func (s *Service) finishChatGPTRefresh(
	sessionID string,
	call *inFlightChatGPTRefresh,
	credential *model.ChatGPTProviderCredential,
	err error,
) {
	call.credential = cloneChatGPTCredential(credential)
	call.err = err
	close(call.done)

	s.refreshMu.Lock()
	// Import invalidation may have detached this generation so post-commit callers
	// can start against the newly persisted credential. An old leader must not
	// erase a replacement call that acquired the same provider key.
	if current, exists := s.inFlightRefreshes[sessionID]; exists && current == call {
		delete(s.inFlightRefreshes, sessionID)
	}
	s.refreshMu.Unlock()
}

func (s *Service) storeRecentChatGPTRefresh(sessionID string, credential *model.ChatGPTProviderCredential) {
	if strings.TrimSpace(sessionID) == "" || credential == nil {
		return
	}

	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()

	now := s.clock.Now()
	s.pruneRecentChatGPTRefreshesLocked(now)
	s.recentChatGPTRefreshes[sessionID] = recentChatGPTRefresh{
		credential: cloneChatGPTCredential(credential),
		expiresAt:  now.Add(recentRefreshReuseWindow),
	}
}

func (s *Service) reuseRecentChatGPTRefresh(
	sessionID string,
	credential *model.ChatGPTProviderCredential,
) *model.ChatGPTProviderCredential {
	if strings.TrimSpace(sessionID) == "" || credential == nil {
		return credential
	}

	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()

	now := s.clock.Now()
	s.pruneRecentChatGPTRefreshesLocked(now)
	recent, ok := s.recentChatGPTRefreshes[sessionID]
	if !ok || !shouldPreferChatGPTCredential(recent.credential, credential) {
		return credential
	}
	// Requests can hold a session snapshot that predates a successful refresh.
	// Reusing the newest in-process credential prevents an immediate retry with the
	// just-invalidated refresh token before the next store read catches up.
	return cloneChatGPTCredential(recent.credential)
}

func (s *Service) pruneRecentChatGPTRefreshesLocked(now time.Time) {
	for sessionID, recent := range s.recentChatGPTRefreshes {
		if !recent.expiresAt.After(now) {
			delete(s.recentChatGPTRefreshes, sessionID)
		}
	}
}

func shouldPreferChatGPTCredential(
	candidate *model.ChatGPTProviderCredential,
	current *model.ChatGPTProviderCredential,
) bool {
	if candidate == nil || !candidate.Ready() {
		return false
	}
	if current == nil {
		return true
	}
	// Defensive account comparison makes subject invariance explicit even if a
	// corrupt store were to reuse a session ID for another account.
	if candidate.AccountID != current.AccountID {
		return false
	}
	if !current.Ready() {
		return true
	}
	if candidate.LastRefresh.After(current.LastRefresh) {
		return true
	}
	if candidate.LastRefresh.Equal(current.LastRefresh) && candidate.ExpiresAt.After(current.ExpiresAt) {
		return true
	}
	if candidate.RefreshToken == current.RefreshToken && candidate.ExpiresAt.After(current.ExpiresAt) {
		return true
	}
	return false
}

func cloneChatGPTCredential(credential *model.ChatGPTProviderCredential) *model.ChatGPTProviderCredential {
	if credential == nil {
		return nil
	}

	cloned := *credential
	cloned.Usage = cloneProviderUsageSnapshot(credential.Usage)
	return &cloned
}

func (s *Service) persistChatGPTRefreshFailure(
	ctx context.Context,
	routeTargetID string,
	snapshot *credentialsession.Snapshot,
	credential *model.ChatGPTProviderCredential,
	refreshErr error,
) error {
	reason, terminal := classifyChatGPTRefreshFailure(refreshErr)
	if snapshot == nil {
		return refreshErr
	}

	now := s.clock.Now()
	authState := snapshot.AuthState.Clone()
	failureAt := now.UTC()

	if terminal {
		authState.Status = credentialsession.AuthStatusReauthRequired
		authState.StatusReason = reason
		authState.LastTransitionAt = timePointer(now)
	} else {
		authState.Status = snapshot.AuthState.Status
	}
	authState.RefreshFailCount++
	authState.LastRefreshFailureAt = &failureAt
	authState.LastError = strings.TrimSpace(refreshErr.Error())
	store, ok := s.credentialStore.(CredentialStore)
	if !ok {
		return fmt.Errorf("credential session store is unavailable")
	}
	if err := store.UpdateCredentialSessionAuthState(ctx, snapshot.SessionID, authState); err != nil {
		return err
	}

	if !terminal {
		return refreshErr
	}
	return &ProviderAuthStateError{
		ProviderID: routeTargetID,
		SessionID:  snapshot.SessionID,
		Status:     ProviderAuthStatusReauthRequired,
		Reason:     reason,
		LastError:  authState.LastError,
	}
}

func classifyChatGPTRefreshFailure(err error) (string, bool) {
	if err == nil {
		return "", false
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, ProviderAuthReasonRefreshTokenReused):
		return ProviderAuthReasonRefreshTokenReused, true
	case strings.Contains(message, ProviderAuthReasonInvalidGrant):
		return ProviderAuthReasonInvalidGrant, true
	case strings.Contains(message, ProviderAuthReasonInteractionRequired),
		strings.Contains(message, "login_required"),
		strings.Contains(message, "consent_required"),
		strings.Contains(message, "reauth"),
		strings.Contains(message, "re-auth"):
		return ProviderAuthReasonInteractionRequired, true
	default:
		return "", false
	}
}
