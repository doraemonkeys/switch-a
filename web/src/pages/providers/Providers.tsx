import { useState, useEffect } from "react";
import { useSearchParams } from "react-router-dom";
import { useGroups } from "../../hooks/useGroups";
import { useLocalStorage } from "../../hooks/useLocalStorage";
import { ConfirmModal, ProviderDetailDrawer } from "../../components";
import { DEFAULT_REFRESH_INTERVAL } from "../../components/refreshIntervalConstants";
import type { Provider, ProviderInput } from "../../api/client";
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

export function Providers() {
  const {
    providers,
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

  const [showModal, setShowModal] = useState(false);
  const [editingProvider, setEditingProvider] = useState<Provider | null>(null);

  // Auto-refresh state
  const [refreshInterval, setRefreshInterval] = useLocalStorage(
    "providers:refreshInterval",
    DEFAULT_REFRESH_INTERVAL.providers,
  );

  // Auto-refresh effect
  useEffect(() => {
    let intervalId: ReturnType<typeof setInterval>;
    if (refreshInterval > 0) {
      intervalId = setInterval(() => refetch(), refreshInterval);
    }
    return () => {
      if (intervalId) clearInterval(intervalId);
    };
  }, [refreshInterval, refetch]);

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
    setShowModal(false);
    setEditingProvider(null);
  };

  const handleAddClick = () => {
    setEditingProvider(null);
    setShowModal(true);
  };

  const handleEditClick = (provider: Provider) => {
    setEditingProvider(provider);
    setShowModal(true);
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

  if (error)
    return <ErrorState message={error.message} onRetry={() => refetch()} />;

  return (
    <div className="space-y-5">
      <PageHeader
        loading={loading}
        onRefresh={() => refetch()}
        onAddClick={handleAddClick}
        refreshInterval={refreshInterval}
        onRefreshIntervalChange={setRefreshInterval}
      />

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
                onGroupClick={setGroupFilter}
                onViewDetail={handleViewDetail}
                getGroupName={getGroupName}
              />
            </tbody>
          </table>
        </div>
      </div>

      {showModal && (
        <ProviderModal
          initialData={editingProvider || undefined}
          onClose={() => {
            setShowModal(false);
            setEditingProvider(null);
          }}
          onSubmit={onSaveProvider}
          groups={groups}
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
