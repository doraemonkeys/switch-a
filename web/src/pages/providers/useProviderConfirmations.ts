import { useState } from "react";
import type { Provider } from "../../api";
import type { ToastContextValue } from "../../hooks/useToast";

interface ProviderConfirmationState {
  isOpen: boolean;
  provider: Provider | null;
}

interface ProviderConfirmationDependencies {
  deleteProvider: (id: string) => Promise<void>;
  resetProvider: (id: string) => Promise<void>;
  toast: Pick<ToastContextValue, "success" | "error">;
}

const CLOSED_CONFIRMATION: ProviderConfirmationState = {
  isOpen: false,
  provider: null,
};

export function useProviderConfirmations({
  deleteProvider,
  resetProvider,
  toast,
}: ProviderConfirmationDependencies) {
  const [deleteConfirm, setDeleteConfirm] =
    useState<ProviderConfirmationState>(CLOSED_CONFIRMATION);
  const [deleting, setDeleting] = useState(false);
  const [resetConfirm, setResetConfirm] =
    useState<ProviderConfirmationState>(CLOSED_CONFIRMATION);
  const [resetting, setResetting] = useState(false);

  const handleDeleteClick = (provider: Provider) =>
    setDeleteConfirm({ isOpen: true, provider });

  const handleDeleteConfirm = async () => {
    if (!deleteConfirm.provider) return;
    setDeleting(true);
    try {
      await deleteProvider(deleteConfirm.provider.id);
      toast.success(`Provider "${deleteConfirm.provider.name}" deleted`);
      setDeleteConfirm(CLOSED_CONFIRMATION);
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : "Failed to delete provider",
      );
    } finally {
      setDeleting(false);
    }
  };

  const handleResetClick = (provider: Provider) =>
    setResetConfirm({ isOpen: true, provider });

  const handleResetConfirm = async () => {
    if (!resetConfirm.provider) return;
    setResetting(true);
    try {
      await resetProvider(resetConfirm.provider.id);
      toast.success(`Provider "${resetConfirm.provider.name}" reset`);
      setResetConfirm(CLOSED_CONFIRMATION);
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : "Failed to reset provider",
      );
    } finally {
      setResetting(false);
    }
  };

  return {
    deleteConfirm,
    deleting,
    handleDeleteClick,
    handleDeleteConfirm,
    handleDeleteCancel: () => setDeleteConfirm(CLOSED_CONFIRMATION),
    resetConfirm,
    resetting,
    handleResetClick,
    handleResetConfirm,
    handleResetCancel: () => setResetConfirm(CLOSED_CONFIRMATION),
  };
}
