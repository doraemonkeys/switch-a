import type { Provider } from "../../api/client";
import {
  getProviderStatus,
  statusDotClass,
  statusBadgeClass,
  statusLabel,
} from "./types";

export interface ProvidersTableBodyProps {
  loading: boolean;
  providers: Provider[];
  filteredProviders: Provider[];
  onToggle: (provider: Provider) => void;
  onEdit: (provider: Provider) => void;
  onDelete: (provider: Provider) => void;
  onAddClick: () => void;
  getGroupName: (groupId: string | null) => string;
}

export function ProvidersTableBody({
  loading,
  providers,
  filteredProviders,
  onToggle,
  onEdit,
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
        const status = getProviderStatus(
          provider.enabled,
          provider.health?.available,
        );
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
                  onClick={() => onEdit(provider)}
                  className="btn btn-ghost btn-sm"
                  title="Edit"
                >
                  ✏️
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
