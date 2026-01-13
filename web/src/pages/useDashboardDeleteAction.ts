import { useState, useCallback } from "react";
import { useToast } from "../hooks/useToast";
import type { Provider } from "../api/client";

interface UseDeleteActionProps {
  deleteProvider: (id: string) => Promise<void>;
  refetchStatus: () => void;
  onDetailClose: () => void;
}

export function useDashboardDeleteAction({
  deleteProvider,
  refetchStatus,
  onDetailClose,
}: UseDeleteActionProps) {
  const toast = useToast();

  const [deleteConfirm, setDeleteConfirm] = useState<{
    isOpen: boolean;
    provider: Provider | null;
  }>({ isOpen: false, provider: null });
  const [deleting, setDeleting] = useState(false);

  const handleDeleteProviderClick = useCallback(
    (provider: Provider) => {
      setDeleteConfirm({ isOpen: true, provider });
      onDetailClose();
    },
    [onDetailClose],
  );

  const handleDeleteConfirm = useCallback(async () => {
    if (!deleteConfirm.provider) return;
    setDeleting(true);
    try {
      await deleteProvider(deleteConfirm.provider.id);
      toast.success(`Provider "${deleteConfirm.provider.name}" deleted`);
      setDeleteConfirm({ isOpen: false, provider: null });
      refetchStatus();
    } catch (err) {
      toast.error(
        err instanceof Error ? err.message : "Failed to delete provider",
      );
    } finally {
      setDeleting(false);
    }
  }, [deleteConfirm.provider, deleteProvider, refetchStatus, toast]);

  const handleDeleteCancel = useCallback(
    () => setDeleteConfirm({ isOpen: false, provider: null }),
    [],
  );

  return {
    deleteConfirm,
    deleting,
    handleDeleteProviderClick,
    handleDeleteConfirm,
    handleDeleteCancel,
  };
}
