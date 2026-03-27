import { useState } from "react";
import { useProviders } from "../../hooks/useProviders";
import { useToast } from "../../hooks/useToast";
import type { Provider, ProviderInput } from "../../api/client";

interface ConfirmState<T> {
  isOpen: boolean;
  item: T | null;
}

export function useProviderActions() {
  const {
    providers,
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

  // Delete confirmation state
  const [deleteConfirm, setDeleteConfirm] = useState<ConfirmState<Provider>>({
    isOpen: false,
    item: null,
  });
  const [deleting, setDeleting] = useState(false);

  // Reset confirmation state
  const [resetConfirm, setResetConfirm] = useState<ConfirmState<Provider>>({
    isOpen: false,
    item: null,
  });
  const [resetting, setResetting] = useState(false);

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

  const handleDeleteClick = (provider: Provider) =>
    setDeleteConfirm({ isOpen: true, item: provider });

  const handleDeleteConfirm = async () => {
    if (!deleteConfirm.item) return;
    setDeleting(true);
    try {
      await deleteProvider(deleteConfirm.item.id);
      toast.success(`Provider "${deleteConfirm.item.name}" deleted`);
      setDeleteConfirm({ isOpen: false, item: null });
    } catch (err) {
      toast.error(
        err instanceof Error ? err.message : "Failed to delete provider",
      );
    } finally {
      setDeleting(false);
    }
  };

  const handleDeleteCancel = () =>
    setDeleteConfirm({ isOpen: false, item: null });

  const handleResetClick = (provider: Provider) =>
    setResetConfirm({ isOpen: true, item: provider });

  const handleResetConfirm = async () => {
    if (!resetConfirm.item) return;
    setResetting(true);
    try {
      await resetProvider(resetConfirm.item.id);
      toast.success(`Provider "${resetConfirm.item.name}" reset`);
      setResetConfirm({ isOpen: false, item: null });
    } catch (err) {
      toast.error(
        err instanceof Error ? err.message : "Failed to reset provider",
      );
    } finally {
      setResetting(false);
    }
  };

  const handleResetCancel = () =>
    setResetConfirm({ isOpen: false, item: null });

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
    loading,
    error,
    refetch,
    // Delete
    deleteConfirm: {
      isOpen: deleteConfirm.isOpen,
      provider: deleteConfirm.item,
    },
    deleting,
    handleDeleteClick,
    handleDeleteConfirm,
    handleDeleteCancel,
    // Reset
    resetConfirm: { isOpen: resetConfirm.isOpen, provider: resetConfirm.item },
    resetting,
    handleResetClick,
    handleResetConfirm,
    handleResetCancel,
    // Actions
    handleToggleProvider,
    handleSaveProvider,
    handleRefreshCredential,
    handleRefreshUsage,
  };
}
