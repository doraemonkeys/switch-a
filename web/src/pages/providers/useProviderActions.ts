import { useState } from "react";
import { useProviders } from "../../hooks/useProviders";
import { useToast } from "../../hooks/useToast";
import { ApiError, type Provider, type ProviderInput } from "../../api/client";
import { downloadJsonFile } from "../../lib/jsonDownload";
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
    exportCodexAuth,
  } = useProviders();
  const toast = useToast();
  const [exportingProviderId, setExportingProviderId] = useState<string | null>(
    null,
  );
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

  const handleExportCodexAuth = async (provider: Provider) => {
    setExportingProviderId(provider.id);
    try {
      const authDocument = await exportCodexAuth(provider.id);
      downloadJsonFile("auth.json", authDocument);
      toast.success(
        `Codex auth.json exported for "${provider.name}". Keep this provider paused while the file is in use.`,
      );
    } catch (err) {
      toast.error(
        err instanceof Error ? err.message : "Failed to export Codex auth.json",
      );
    } finally {
      setExportingProviderId(null);
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
    exportingProviderId,
  };
}
