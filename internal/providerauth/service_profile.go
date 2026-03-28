package providerauth

import (
	"context"
	"fmt"
	"time"

	"switch-a/internal/model"
)

// BuildProviderAuthView exposes the pure-read auth projection needed by admin handlers.
func (s *Service) BuildProviderAuthView(provider *model.Provider) *ProviderAuthView {
	return BuildProviderAuthView(provider)
}

// RefreshProviderUsage fetches a fresh usage snapshot without coupling admin read paths
// to token refresh or other implicit write-back behavior.
func (s *Service) RefreshProviderUsage(ctx context.Context, provider *model.Provider) (bool, error) {
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
