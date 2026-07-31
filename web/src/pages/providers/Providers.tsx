import { useState, useEffect } from "react";
import { useSearchParams } from "react-router";
import { useGroups } from "../../hooks/useGroups";
import { useLocalStorage } from "../../hooks/useLocalStorage";
import { ConfirmModal, ProviderDetailDrawer } from "../../components";
import { ProviderImportModal } from "../../components/provider-import";
import { DEFAULT_REFRESH_INTERVAL } from "../../components/refreshIntervalConstants";
import { useApi, type Provider, type ProviderInput } from "../../api";
import { getProviderStatus } from "./types";
import type { StatusFilter } from "./types";
import { ProvidersTableBody } from "./ProvidersTableBody";
import { ProviderModal } from "./ProviderModal";
import { useProviderActions } from "./useProviderActions";
import {
  PageHeader,
  FilterBar,
  HelpCard,
  ErrorState,
  ProvidersTableHeader,
} from "./ProvidersPageSections";

type ProviderDialogState =
  | { kind: "add" }
  | { kind: "edit"; provider: Provider }
  | { kind: "import" }
  | null;

export function Providers() {
  const api = useApi();
  const {
    providers,
    hasSnapshot,
    loading,
    error,
    refetch,
    deleteConfirm,
    deleting,
    handleDeleteClick,
    handleDeleteConfirm,
    handleDeleteCancel,
    resetConfirm,
    resetting,
    handleResetClick,
    handleResetConfirm,
    handleResetCancel,
    handleToggleProvider,
    handleSaveProvider,
    handleRefreshCredential,
    handleRefreshUsage,
  } = useProviderActions();
  const { groups } = useGroups();
  const [searchParams] = useSearchParams();

  // Initialize filters from URL parameters
  const [searchQuery, setSearchQuery] = useState(
    searchParams.get("search") || "",
  );
  const [groupFilter, setGroupFilter] = useState("");
  const [statusFilter, setStatusFilter] = useState<StatusFilter>(
    (searchParams.get("status") as StatusFilter) || "",
  );

  const [dialog, setDialog] = useState<ProviderDialogState>(null);
  const editingProvider = dialog?.kind === "edit" ? dialog.provider : null;

  // Auto-refresh state
  const [refreshInterval, setRefreshInterval] = useLocalStorage(
    "providers:refreshInterval",
    DEFAULT_REFRESH_INTERVAL.providers,
  );

  // Auto-refresh effect
  useEffect(() => {
    let intervalId: ReturnType<typeof setInterval>;
    if (refreshInterval > 0 && dialog?.kind !== "import") {
      intervalId = setInterval(() => void refetch(), refreshInterval);
    }
    return () => {
      if (intervalId) clearInterval(intervalId);
    };
  }, [dialog?.kind, refreshInterval, refetch]);

  // Detail drawer state
  const [detailProviderId, setDetailProviderId] = useState<string | null>(null);
  const detailProvider =
    providers.find((provider) => provider.id === detailProviderId) ?? null;

  const filteredProviders = providers.filter((provider) => {
    if (searchQuery) {
      const query = searchQuery.toLowerCase();
      const matchesName = provider.name.toLowerCase().includes(query);
      const matchesId = provider.id.toLowerCase().includes(query);
      const matchesUrl = provider.api_types?.some((t) =>
        t.base_url.toLowerCase().includes(query),
      );
      if (!matchesName && !matchesId && !matchesUrl) return false;
    }
    if (groupFilter && provider.group_id !== groupFilter) return false;
    if (statusFilter) {
      const status = getProviderStatus(
        provider.enabled,
        provider.health?.available,
        provider.health?.disabled_until,
      );
      if (statusFilter === "pending-recovery" && status === "pending-recovery")
        return true;
      if (statusFilter === status) return true;
      return false;
    }
    return true;
  });

  const onSaveProvider = async (data: ProviderInput) => {
    await handleSaveProvider(data, editingProvider);
    setDialog(null);
  };

  const handleAddClick = () => {
    setDialog({ kind: "add" });
  };

  const handleImportClick = () => setDialog({ kind: "import" });
  const handleImportClose = () => {
    setDialog(null);
  };
  const handleImportCheckProviders = () => {
    setDialog(null);
    void refetch();
  };

  const handleEditClick = (provider: Provider) => {
    setDialog({ kind: "edit", provider });
    setDetailProviderId(null);
  };

  const handleViewDetail = (provider: Provider) =>
    setDetailProviderId(provider.id);
  const handleCloseDetail = () => setDetailProviderId(null);

  const handleDrawerToggle = async (provider: Provider) => {
    await handleToggleProvider(provider);
    const updatedProvider = providers.find((p) => p.id === provider.id);
    if (updatedProvider) setDetailProviderId(updatedProvider.id);
  };

  const handleDrawerDelete = (provider: Provider) => {
    handleDeleteClick(provider);
    setDetailProviderId(null);
  };

  const handleDrawerReset = (provider: Provider) => {
    handleResetClick(provider);
    setDetailProviderId(null);
  };

  const getGroupName = (groupId: string | null) => {
    if (!groupId) return "—";
    const group = groups.find((g) => g.id === groupId);
    return group?.name ?? groupId;
  };

  const getGroupEnabled = (groupId: string | null) => {
    if (!groupId) return undefined;
    return groups.find((group) => group.id === groupId)?.enabled;
  };

  if (error && !hasSnapshot)
    return <ErrorState message={error.message} onRetry={() => refetch()} />;

  return (
    <div className="space-y-5">
      <PageHeader
        loading={loading}
        onRefresh={() => refetch()}
        onAddClick={handleAddClick}
        onImportClick={handleImportClick}
        refreshInterval={refreshInterval}
        onRefreshIntervalChange={setRefreshInterval}
      />

      {error && (
        <div
          role="alert"
          className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-warning/30 bg-warning-light/30 p-3 text-sm text-text-secondary"
        >
          <span>
            Provider list could not refresh. The last successful snapshot is
            still shown.
          </span>
          <button
            type="button"
            className="btn btn-secondary"
            onClick={() => void refetch()}
          >
            Retry refresh
          </button>
        </div>
      )}

      {providers.length === 0 &&
        !loading &&
        !searchQuery &&
        !groupFilter &&
        !statusFilter && <HelpCard />}

      <FilterBar
        searchQuery={searchQuery}
        onSearchChange={setSearchQuery}
        groupFilter={groupFilter}
        onGroupFilterChange={setGroupFilter}
        statusFilter={statusFilter}
        onStatusFilterChange={setStatusFilter}
        groups={groups}
      />

      <div className="bg-white border border-border rounded-2xl shadow-sm overflow-hidden">
        <div className="overflow-x-auto">
          <table className="w-full min-w-[1000px] table-auto">
            <ProvidersTableHeader />
            <tbody className="divide-y divide-border/60 bg-white">
              <ProvidersTableBody
                loading={loading}
                providers={providers}
                filteredProviders={filteredProviders}
                onToggle={handleToggleProvider}
                onEdit={handleEditClick}
                onDelete={handleDeleteClick}
                onReset={handleResetClick}
                onAddClick={handleAddClick}
                onImportClick={handleImportClick}
                onGroupClick={setGroupFilter}
                onViewDetail={handleViewDetail}
                getGroupName={getGroupName}
                getGroupEnabled={getGroupEnabled}
              />
            </tbody>
          </table>
        </div>
      </div>

      {(dialog?.kind === "add" || dialog?.kind === "edit") && (
        <ProviderModal
          initialData={editingProvider || undefined}
          onClose={() => setDialog(null)}
          onSubmit={onSaveProvider}
          groups={groups}
        />
      )}

      {dialog?.kind === "import" && (
        <ProviderImportModal
          gateway={api.providerImports}
          existingProviderIds={providers.map((provider) => provider.id)}
          groups={groups}
          onClose={handleImportClose}
          onCheckProviders={handleImportCheckProviders}
          onCommitted={() => refetch()}
        />
      )}

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
      <ConfirmModal
        isOpen={resetConfirm.isOpen}
        onClose={handleResetCancel}
        onConfirm={handleResetConfirm}
        title="Reset Provider Circuit Breaker"
        message={`Are you sure you want to reset the circuit breaker state for provider "${resetConfirm.provider?.name}"? This will clear failure counts and enable the provider immediately.`}
        confirmText="Reset"
        cancelText="Cancel"
        variant="warning"
        loading={resetting}
      />
      <ProviderDetailDrawer
        provider={detailProvider}
        onClose={handleCloseDetail}
        onEdit={handleEditClick}
        onDelete={handleDrawerDelete}
        onToggle={handleDrawerToggle}
        onReset={handleDrawerReset}
        onRefreshCredential={handleRefreshCredential}
        onRefreshUsage={handleRefreshUsage}
        getGroupName={getGroupName}
      />
    </div>
  );
}
