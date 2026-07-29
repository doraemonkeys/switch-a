import { Link } from "react-router";
import { ConfirmModal, ProviderDetailDrawer } from "../components";
import { RefreshIntervalSelect } from "../components/RefreshIntervalSelect";
import {
  ProviderStatusContent,
  RecentErrorsContent,
  StatCard,
  QuickActionButton,
} from "./DashboardSections";
import { useDashboard } from "./useDashboard";

export function Dashboard() {
  const {
    status,
    summary,
    totalLogs,
    stats,
    recentErrors,
    loading,
    error,
    batchLoading,
    refreshInterval,
    setRefreshInterval,
    handleRefresh,
    detailProvider,
    handleProviderClick,
    handleCloseDetail,
    handleEditProvider,
    handleToggleProvider,
    handleResetProvider,
    getGroupName,
    deleteConfirm,
    deleting,
    handleDeleteProviderClick,
    handleDeleteConfirm,
    handleDeleteCancel,
    batchResetConfirm,
    handleBatchResetClick,
    handleBatchResetConfirm,
    handleBatchResetCancel,
    navigate,
  } = useDashboard();

  return (
    <div className="space-y-6">
      {/* Page Header */}
      <div className="flex items-center justify-between">
        <div>
          <h2 className="text-2xl font-bold text-text-primary">Dashboard</h2>
          <p className="text-text-secondary mt-1">系统状态总览</p>
        </div>
        <div className="flex items-center gap-3">
          <button
            onClick={handleRefresh}
            disabled={loading}
            className="btn btn-secondary btn-sm"
          >
            <span className={loading ? "animate-spin" : ""}>🔄</span>
            Refresh
          </button>
        </div>
      </div>

      {/* Error Banner */}
      {error && (
        <div className="bg-danger-light/10 border border-danger-light text-danger-dark px-4 py-3 rounded-lg flex items-center gap-3">
          <span className="text-xl">⚠️</span>
          <div>
            <p className="font-medium">Failed to load dashboard data</p>
            <p className="text-sm opacity-90">{error.message}</p>
          </div>
        </div>
      )}

      {/* Stats Grid */}
      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-4">
        <StatCard
          title="Healthy"
          value={stats.healthy.toString()}
          icon="✅"
          subtext="Available"
          variant="success"
          loading={loading && !summary}
          onClick={() => navigate("/providers?status=healthy")}
        />
        <StatCard
          title="Unhealthy"
          value={stats.unhealthy.toString()}
          icon="🔴"
          subtext="Circuit Breaker"
          variant="danger"
          loading={loading && !summary}
          onClick={() => navigate("/providers?status=unhealthy")}
        />
        <StatCard
          title="Disabled"
          value={stats.disabled.toString()}
          icon="⚪"
          subtext="User Disabled"
          variant="secondary"
          loading={loading && !summary}
          onClick={() => navigate("/providers?status=disabled")}
        />
        <StatCard
          title="Total Requests"
          value={totalLogs.toString() || "0"}
          icon="📈"
          subtext="Total logged"
          variant="info"
          loading={loading && totalLogs === 0}
          onClick={() => navigate("/logs")}
        />
      </div>

      {/* Main Content Grid */}
      <div className="grid grid-cols-1 lg:grid-cols-3 gap-6">
        <div className="lg:col-span-2 card">
          <div className="flex items-center justify-between mb-4">
            <h3 className="text-lg font-semibold text-text-primary">
              Provider Status
            </h3>
            <div className="flex items-center gap-2">
              <RefreshIntervalSelect
                value={refreshInterval}
                onChange={setRefreshInterval}
              />
              <span className="badge badge-neutral">
                {status?.providers.length || 0} providers
              </span>
            </div>
          </div>
          <ProviderStatusContent
            status={status}
            loading={loading}
            onBatchReset={handleBatchResetClick}
            batchLoading={batchLoading}
            onProviderClick={handleProviderClick}
          />
        </div>

        <div className="card">
          <h3 className="text-lg font-semibold text-text-primary mb-4">
            Quick Actions
          </h3>
          <div className="space-y-2">
            <QuickActionButton
              icon="➕"
              label="Add Provider"
              onClick={() => navigate("/providers")}
            />
            <QuickActionButton
              icon="📁"
              label="Create Group"
              onClick={() => navigate("/groups")}
            />
            <QuickActionButton
              icon="⚙️"
              label="Edit Config"
              onClick={() => navigate("/config")}
            />
            <QuickActionButton
              icon="📋"
              label="View Logs"
              onClick={() => navigate("/logs")}
            />
          </div>
        </div>
      </div>

      {/* Recent Errors */}
      <div className="card">
        <div className="flex items-center justify-between mb-4">
          <h3 className="text-lg font-semibold text-text-primary">
            Recent Errors
          </h3>
          <Link
            to="/logs"
            className="text-sm text-primary hover:text-primary-hover font-medium"
          >
            View All →
          </Link>
        </div>
        <RecentErrorsContent errors={recentErrors} loading={loading} />
      </div>

      <ConfirmModal
        isOpen={batchResetConfirm.isOpen}
        onClose={handleBatchResetCancel}
        onConfirm={handleBatchResetConfirm}
        title="Reset Unhealthy Providers"
        message={`Are you sure you want to reset ${batchResetConfirm.count} unhealthy providers? This will clear failure counts and enable them immediately.`}
        confirmText="Reset All"
        cancelText="Cancel"
        variant="warning"
        loading={batchLoading}
      />
      <ConfirmModal
        isOpen={deleteConfirm.isOpen}
        onClose={handleDeleteCancel}
        onConfirm={handleDeleteConfirm}
        title="Delete Provider"
        message={`Are you sure you want to delete provider "${deleteConfirm.provider?.name}"? This action cannot be undone.`}
        confirmText="Delete"
        cancelText="Cancel"
        variant="danger"
        loading={deleting}
      />
      <ProviderDetailDrawer
        provider={detailProvider}
        onClose={handleCloseDetail}
        onEdit={handleEditProvider}
        onDelete={handleDeleteProviderClick}
        onToggle={handleToggleProvider}
        onReset={handleResetProvider}
        getGroupName={getGroupName}
      />
    </div>
  );
}
