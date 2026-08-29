import { useProviders } from "../../hooks/useProviders";
import { useToast } from "../../hooks/useToast";
import { useApi, type Provider, type ProviderInput } from "../../api";
import { useProviderConfirmations } from "./useProviderConfirmations";
import { resolveProviderChatGPTCredentialSession } from "../../lib/providerAuth";
import { useCodexAuthExport } from "./useCodexAuthExport";

export function useProviderActions() {
  const api = useApi();
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
  } = useProviders();
  const toast = useToast();
  const { handleExportCodexAuth, exportingCredentialSessionId } =
    useCodexAuthExport(providers);
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
      toast.error(
        err instanceof Error ? err.message : "Failed to save provider",
      );
      throw err;
    }
  };

  const handleRefreshCredential = async (provider: Provider) => {
    const session = resolveProviderChatGPTCredentialSession(provider);
    if (!session) {
      const error = new Error(
        `Provider "${provider.name}" has no GPT credential session`,
      );
      toast.error(error.message);
      throw error;
    }
    try {
      await api.credentialSessions.refresh(session.id);
      toast.success(`Credential refreshed for "${provider.name}"`);
    } catch (err) {
      toast.error(
        err instanceof Error
          ? err.message
          : "Failed to refresh provider credential",
      );
      throw err;
    } finally {
      await refetch();
    }
  };

  const handleRefreshUsage = async (provider: Provider) => {
    const session = resolveProviderChatGPTCredentialSession(provider);
    if (!session) {
      const error = new Error(
        `Provider "${provider.name}" has no GPT credential session`,
      );
      toast.error(error.message);
      throw error;
    }
    try {
      await api.credentialSessions.refreshUsage(session.id);
      toast.success(`Usage snapshot refreshed for "${provider.name}"`);
    } catch (err) {
      toast.error(
        err instanceof Error ? err.message : "Failed to refresh provider usage",
      );
      throw err;
    } finally {
      await refetch();
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
    handleExportCodexAuth,
    exportingCredentialSessionId,
  };
}
