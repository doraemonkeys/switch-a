import { useProviders } from "../../hooks/useProviders";
import { useToast } from "../../hooks/useToast";
import { ApiError, type Provider, type ProviderInput } from "../../api/client";
import { useProviderConfirmations } from "./useProviderConfirmations";

function isCredentialBindingConflict(error: unknown): boolean {
  return (
    error instanceof ApiError && error.details?.kind === "credential_binding"
  );
}

export function useProviderActions() {
  const {
    providers,
    hasSnapshot,
    loading,
    error,
    refetch,
    createProvider,
    updateProvider,
    deleteProvider,
    enableProvider,
    disableProvider,
    resetProvider,
    refreshCredential,
    refreshUsage,
  } = useProviders();
  const toast = useToast();
  const confirmations = useProviderConfirmations({
    deleteProvider,
    resetProvider,
    toast,
  });

  const handleToggleProvider = async (provider: Provider) => {
    try {
      if (provider.enabled) {
        await disableProvider(provider.id);
        toast.success(`Provider "${provider.name}" disabled`);
      } else {
        await enableProvider(provider.id);
        toast.success(`Provider "${provider.name}" enabled`);
      }
    } catch (err) {
      toast.error(
        err instanceof Error ? err.message : "Failed to toggle provider",
      );
    }
  };

  const handleSaveProvider = async (
    data: ProviderInput,
    editingProvider: Provider | null,
  ) => {
    try {
      if (editingProvider) {
        await updateProvider(editingProvider.id, data);
        toast.success("Provider updated successfully");
      } else {
        await createProvider(data);
        toast.success("Provider created successfully");
      }
      return true;
    } catch (err) {
      // A duplicate GPT account is an expected decision point; the modal owns
      // the confirmation so an error toast would race the replacement prompt.
      if (isCredentialBindingConflict(err)) {
        throw err;
      }
      toast.error(
        err instanceof Error ? err.message : "Failed to save provider",
      );
      throw err;
    }
  };

  const handleRefreshCredential = async (provider: Provider) => {
    try {
      await refreshCredential(provider.id);
      toast.success(`Credential refreshed for "${provider.name}"`);
    } catch (err) {
      toast.error(
        err instanceof Error
          ? err.message
          : "Failed to refresh provider credential",
      );
      throw err;
    }
  };

  const handleRefreshUsage = async (provider: Provider) => {
    try {
      await refreshUsage(provider.id);
      toast.success(`Usage snapshot refreshed for "${provider.name}"`);
    } catch (err) {
      toast.error(
        err instanceof Error ? err.message : "Failed to refresh provider usage",
      );
      throw err;
    }
  };

  return {
    providers,
    hasSnapshot,
    loading,
    error,
    refetch,
    ...confirmations,
    handleToggleProvider,
    handleSaveProvider,
    handleRefreshCredential,
    handleRefreshUsage,
  };
}
