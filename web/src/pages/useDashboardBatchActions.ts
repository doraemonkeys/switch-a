import { useState, useCallback, useMemo } from "react";
import { useProviderBatch } from "../hooks/useProviderBatch";
import { useToast } from "../hooks/useToast";
import type { SystemStatus } from "../api/client";

interface UseBatchActionsProps {
  status: SystemStatus | null;
  refetchStatus: () => void;
}

export function useDashboardBatchActions({
  status,
  refetchStatus,
}: UseBatchActionsProps) {
  const { batchAction, loading: batchLoading } = useProviderBatch();
  const toast = useToast();

  const [batchResetConfirm, setBatchResetConfirm] = useState<{
    isOpen: boolean;
    count: number;
  }>({ isOpen: false, count: 0 });

  // Memoize unhealthy provider IDs to avoid repeated calculations
  const providers = status?.providers;
  const unhealthyIds = useMemo(() => {
    if (!providers) return [];
    return providers
      .filter((p) => p.enabled && p.health?.available === false)
      .map((p) => p.id);
  }, [providers]);

  const handleBatchResetClick = useCallback(() => {
    if (unhealthyIds.length === 0) return;
    setBatchResetConfirm({ isOpen: true, count: unhealthyIds.length });
  }, [unhealthyIds]);

  const handleBatchResetConfirm = useCallback(async () => {
    if (unhealthyIds.length === 0) return;
    try {
      await batchAction("reset", unhealthyIds);
      toast.success(`Successfully reset ${unhealthyIds.length} providers`);
      setBatchResetConfirm({ isOpen: false, count: 0 });
      refetchStatus();
    } catch (err) {
      console.error("Failed to batch reset providers:", err);
      toast.error("Failed to batch reset providers");
    }
  }, [batchAction, refetchStatus, unhealthyIds, toast]);

  const handleBatchResetCancel = useCallback(
    () => setBatchResetConfirm({ isOpen: false, count: 0 }),
    [],
  );

  return {
    batchLoading,
    batchResetConfirm,
    handleBatchResetClick,
    handleBatchResetConfirm,
    handleBatchResetCancel,
  };
}
