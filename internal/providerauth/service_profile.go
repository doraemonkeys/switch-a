package providerauth

import (
	"context"
	"fmt"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"
	"go.uber.org/zap"
)

// BuildProviderAuthView exposes the pure-read auth projection needed by admin handlers.
func (s *Service) BuildProviderAuthView(provider *model.Provider) *ProviderAuthView {
	return BuildProviderAuthView(provider)
}

// RefreshProviderUsage fetches a fresh usage snapshot without coupling admin read paths
// to token refresh or other implicit write-back behavior.
func (s *Service) RefreshProviderUsage(ctx context.Context, provider *model.Provider) (bool, error) {
	if provider == nil {
		return false, fmt.Errorf("provider is required")
	}
	switch model.NormalizeProviderCredentialType(provider.CredentialType) {
	case providerCredentialTypeChatGPT:
		return true, s.refreshChatGPTUsageSnapshot(ctx, provider)
	default:
		return false, nil
	}
}

func (s *Service) refreshChatGPTUsageSnapshot(ctx context.Context, provider *model.Provider) error {
	if provider == nil {
		return fmt.Errorf("provider is required")
	}
	ownedCtx, release, err := s.withProviderCredentialMutations(ctx, []string{provider.ID})
	if err != nil {
		return fmt.Errorf("acquire credential mutation for provider %q: %w", provider.ID, err)
	}
	defer release()

	reloaded, err := s.reloadProviderForCredentialMutation(ownedCtx, provider)
	if err != nil {
		return err
	}
	if reloaded {
		s.logger.Debug("reloaded provider credential before usage refresh", zap.String("provider_id", provider.ID))
	}
	ctx = ownedCtx

	credential, err := DecodeProviderChatGPTCredential(provider)
	if err != nil {
		return &ProviderAuthStateError{
			ProviderID: provider.ID,
			Status:     ProviderAuthStatusNotConnected,
			Reason:     ProviderAuthReasonCredentialInvalid,
			LastError:  err.Error(),
		}
	}
	if credential == nil || !credential.Ready() {
		return &ProviderAuthStateError{
			ProviderID: provider.ID,
			Status:     ProviderAuthStatusNotConnected,
			Reason:     ProviderAuthReasonLoginRequired,
		}
	}

	authState := providerAuthStateSnapshot(provider)
	if authState.Status != ProviderAuthStatusActive {
		return &ProviderAuthStateError{
			ProviderID: provider.ID,
			Status:     authState.Status,
			Reason:     providerAuthReason(providerCredentialTypeChatGPT, authState),
			LastError:  authState.LastError,
		}
	}

	snapshot, err := s.fetchChatGPTUsageSnapshot(ctx, credential)
	if err != nil {
		if reason, terminal := classifyChatGPTUsageAuthFailure(err); terminal {
			return s.markChatGPTUsageReauthenticationRequired(
				ctx,
				provider,
				credential,
				authState,
				reason,
				err,
			)
		}
		return err
	}

	updatedState := buildChatGPTAuthState(
		provider.ID,
		authState,
		credential,
		ProviderAuthStatusActive,
		"",
		"",
		snapshot,
		time.Time{},
	)
	if err := s.persistProviderAuthState(ctx, provider.ID, updatedState); err != nil {
		return err
	}

	applyProviderAuthState(provider, updatedState)
	return nil
}

func (s *Service) markChatGPTUsageReauthenticationRequired(
	ctx context.Context,
	provider *model.Provider,
	credential *model.ChatGPTProviderCredential,
	current *model.ProviderAuthState,
	reason string,
	cause error,
) error {
	updatedState := buildChatGPTAuthState(
		provider.ID,
		current,
		credential,
		ProviderAuthStatusReauthRequired,
		reason,
		cause.Error(),
		nil,
		s.clock.Now(),
	)
	if err := s.persistProviderAuthState(ctx, provider.ID, updatedState); err != nil {
		return err
	}

	applyProviderAuthState(provider, updatedState)
	return &ProviderAuthStateError{
		ProviderID: provider.ID,
		Status:     ProviderAuthStatusReauthRequired,
		Reason:     reason,
		LastError:  updatedState.LastError,
	}
}

func (s *Service) persistProviderAuthState(
	ctx context.Context,
	providerID string,
	authState *model.ProviderAuthState,
) error {
	if s.credentialStore == nil || providerID == "" {
		return nil
	}
	if err := s.credentialStore.UpdateProviderAuthState(ctx, providerID, authState); err != nil {
		return fmt.Errorf("persist provider auth state for provider %q: %w", providerID, err)
	}
	return nil
}
