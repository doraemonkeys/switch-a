import { useDashboardData } from "./useDashboardData";
import { useDashboardProviderActions } from "./useDashboardProviderActions";
import { useDashboardDeleteAction } from "./useDashboardDeleteAction";
import { useDashboardBatchActions } from "./useDashboardBatchActions";

export function useDashboard() {
  const data = useDashboardData();

  const providerActions = useDashboardProviderActions({
    providers: data.providers,
    enableProvider: data.enableProvider,
    disableProvider: data.disableProvider,
    resetProvider: data.resetProvider,
    refetchStatus: data.refetchStatus,
  });

  const deleteAction = useDashboardDeleteAction({
    deleteProvider: data.deleteProvider,
    refetchStatus: data.refetchStatus,
    onDetailClose: providerActions.handleCloseDetail,
  });

  const batchActions = useDashboardBatchActions({
    status: data.status,
    refetchStatus: data.refetchStatus,
  });

  return {
    // Data
    status: data.status,
    summary: data.summary,
    totalLogs: data.totalLogs,
    stats: data.stats,
    recentErrors: data.recentErrors,
    loading: data.loading,
    error: data.error,
    refreshInterval: data.refreshInterval,
    setRefreshInterval: data.setRefreshInterval,
    handleRefresh: data.handleRefresh,
    getGroupName: data.getGroupName,

    // Provider actions
    navigate: providerActions.navigate,
    detailProvider: providerActions.detailProvider,
    handleProviderClick: providerActions.handleProviderClick,
    handleCloseDetail: providerActions.handleCloseDetail,
    handleEditProvider: providerActions.handleEditProvider,
    handleToggleProvider: providerActions.handleToggleProvider,
    handleResetProvider: providerActions.handleResetProvider,

    // Delete actions
    deleteConfirm: deleteAction.deleteConfirm,
    deleting: deleteAction.deleting,
    handleDeleteProviderClick: deleteAction.handleDeleteProviderClick,
    handleDeleteConfirm: deleteAction.handleDeleteConfirm,
    handleDeleteCancel: deleteAction.handleDeleteCancel,

    // Batch actions
    batchLoading: batchActions.batchLoading,
    batchResetConfirm: batchActions.batchResetConfirm,
    handleBatchResetClick: batchActions.handleBatchResetClick,
    handleBatchResetConfirm: batchActions.handleBatchResetConfirm,
    handleBatchResetCancel: batchActions.handleBatchResetCancel,
  };
}
