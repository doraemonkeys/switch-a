package store

import "switch-a/internal/model"

// ProviderWriteOptions is kept in the store package as a consumer-friendly alias;
// the command itself belongs to the domain model so all store interfaces share it
// without introducing an import cycle.
type ProviderWriteOptions = model.ProviderWriteOptions

func resolveProviderWriteOptions(options []ProviderWriteOptions) ProviderWriteOptions {
	if len(options) == 0 {
		return ProviderWriteOptions{
			CredentialBindingResolution: model.CredentialBindingResolutionReject,
		}
	}
	return options[0]
}
