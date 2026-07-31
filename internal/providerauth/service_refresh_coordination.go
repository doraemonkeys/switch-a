package providerauth

import (
	"context"
	"fmt"
	"strings"

	"github.com/doraemonkeys/switch-a/internal/model"
	"go.uber.org/zap"
)

func (s *Service) refreshAndPersistChatGPTCredentialCoordinated(
	ctx context.Context,
	provider *model.Provider,
	credential *model.ChatGPTProviderCredential,
	force bool,
) (*model.ChatGPTProviderCredential, error) {
	ownedCtx, release, err := s.withProviderCredentialMutations(ctx, []string{provider.ID})
	if err != nil {
		return nil, fmt.Errorf("acquire credential mutation for provider %q: %w", provider.ID, err)
	}
	if _, coordinated := s.credentialStore.(providerCredentialMutationCoordinator); coordinated {
		s.logger.Debug("acquired provider credential mutation lease", zap.String("provider_id", provider.ID))
	}
	defer release()

	latestCredential, reloaded, err := s.reloadChatGPTCredentialForRefresh(ownedCtx, provider, credential)
	if err != nil {
		return nil, err
	}
	if reloaded {
		s.logger.Debug("reloaded provider credential before refresh",
			zap.String("provider_id", provider.ID),
			zap.Bool("credential_changed", latestCredential.RefreshToken != credential.RefreshToken),
		)
	}
	if !force && latestCredential.ExpiresAt.After(s.clock.Now().Add(proactiveRefreshWindow)) {
		return latestCredential, nil
	}
	return s.refreshAndPersistChatGPTCredentialDirect(ownedCtx, provider, latestCredential)
}

func (s *Service) withProviderCredentialMutations(
	ctx context.Context,
	providerIDs []string,
) (context.Context, func(), error) {
	normalizedIDs := make([]string, 0, len(providerIDs))
	for _, providerID := range providerIDs {
		if providerID = strings.TrimSpace(providerID); providerID != "" {
			normalizedIDs = append(normalizedIDs, providerID)
		}
	}
	coordinator, ok := s.credentialStore.(providerCredentialMutationCoordinator)
	if !ok || len(normalizedIDs) == 0 {
		return ctx, func() {}, nil
	}
	ownedCtx, release, err := coordinator.WithProviderCredentialMutations(ctx, normalizedIDs)
	if err != nil {
		return nil, nil, err
	}
	if ownedCtx == nil {
		ownedCtx = ctx
	}
	if release == nil {
		release = func() {}
	}
	return ownedCtx, release, nil
}

// InvalidateProviderCredentialSessions prevents an in-process refresh result
// from resurrecting the credential generation that a durable import replaced.
// Callers invoke this while still holding the store mutation lease, after commit.
func (s *Service) InvalidateProviderCredentialSessions(providerIDs []string) {
	s.refreshMu.Lock()
	invalidatedProviders := make(map[string]struct{}, len(providerIDs))
	for _, providerID := range providerIDs {
		providerID = strings.TrimSpace(providerID)
		if providerID == "" {
			continue
		}
		if _, exists := s.recentChatGPTRefreshes[providerID]; exists {
			delete(s.recentChatGPTRefreshes, providerID)
			invalidatedProviders[providerID] = struct{}{}
		}
		// Existing waiters retain their call pointer and may finish normally, while
		// requests that start after commit must not join the superseded generation.
		if _, exists := s.inFlightRefreshes[providerID]; exists {
			delete(s.inFlightRefreshes, providerID)
			invalidatedProviders[providerID] = struct{}{}
		}
	}
	s.refreshMu.Unlock()

	s.logger.Debug("invalidated provider credential sessions",
		zap.Int("requested_provider_count", len(providerIDs)),
		zap.Int("invalidated_provider_count", len(invalidatedProviders)),
	)
}

func (s *Service) reloadProviderForCredentialMutation(ctx context.Context, provider *model.Provider) (bool, error) {
	reader, ok := s.credentialStore.(providerCredentialReader)
	if !ok || provider == nil || strings.TrimSpace(provider.ID) == "" {
		return false, nil
	}

	latestProvider, err := reader.GetProvider(ctx, provider.ID)
	if err != nil {
		return false, fmt.Errorf("reload provider %q before credential mutation: %w", provider.ID, err)
	}
	if latestProvider == nil {
		return false, fmt.Errorf("reload provider %q before credential mutation: provider is missing", provider.ID)
	}
	if model.NormalizeProviderCredentialType(latestProvider.CredentialType) != providerCredentialTypeChatGPT {
		return false, &ProviderAuthStateError{
			ProviderID: provider.ID,
			Status:     ProviderAuthStatusNotConnected,
			Reason:     ProviderAuthReasonCredentialInvalid,
		}
	}

	provider.CredentialType = latestProvider.CredentialType
	provider.Credential = latestProvider.Credential.Clone()
	provider.AuthState = latestProvider.AuthState.Clone()
	return true, nil
}

func (s *Service) reloadChatGPTCredentialForRefresh(
	ctx context.Context,
	provider *model.Provider,
	fallback *model.ChatGPTProviderCredential,
) (*model.ChatGPTProviderCredential, bool, error) {
	if _, ok := s.credentialStore.(providerCredentialReader); !ok || strings.TrimSpace(provider.ID) == "" {
		return fallback, false, nil
	}

	reloaded, err := s.reloadProviderForCredentialMutation(ctx, provider)
	if err != nil {
		return nil, false, err
	}

	latestCredential, err := DecodeProviderChatGPTCredential(provider)
	if err != nil || latestCredential == nil || !latestCredential.Ready() {
		lastError := ""
		if err != nil {
			lastError = err.Error()
		}
		return nil, false, &ProviderAuthStateError{
			ProviderID: provider.ID,
			Status:     ProviderAuthStatusNotConnected,
			Reason:     ProviderAuthReasonCredentialInvalid,
			LastError:  lastError,
		}
	}
	authState := providerAuthStateSnapshot(provider)
	if authState.Status != ProviderAuthStatusActive {
		return nil, false, &ProviderAuthStateError{
			ProviderID: provider.ID,
			Status:     authState.Status,
			Reason:     providerAuthReason(providerCredentialTypeChatGPT, authState),
			LastError:  authState.LastError,
		}
	}

	return latestCredential, reloaded, nil
}
