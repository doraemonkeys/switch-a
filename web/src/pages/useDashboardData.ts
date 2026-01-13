import { useMemo, useEffect, useCallback } from "react";
import { useStatus } from "../hooks/useStatus";
import { useLogs } from "../hooks/useLogs";
import { useLocalStorage } from "../hooks/useLocalStorage";
import { useProviders } from "../hooks/useProviders";
import { useGroups } from "../hooks/useGroups";
import { DEFAULT_REFRESH_INTERVAL } from "../components/refreshIntervalConstants";

export function useDashboardData() {
  const {
    status,
    summary,
    loading: statusLoading,
    error: statusError,
    refetch: refetchStatus,
  } = useStatus();
  const {
    logs,
    total: totalLogs,
    loading: logsLoading,
    error: logsError,
    refetch: refetchLogs,
  } = useLogs({ limit: 50 });
  const {
    providers,
    enableProvider,
    disableProvider,
    deleteProvider,
    resetProvider,
    refetch: refetchProviders,
  } = useProviders();
  const { groups } = useGroups();

  const [refreshInterval, setRefreshInterval] = useLocalStorage(
    "dashboard:refreshInterval",
    DEFAULT_REFRESH_INTERVAL.dashboard,
  );

  // Auto-refresh effect
  useEffect(() => {
    let intervalId: ReturnType<typeof setInterval>;
    if (refreshInterval > 0) {
      intervalId = setInterval(() => {
        refetchStatus();
        refetchLogs();
      }, refreshInterval);
    }
    return () => {
      if (intervalId) clearInterval(intervalId);
    };
  }, [refreshInterval, refetchStatus, refetchLogs]);

  const handleRefresh = useCallback(() => {
    refetchStatus();
    refetchLogs();
    refetchProviders();
  }, [refetchStatus, refetchLogs, refetchProviders]);

  const getGroupName = useCallback(
    (groupId: string | null) => {
      if (!groupId) return "—";
      const group = groups.find((g) => g.id === groupId);
      return group?.name ?? groupId;
    },
    [groups],
  );

  const loading = statusLoading || logsLoading;
  const error = statusError || logsError;

  const stats = useMemo(() => {
    if (!status?.providers)
      return { total: 0, healthy: 0, unhealthy: 0, disabled: 0 };
    const providerList = status.providers;
    return {
      total: providerList.length,
      healthy: providerList.filter(
        (p) => p.enabled && p.health?.available !== false,
      ).length,
      unhealthy: providerList.filter(
        (p) => p.enabled && p.health?.available === false,
      ).length,
      disabled: providerList.filter((p) => !p.enabled).length,
    };
  }, [status]);

  const recentErrors = useMemo(
    () => logs.filter((log) => !log.success).slice(0, 5),
    [logs],
  );

  return {
    status,
    summary,
    totalLogs,
    stats,
    recentErrors,
    loading,
    error,
    providers,
    enableProvider,
    disableProvider,
    deleteProvider,
    resetProvider,
    refetchStatus,
    refreshInterval,
    setRefreshInterval,
    handleRefresh,
    getGroupName,
  };
}
