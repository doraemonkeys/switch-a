import { useState, useMemo, useEffect } from "react";
import { useSearchParams } from "react-router-dom";
import { useProviders } from "../../hooks/useProviders";
import { useGroups } from "../../hooks/useGroups";
import { useToast } from "../../hooks/useToast";
import { useLocalStorage } from "../../hooks/useLocalStorage";
import { ConfirmModal } from "../../components";
import { DEFAULT_REFRESH_INTERVAL } from "../../components/refreshIntervalConstants";
import type { Provider, ProviderInput } from "../../api/client";
import { getProviderStatus } from "./types";
import type { StatusFilter } from "./types";
import { ProvidersTableBody } from "./ProvidersTableBody";
import { ProviderModal } from "./ProviderModal";
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
    createProvider,
    updateProvider,
    deleteProvider,
    enableProvider,
    disableProvider,
    resetProvider,
  } = useProviders();
  const { groups } = useGroups();
  const toast = useToast();
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
      intervalId = setInterval(() => {
        refetch();
      }, refreshInterval);
    }
    return () => {
      if (intervalId) clearInterval(intervalId);
    };
  }, [refreshInterval, refetch]);

  const [deleteConfirm, setDeleteConfirm] = useState<{
    isOpen: boolean;
    provider: Provider | null;
  }>({
    isOpen: false,
    provider: null,
  });
  const [deleting, setDeleting] = useState(false);

  const [resetConfirm, setResetConfirm] = useState<{
    isOpen: boolean;
    provider: Provider | null;
  }>({
    isOpen: false,
    provider: null,
  });
  const [resetting, setResetting] = useState(false);

  const filteredProviders = useMemo(() => {
    return providers.filter((provider) => {
      if (searchQuery) {
        const query = searchQuery.toLowerCase();
        const matchesName = provider.name.toLowerCase().includes(query);
        const matchesId = provider.id.toLowerCase().includes(query);
        const matchesUrl = provider.base_url.toLowerCase().includes(query);
        if (!matchesName && !matchesId && !matchesUrl) return false;
      }
      if (groupFilter && provider.group_id !== groupFilter) return false;
      if (statusFilter) {
        const status = getProviderStatus(
          provider.enabled,
          provider.health?.available,
          provider.health?.disabled_until,
        );
        if (
          statusFilter === "pending-recovery" &&
          status === "pending-recovery"
        )
          return true;

        // Map "unhealthy" filter to both "unhealthy" and "circuit-open" status logic if needed
        // Currently getProviderStatus returns "unhealthy" for circuit open.
        if (statusFilter === status) return true;

        return false;
      }
      return true;
    });
  }, [providers, searchQuery, groupFilter, statusFilter]);

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
      const message =
        err instanceof Error ? err.message : "Failed to toggle provider";
      toast.error(message);
    }
  };

  const handleDeleteClick = (provider: Provider) => {
    setDeleteConfirm({ isOpen: true, provider });
  };

  const handleDeleteConfirm = async () => {
    if (!deleteConfirm.provider) return;
    setDeleting(true);
    try {
      await deleteProvider(deleteConfirm.provider.id);
      toast.success(`Provider "${deleteConfirm.provider.name}" deleted`);
      setDeleteConfirm({ isOpen: false, provider: null });
    } catch (err) {
      const message =
        err instanceof Error ? err.message : "Failed to delete provider";
      toast.error(message);
    } finally {
      setDeleting(false);
    }
  };

  const handleDeleteCancel = () => {
    setDeleteConfirm({ isOpen: false, provider: null });
  };

  const handleResetClick = (provider: Provider) => {
    setResetConfirm({ isOpen: true, provider });
  };

  const handleResetConfirm = async () => {
    if (!resetConfirm.provider) return;
    setResetting(true);
    try {
      await resetProvider(resetConfirm.provider.id);
      toast.success(`Provider "${resetConfirm.provider.name}" reset`);
      setResetConfirm({ isOpen: false, provider: null });
    } catch (err) {
      const message =
        err instanceof Error ? err.message : "Failed to reset provider";
      toast.error(message);
    } finally {
      setResetting(false);
    }
  };

  const handleResetCancel = () => {
    setResetConfirm({ isOpen: false, provider: null });
  };

  const handleSaveProvider = async (data: ProviderInput) => {
    try {
      if (editingProvider) {
        await updateProvider(editingProvider.id, data);
        toast.success("Provider updated successfully");
      } else {
        await createProvider(data);
        toast.success("Provider created successfully");
      }
      setShowModal(false);
      setEditingProvider(null);
    } catch (err) {
      const message =
        err instanceof Error ? err.message : "Failed to save provider";
      toast.error(message);
      throw err;
    }
  };

  const handleAddClick = () => {
    setEditingProvider(null);
    setShowModal(true);
  };

  const handleEditClick = (provider: Provider) => {
    setEditingProvider(provider);
    setShowModal(true);
  };

  const getGroupName = (groupId: string | null) => {
    if (!groupId) return "—";
    const group = groups.find((g) => g.id === groupId);
    return group?.name ?? groupId;
  };

  if (error) {
    return <ErrorState message={error.message} onRetry={() => refetch()} />;
  }

  return (
    <div className="space-y-6">
      <PageHeader
        loading={loading}
        onRefresh={() => refetch()}
        onAddClick={handleAddClick}
        refreshInterval={refreshInterval}
        onRefreshIntervalChange={setRefreshInterval}
      />

      <FilterBar
        searchQuery={searchQuery}
        onSearchChange={setSearchQuery}
        groupFilter={groupFilter}
        onGroupFilterChange={setGroupFilter}
        statusFilter={statusFilter}
        onStatusFilterChange={setStatusFilter}
        groups={groups}
      />

      <div className="card overflow-hidden p-0">
        <table className="w-full">
          <ProvidersTableHeader />
          <tbody className="divide-y divide-border">
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
              getGroupName={getGroupName}
            />
          </tbody>
        </table>
      </div>

      <HelpCard />

      {showModal && (
        <ProviderModal
          initialData={editingProvider || undefined}
          onClose={() => {
            setShowModal(false);
            setEditingProvider(null);
          }}
          onSubmit={handleSaveProvider}
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
    </div>
  );
}
