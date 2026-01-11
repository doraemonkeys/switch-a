import { useState, useMemo } from "react";
import { useProviders } from "../hooks/useProviders";
import { useGroups } from "../hooks/useGroups";
import type { Provider, ProviderInput } from "../api/client";

type ProviderStatusType = "healthy" | "unhealthy" | "disabled";
type StatusFilter = "" | "healthy" | "unhealthy" | "disabled";

function getProviderStatus(enabled: boolean): ProviderStatusType {
  if (!enabled) return "disabled";
  return "healthy"; // Default to healthy since we don't have health data in Provider type
}

const statusDotClass: Record<ProviderStatusType, string> = {
  healthy: "bg-success",
  unhealthy: "bg-danger",
  disabled: "bg-text-muted",
};

const statusBadgeClass: Record<ProviderStatusType, string> = {
  healthy: "bg-success-light text-success-dark",
  unhealthy: "bg-danger-light text-danger-dark",
  disabled: "bg-gray-100 text-gray-600",
};

const statusLabel: Record<ProviderStatusType, string> = {
  healthy: "Healthy",
  unhealthy: "Unhealthy",
  disabled: "Disabled",
};

interface ProvidersTableBodyProps {
  loading: boolean;
  providers: Provider[];
  filteredProviders: Provider[];
  onToggle: (provider: Provider) => void;
  onDelete: (provider: Provider) => void;
  onAddClick: () => void;
  getGroupName: (groupId: string | null) => string;
}

function ProvidersTableBody({
  loading,
  providers,
  filteredProviders,
  onToggle,
  onDelete,
  onAddClick,
  getGroupName,
}: ProvidersTableBodyProps) {
  if (loading && providers.length === 0) {
    return (
      <tr>
        <td colSpan={6} className="px-4 py-16">
          <div className="empty-state">
            <div className="w-8 h-8 mx-auto mb-4 border-2 border-primary border-t-transparent rounded-full animate-spin" />
            <p className="text-sm text-text-muted">Loading providers...</p>
          </div>
        </td>
      </tr>
    );
  }

  if (filteredProviders.length === 0) {
    const noProvidersConfigured = providers.length === 0;
    return (
      <tr>
        <td colSpan={6} className="px-4 py-16">
          <div className="empty-state">
            <div className="w-20 h-20 mx-auto mb-4 bg-bg-tertiary rounded-2xl flex items-center justify-center">
              <span className="text-4xl">🔌</span>
            </div>
            <p className="font-medium text-text-primary mb-1">
              {noProvidersConfigured
                ? "No providers configured yet"
                : "No providers match your filters"}
            </p>
            <p className="text-sm text-text-muted mb-4">
              {noProvidersConfigured
                ? "Add your first AI provider to start proxying requests."
                : "Try adjusting your search or filter criteria."}
            </p>
            {noProvidersConfigured && (
              <button onClick={onAddClick} className="btn btn-primary">
                <span>➕</span>
                Add Provider
              </button>
            )}
          </div>
        </td>
      </tr>
    );
  }

  return (
    <>
      {filteredProviders.map((provider) => {
        const status = getProviderStatus(provider.enabled);
        return (
          <tr
            key={provider.id}
            className="hover:bg-bg-secondary/50 transition-colors"
          >
            <td className="table-cell">
              <div className="flex items-center gap-3">
                <div
                  className={`w-2 h-2 rounded-full ${statusDotClass[status]}`}
                />
                <div>
                  <p className="font-medium text-text-primary">
                    {provider.name}
                  </p>
                  <p className="text-xs text-text-muted truncate max-w-[200px]">
                    {provider.base_url}
                  </p>
                </div>
              </div>
            </td>
            <td className="table-cell">
              <span className="badge badge-neutral">
                {getGroupName(provider.group_id)}
              </span>
            </td>
            <td className="table-cell">
              <div className="flex flex-wrap gap-1">
                {provider.api_types?.map((apiType) => (
                  <span
                    key={apiType.api_type}
                    className="px-1.5 py-0.5 text-xs rounded bg-primary-light text-primary-dark"
                  >
                    {apiType.api_type}
                  </span>
                )) ?? <span className="text-text-muted text-sm">—</span>}
              </div>
            </td>
            <td className="table-cell">
              <span
                className={`px-2 py-1 rounded text-xs font-medium ${statusBadgeClass[status]}`}
              >
                {statusLabel[status]}
              </span>
            </td>
            <td className="table-cell">
              <span className="text-sm text-text-secondary">
                P{provider.priority} / W{provider.weight}
              </span>
            </td>
            <td className="table-cell text-right">
              <div className="flex items-center justify-end gap-1">
                <button
                  onClick={() => onToggle(provider)}
                  className="btn btn-ghost btn-sm"
                  title={provider.enabled ? "Disable" : "Enable"}
                >
                  {provider.enabled ? "⏸️" : "▶️"}
                </button>
                <button
                  onClick={() => onDelete(provider)}
                  className="btn btn-ghost btn-sm text-danger hover:bg-danger-light"
                  title="Delete"
                >
                  🗑️
                </button>
              </div>
            </td>
          </tr>
        );
      })}
    </>
  );
}

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
        const status = getProviderStatus(provider.enabled);
        if (statusFilter !== status) return false;
      }

      return true;
    });
  }, [providers, searchQuery, groupFilter, statusFilter]);

  const handleToggleProvider = async (provider: Provider) => {
    try {
      if (provider.enabled) {
        await disableProvider(provider.id);
      } else {
        await enableProvider(provider.id);
      }
    } catch (err) {
      console.error("Failed to toggle provider:", err);
    }
  };

  const handleDeleteProvider = async (provider: Provider) => {
    if (
      !confirm(`Are you sure you want to delete provider "${provider.name}"?`)
    ) {
      return;
    }
    try {
      await deleteProvider(provider.id);
    } catch (err) {
      console.error("Failed to delete provider:", err);
    }
  };

  const handleAddProvider = async (data: ProviderInput) => {
    try {
      await createProvider(data);
      setShowAddModal(false);
    } catch (err) {
      console.error("Failed to create provider:", err);
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

interface AddProviderModalProps {
  onClose: () => void;
  onSubmit: (data: ProviderInput) => Promise<void>;
  groups: Array<{ id: string; name: string }>;
}

function AddProviderModal({
  onClose,
  onSubmit,
  groups,
}: AddProviderModalProps) {
  const [formData, setFormData] = useState<ProviderInput>({
    name: "",
    base_url: "",
    api_key: "",
    api_types: [],
    group_id: null,
    weight: 1,
    priority: 0,
    enabled: true,
  });
  const [submitting, setSubmitting] = useState(false);
  const [apiTypesInput, setApiTypesInput] = useState("");

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setSubmitting(true);
    try {
      await onSubmit({
        ...formData,
        api_types: apiTypesInput
          .split(",")
          .map((s) => s.trim())
          .filter(Boolean),
      });
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50">
      <div className="bg-bg-primary rounded-xl shadow-xl w-full max-w-lg mx-4 max-h-[90vh] overflow-y-auto">
        <div className="p-6 border-b border-border">
          <div className="flex items-center justify-between">
            <h3 className="text-lg font-semibold text-text-primary">
              Add Provider
            </h3>
            <button
              onClick={onClose}
              className="text-text-muted hover:text-text-primary"
            >
              ✕
            </button>
          </div>
        </div>
        <form onSubmit={handleSubmit} className="p-6 space-y-4">
          <div>
            <label className="block text-sm font-medium text-text-secondary mb-1">
              Name
            </label>
            <input
              type="text"
              className="input"
              value={formData.name}
              onChange={(e) =>
                setFormData((prev) => ({ ...prev, name: e.target.value }))
              }
              required
              placeholder="e.g., OpenAI Production"
            />
          </div>
          <div>
            <label className="block text-sm font-medium text-text-secondary mb-1">
              Base URL
            </label>
            <input
              type="url"
              className="input"
              value={formData.base_url}
              onChange={(e) =>
                setFormData((prev) => ({ ...prev, base_url: e.target.value }))
              }
              required
              placeholder="e.g., https://api.openai.com"
            />
          </div>
          <div>
            <label className="block text-sm font-medium text-text-secondary mb-1">
              API Key
            </label>
            <input
              type="password"
              className="input"
              value={formData.api_key}
              onChange={(e) =>
                setFormData((prev) => ({ ...prev, api_key: e.target.value }))
              }
              required
              placeholder="sk-..."
            />
          </div>
          <div>
            <label className="block text-sm font-medium text-text-secondary mb-1">
              API Types (comma-separated)
            </label>
            <input
              type="text"
              className="input"
              value={apiTypesInput}
              onChange={(e) => setApiTypesInput(e.target.value)}
              placeholder="e.g., chat, embeddings, images"
            />
          </div>
          <div>
            <label className="block text-sm font-medium text-text-secondary mb-1">
              Group
            </label>
            <select
              className="input"
              value={formData.group_id ?? ""}
              onChange={(e) =>
                setFormData((prev) => ({
                  ...prev,
                  group_id: e.target.value || null,
                }))
              }
            >
              <option value="">No Group</option>
              {groups.map((group) => (
                <option key={group.id} value={group.id}>
                  {group.name}
                </option>
              ))}
            </select>
          </div>
          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className="block text-sm font-medium text-text-secondary mb-1">
                Priority
              </label>
              <input
                type="number"
                className="input"
                value={formData.priority}
                onChange={(e) =>
                  setFormData((prev) => ({
                    ...prev,
                    priority: parseInt(e.target.value) || 0,
                  }))
                }
                min={0}
              />
            </div>
            <div>
              <label className="block text-sm font-medium text-text-secondary mb-1">
                Weight
              </label>
              <input
                type="number"
                className="input"
                value={formData.weight}
                onChange={(e) =>
                  setFormData((prev) => ({
                    ...prev,
                    weight: parseInt(e.target.value) || 1,
                  }))
                }
                min={1}
              />
            </div>
          </div>
          <div className="flex items-center gap-2">
            <input
              type="checkbox"
              id="enabled"
              checked={formData.enabled}
              onChange={(e) =>
                setFormData((prev) => ({ ...prev, enabled: e.target.checked }))
              }
              className="w-4 h-4 rounded border-border text-primary focus:ring-primary"
            />
            <label
              htmlFor="enabled"
              className="text-sm font-medium text-text-secondary"
            >
              Enable provider immediately
            </label>
          </div>
          <div className="flex justify-end gap-3 pt-4">
            <button
              type="button"
              onClick={onClose}
              className="btn btn-secondary"
              disabled={submitting}
            >
              Cancel
            </button>
            <button
              type="submit"
              className="btn btn-primary"
              disabled={submitting}
            >
              {submitting ? (
                <>
                  <span className="animate-spin">⏳</span>
                  Creating...
                </>
              ) : (
                <>
                  <span>➕</span>
                  Add Provider
                </>
              )}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}
