import type { Group } from "../../api/client";
import type { StatusFilter } from "./types";

interface PageHeaderProps {
  loading: boolean;
  onRefresh: () => void;
  onAddClick: () => void;
}

export function PageHeader({
  loading,
  onRefresh,
  onAddClick,
}: PageHeaderProps) {
  return (
    <div className="flex items-center justify-between">
      <div>
        <h2 className="text-2xl font-bold text-text-primary">Providers</h2>
        <p className="text-text-secondary mt-1">管理 AI 供应商配置</p>
      </div>
      <div className="flex items-center gap-2">
        <button
          onClick={onRefresh}
          disabled={loading}
          className="btn btn-secondary btn-sm"
        >
          <span className={loading ? "animate-spin" : ""}>🔄</span>
          Refresh
        </button>
        <button onClick={onAddClick} className="btn btn-primary">
          <span>➕</span>
          Add Provider
        </button>
      </div>
    </div>
  );
}

interface MutationErrorAlertProps {
  error: string;
  onDismiss: () => void;
}

export function MutationErrorAlert({
  error,
  onDismiss,
}: MutationErrorAlertProps) {
  return (
    <div className="flex items-center justify-between p-3 rounded-lg border border-danger-light bg-danger-light/20">
      <div className="flex items-center gap-2">
        <span>⚠️</span>
        <span className="text-sm text-danger-dark">{error}</span>
      </div>
      <button
        onClick={onDismiss}
        className="text-danger-dark hover:text-danger transition-colors cursor-pointer"
        aria-label="Dismiss error"
      >
        ✕
      </button>
    </div>
  );
}

interface FilterBarProps {
  searchQuery: string;
  onSearchChange: (value: string) => void;
  groupFilter: string;
  onGroupFilterChange: (value: string) => void;
  statusFilter: StatusFilter;
  onStatusFilterChange: (value: StatusFilter) => void;
  groups: Group[];
}

export function FilterBar({
  searchQuery,
  onSearchChange,
  groupFilter,
  onGroupFilterChange,
  statusFilter,
  onStatusFilterChange,
  groups,
}: FilterBarProps) {
  return (
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
          onChange={(e) => onSearchChange(e.target.value)}
        />
      </div>
      <select
        className="input w-auto"
        value={groupFilter}
        onChange={(e) => onGroupFilterChange(e.target.value)}
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
        onChange={(e) => onStatusFilterChange(e.target.value as StatusFilter)}
      >
        <option value="">All Status</option>
        <option value="healthy">Healthy</option>
        <option value="unhealthy">Unhealthy</option>
        <option value="disabled">Disabled</option>
      </select>
    </div>
  );
}

export function HelpCard() {
  return (
    <div className="card bg-primary-light border-primary/20">
      <div className="flex items-start gap-4">
        <div className="w-10 h-10 bg-primary rounded-lg flex items-center justify-center shrink-0">
          <span className="text-white">💡</span>
        </div>
        <div>
          <h4 className="font-semibold text-text-primary">Getting Started</h4>
          <p className="text-sm text-text-secondary mt-1">
            Configure your AI providers with their base URL and API key. You can
            group providers and set up load balancing strategies to
            automatically switch between them.
          </p>
        </div>
      </div>
    </div>
  );
}

interface ErrorStateProps {
  message: string;
  onRetry: () => void;
}

export function ErrorState({ message, onRetry }: ErrorStateProps) {
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
          <p className="text-sm text-text-muted mb-4">{message}</p>
          <button onClick={onRetry} className="btn btn-primary">
            <span>🔄</span>
            Retry
          </button>
        </div>
      </div>
    </div>
  );
}

export function ProvidersTableHeader() {
  const headers = [
    { label: "Provider", align: "left" },
    { label: "Group", align: "left" },
    { label: "API Types", align: "left" },
    { label: "Status", align: "left" },
    { label: "Priority / Weight", align: "left" },
    { label: "Actions", align: "right" },
  ];

  return (
    <thead className="table-header">
      <tr>
        {headers.map((header) => (
          <th
            key={header.label}
            className={`table-cell text-${header.align} text-xs font-medium text-text-secondary uppercase tracking-wider`}
          >
            {header.label}
          </th>
        ))}
      </tr>
    </thead>
  );
}
