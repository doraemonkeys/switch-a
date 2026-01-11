import { useState, useMemo } from "react";
import { useProviders } from "../../hooks/useProviders";
import { useGroups } from "../../hooks/useGroups";
import type { Provider, ProviderInput } from "../../api/client";
import { getProviderStatus } from "./types";
import type { StatusFilter } from "./types";
import { ProvidersTableBody } from "./ProvidersTableBody";
import { AddProviderModal } from "./AddProviderModal";

export function Providers() {
  const {
    providers,
    loading,
    error,
    refetch,
    createProvider,
    deleteProvider,
    enableProvider,
    disableProvider,
  } = useProviders();
  const { groups } = useGroups();

  const [searchQuery, setSearchQuery] = useState("");
  const [groupFilter, setGroupFilter] = useState("");
  const [statusFilter, setStatusFilter] = useState<StatusFilter>("");
  const [showAddModal, setShowAddModal] = useState(false);
  const [mutationError, setMutationError] = useState<string | null>(null);

  // Filter providers based on search and filters
  const filteredProviders = useMemo(() => {
    return providers.filter((provider) => {
      // Search filter
      if (searchQuery) {
        const query = searchQuery.toLowerCase();
        const matchesName = provider.name.toLowerCase().includes(query);
        const matchesId = provider.id.toLowerCase().includes(query);
        const matchesUrl = provider.base_url.toLowerCase().includes(query);
        if (!matchesName && !matchesId && !matchesUrl) return false;
      }

      // Group filter
      if (groupFilter && provider.group_id !== groupFilter) return false;

      // Status filter
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

  const handleAddProvider = async (data: ProviderInput) => {
    setMutationError(null);
    try {
      await createProvider(data);
      setShowAddModal(false);
    } catch (err) {
      const message =
        err instanceof Error ? err.message : "Failed to create provider";
      setMutationError(message);
    }
  };

  const getGroupName = (groupId: string | null) => {
    if (!groupId) return "—";
    const group = groups.find((g) => g.id === groupId);
    return group?.name ?? groupId;
  };

  if (error) {
    return (
      <div className="space-y-6">
        <div className="card">
          <div className="empty-state">
            <div className="w-16 h-16 mx-auto mb-4 bg-danger-light rounded-full flex items-center justify-center">
              <span className="text-3xl">⚠️</span>
            </div>
            <p className="font-medium text-text-primary mb-1">
              Failed to load providers
            </p>
            <p className="text-sm text-text-muted mb-4">{error.message}</p>
            <button onClick={() => refetch()} className="btn btn-primary">
              <span>🔄</span>
              Retry
            </button>
          </div>
        </div>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      {/* Page Header */}
      <div className="flex items-center justify-between">
        <div>
          <h2 className="text-2xl font-bold text-text-primary">Providers</h2>
          <p className="text-text-secondary mt-1">管理 AI 供应商配置</p>
        </div>
        <div className="flex items-center gap-2">
          <button
            onClick={() => refetch()}
            disabled={loading}
            className="btn btn-secondary btn-sm"
          >
            <span className={loading ? "animate-spin" : ""}>🔄</span>
            Refresh
          </button>
          <button
            onClick={() => setShowAddModal(true)}
            className="btn btn-primary"
          >
            <span>➕</span>
            Add Provider
          </button>
        </div>
      </div>

      {/* Mutation Error Alert */}
      {mutationError && (
        <div className="flex items-center justify-between p-3 rounded-lg border border-danger-light bg-danger-light/20">
          <div className="flex items-center gap-2">
            <span>⚠️</span>
            <span className="text-sm text-danger-dark">{mutationError}</span>
          </div>
          <button
            onClick={() => setMutationError(null)}
            className="text-danger-dark hover:text-danger transition-colors"
            aria-label="Dismiss error"
          >
            ✕
          </button>
        </div>
      )}

      {/* Filter Bar */}
      <div className="flex items-center gap-3">
        <div className="flex-1 relative">
          <span className="absolute left-3 top-1/2 -translate-y-1/2 text-text-muted">
            🔍
          </span>
          <input
            type="text"
            placeholder="Search providers..."
            className="input pl-10"
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
          />
        </div>
        <select
          className="input w-auto"
          value={groupFilter}
          onChange={(e) => setGroupFilter(e.target.value)}
        >
          <option value="">All Groups</option>
          {groups.map((group) => (
            <option key={group.id} value={group.id}>
              {group.name}
            </option>
          ))}
        </select>
        <select
          className="input w-auto"
          value={statusFilter}
          onChange={(e) => setStatusFilter(e.target.value as StatusFilter)}
        >
          <option value="">All Status</option>
          <option value="healthy">Healthy</option>
          <option value="unhealthy">Unhealthy</option>
          <option value="disabled">Disabled</option>
        </select>
      </div>

      {/* Providers Table */}
      <div className="card overflow-hidden p-0">
        <table className="w-full">
          <thead className="table-header">
            <tr>
              <th className="table-cell text-left text-xs font-medium text-text-secondary uppercase tracking-wider">
                Provider
              </th>
              <th className="table-cell text-left text-xs font-medium text-text-secondary uppercase tracking-wider">
                Group
              </th>
              <th className="table-cell text-left text-xs font-medium text-text-secondary uppercase tracking-wider">
                API Types
              </th>
              <th className="table-cell text-left text-xs font-medium text-text-secondary uppercase tracking-wider">
                Status
              </th>
              <th className="table-cell text-left text-xs font-medium text-text-secondary uppercase tracking-wider">
                Priority / Weight
              </th>
              <th className="table-cell text-right text-xs font-medium text-text-secondary uppercase tracking-wider">
                Actions
              </th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            <ProvidersTableBody
              loading={loading}
              providers={providers}
              filteredProviders={filteredProviders}
              onToggle={handleToggleProvider}
              onDelete={handleDeleteProvider}
              onAddClick={() => setShowAddModal(true)}
              getGroupName={getGroupName}
            />
          </tbody>
        </table>
      </div>

      {/* Help Card */}
      <div className="card bg-primary-light border-primary/20">
        <div className="flex items-start gap-4">
          <div className="w-10 h-10 bg-primary rounded-lg flex items-center justify-center shrink-0">
            <span className="text-white">💡</span>
          </div>
          <div>
            <h4 className="font-semibold text-text-primary">Getting Started</h4>
            <p className="text-sm text-text-secondary mt-1">
              Configure your AI providers with their base URL and API key. You
              can group providers and set up load balancing strategies to
              automatically switch between them.
            </p>
          </div>
        </div>
      </div>

      {/* Add Provider Modal */}
      {showAddModal && (
        <AddProviderModal
          onClose={() => setShowAddModal(false)}
          onSubmit={handleAddProvider}
          groups={groups}
        />
      )}
    </div>
  );
}
