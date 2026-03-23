package providerauth

import (
	"context"

	"switch-a/internal/model"

	"go.uber.org/zap"
)

func (s *Service) PopulateProviderAuthProfile(ctx context.Context, provider *model.Provider) {
	if provider == nil {
		return
	}

	provider.CredentialType = model.NormalizeProviderCredentialType(provider.CredentialType)
	if provider.CredentialType != providerCredentialTypeChatGPT {
		provider.AuthProfile = BuildAuthProfile(provider)
		return
	}

	credential, err := s.ensureFreshChatGPTCredential(ctx, provider, false)
	if err != nil {
		s.logger.Debug("failed to refresh chatgpt credential while preparing provider auth profile",
			zap.String("provider_id", provider.ID),
			zap.Error(err),
		)
		provider.AuthProfile = BuildAuthProfile(provider)
		return
	}

	credential = s.refreshChatGPTUsageProfile(ctx, provider, credential)
	provider.AuthProfile = buildChatGPTAuthProfile(credential)
}

func (s *Service) refreshChatGPTUsageProfile(
	ctx context.Context,
	provider *model.Provider,
	credential *model.ChatGPTProviderCredential,
) *model.ChatGPTProviderCredential {
	if !shouldRefreshChatGPTUsageSnapshot(credential.Usage, s.clock.Now()) {
		return credential
	}

	snapshot, err := s.fetchChatGPTUsageSnapshot(ctx, credential)
	if err != nil {
		s.logger.Debug("failed to refresh chatgpt usage snapshot while preparing provider auth profile",
			zap.String("provider_id", provider.ID),
			zap.String("account_id", credential.AccountID),
			zap.Error(err),
		)
		return credential
	}

	credential = applyChatGPTUsageSnapshot(credential, snapshot)
	s.persistChatGPTUsageSnapshot(ctx, provider, credential)
	return credential
}

func (s *Service) persistChatGPTUsageSnapshot(
	ctx context.Context,
	provider *model.Provider,
	credential *model.ChatGPTProviderCredential,
) {
	// Apply the refreshed snapshot before persisting so the admin view and the
	// stored credential stay aligned even when the write-back later fails.
	if err := applyChatGPTCredential(provider, credential); err != nil {
		s.logger.Warn("failed to apply refreshed chatgpt usage snapshot",
			zap.String("provider_id", provider.ID),
			zap.Error(err),
		)
		return
	}

	if err := s.persistChatGPTCredential(ctx, provider, credential); err != nil {
		s.logger.Warn("failed to persist chatgpt usage snapshot",
			zap.String("provider_id", provider.ID),
			zap.Error(err),
		)
	}
}
