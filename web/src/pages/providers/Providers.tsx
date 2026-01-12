import { useState, useMemo } from "react";
import { useProviders } from "../../hooks/useProviders";
import { useGroups } from "../../hooks/useGroups";
import type { Provider, ProviderInput } from "../../api/client";
import { getProviderStatus } from "./types";
import type { StatusFilter } from "./types";
import { ProvidersTableBody } from "./ProvidersTableBody";
import { ProviderModal } from "./ProviderModal";
import {
  PageHeader,
  MutationErrorAlert,
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
  } = useProviders();
  const { groups } = useGroups();

  const [searchQuery, setSearchQuery] = useState("");
  const [groupFilter, setGroupFilter] = useState("");
  const [statusFilter, setStatusFilter] = useState<StatusFilter>("");
  const [showModal, setShowModal] = useState(false);
  const [editingProvider, setEditingProvider] = useState<Provider | null>(null);
  const [mutationError, setMutationError] = useState<string | null>(null);

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
        );
        if (statusFilter !== status) return false;
      }
      return true;
    });
  }, [providers, searchQuery, groupFilter, statusFilter]);

  const handleToggleProvider = async (provider: Provider) => {
    setMutationError(null);
    try {
      if (provider.enabled) {
        await disableProvider(provider.id);
      } else {
        await enableProvider(provider.id);
      }
    } catch (err) {
      const message =
        err instanceof Error ? err.message : "Failed to toggle provider";
      setMutationError(message);
    }
  };

  const handleDeleteProvider = async (provider: Provider) => {
    if (
      !confirm(`Are you sure you want to delete provider "${provider.name}"?`)
    ) {
      return;
    }
    setMutationError(null);
    try {
      await deleteProvider(provider.id);
    } catch (err) {
      const message =
        err instanceof Error ? err.message : "Failed to delete provider";
      setMutationError(message);
    }
  };

  const handleSaveProvider = async (data: ProviderInput) => {
    setMutationError(null);
    try {
      if (editingProvider) {
        await updateProvider(editingProvider.id, data);
      } else {
        await createProvider(data);
      }
      setShowModal(false);
      setEditingProvider(null);
    } catch (err) {
      const message =
        err instanceof Error ? err.message : "Failed to save provider";
      setMutationError(message);
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
      />

      {mutationError && (
        <MutationErrorAlert
          error={mutationError}
          onDismiss={() => setMutationError(null)}
        />
      )}

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
              onDelete={handleDeleteProvider}
              onAddClick={handleAddClick}
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
    </div>
  );
}
