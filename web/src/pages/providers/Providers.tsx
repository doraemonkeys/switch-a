import { useState, useMemo } from "react";
import { useProviders } from "../../hooks/useProviders";
import { useGroups } from "../../hooks/useGroups";
import { useToast } from "../../hooks/useToast";
import { ConfirmModal } from "../../components";
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
  } = useProviders();
  const { groups } = useGroups();
  const toast = useToast();

  const [searchQuery, setSearchQuery] = useState("");
  const [groupFilter, setGroupFilter] = useState("");
  const [statusFilter, setStatusFilter] = useState<StatusFilter>("");
  const [showModal, setShowModal] = useState(false);
  const [editingProvider, setEditingProvider] = useState<Provider | null>(null);
  const [deleteConfirm, setDeleteConfirm] = useState<{ isOpen: boolean; provider: Provider | null }>({
    isOpen: false,
    provider: null,
  });
  const [deleting, setDeleting] = useState(false);

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
    </div>
  );
}
