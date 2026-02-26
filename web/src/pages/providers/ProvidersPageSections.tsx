import type { Group } from "../../api/client";
import type { StatusFilter } from "./types";
import { RefreshIntervalSelect } from "../../components/RefreshIntervalSelect";
import {
  Plus,
  RefreshCw,
  Search,
  AlertTriangle,
  Lightbulb,
  X,
  ServerCrash,
} from "lucide-react";

interface PageHeaderProps {
  loading: boolean;
  onRefresh: () => void;
  onAddClick: () => void;
  refreshInterval: number;
  onRefreshIntervalChange: (interval: number) => void;
}

export function PageHeader({
  loading,
  onRefresh,
  onAddClick,
  refreshInterval,
  onRefreshIntervalChange,
}: PageHeaderProps) {
  return (
    <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 bg-white p-6 rounded-2xl border border-border shadow-sm">
      <div>
        <h2 className="text-2xl font-bold text-text-primary tracking-tight">
          Providers
        </h2>
        <p className="text-sm text-text-secondary mt-1.5">
          Manage AI provider configurations and routing
        </p>
      </div>
      <div className="flex flex-wrap items-center gap-3">
        <div className="flex items-center gap-2 bg-bg-secondary px-3 py-1.5 rounded-lg border border-border">
          <RefreshIntervalSelect
            value={refreshInterval}
            onChange={onRefreshIntervalChange}
            showLabel
          />
        </div>
        <button
          onClick={onRefresh}
          disabled={loading}
          className="btn btn-secondary h-10 px-4"
        >
          <RefreshCw
            className={`w-4 h-4 ${loading ? "animate-spin text-primary" : "text-text-secondary"}`}
          />
          <span>Refresh</span>
        </button>
        <button
          onClick={onAddClick}
          className="btn btn-primary h-10 px-5 shadow-md shadow-primary/20"
        >
          <Plus className="w-4 h-4" />
          <span>Add Provider</span>
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
    <div className="flex items-center justify-between p-4 rounded-xl border border-danger/20 bg-danger/5 shadow-sm">
      <div className="flex items-center gap-3">
        <AlertTriangle className="w-5 h-5 text-danger" />
        <span className="text-sm font-medium text-danger-dark">{error}</span>
      </div>
      <button
        onClick={onDismiss}
        className="p-1 rounded-md text-danger/70 hover:text-danger hover:bg-danger/10 transition-colors"
        aria-label="Dismiss error"
      >
        <X className="w-4 h-4" />
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
    <div className="flex flex-col md:flex-row items-stretch md:items-center gap-4 bg-white p-4 rounded-2xl border border-border shadow-sm">
      <div className="flex-1 relative group">
        <Search className="absolute left-3.5 top-1/2 -translate-y-1/2 w-4 h-4 text-text-muted group-focus-within:text-primary transition-colors" />
        <input
          type="text"
          placeholder="Search providers by name, ID or URL..."
          className="input pl-10 h-10 bg-bg-secondary border-transparent focus:bg-white focus:border-primary w-full"
          value={searchQuery}
          onChange={(e) => onSearchChange(e.target.value)}
        />
      </div>
      <div className="flex items-center gap-3">
        <select
          className="input h-10 min-w-[140px] bg-bg-secondary border-transparent focus:bg-white focus:border-primary"
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
          className="input h-10 min-w-[160px] bg-bg-secondary border-transparent focus:bg-white focus:border-primary"
          value={statusFilter}
          onChange={(e) => onStatusFilterChange(e.target.value as StatusFilter)}
        >
          <option value="">All Statuses</option>
          <option value="healthy">Healthy</option>
          <option value="unhealthy">Circuit Open</option>
          <option value="pending-recovery">Pending Recovery</option>
          <option value="disabled">Disabled</option>
        </select>
      </div>
    </div>
  );
}

export function HelpCard() {
  return (
    <div className="bg-gradient-to-br from-primary-light/50 to-primary-light/20 border border-primary/20 rounded-2xl p-5 shadow-sm">
      <div className="flex items-start gap-4">
        <div className="w-10 h-10 bg-white rounded-xl shadow-sm border border-primary/10 flex items-center justify-center shrink-0">
          <Lightbulb className="w-5 h-5 text-primary" />
        </div>
        <div>
          <h4 className="font-semibold text-text-primary text-sm">
            Getting Started
          </h4>
          <p className="text-sm text-text-secondary mt-1 leading-relaxed max-w-3xl">
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
    <div className="card p-12 flex items-center justify-center min-h-[400px]">
      <div className="max-w-md text-center">
        <div className="w-16 h-16 mx-auto mb-5 bg-danger/10 rounded-2xl flex items-center justify-center">
          <ServerCrash className="w-8 h-8 text-danger" />
        </div>
        <h3 className="text-xl font-semibold text-text-primary mb-2">
          Failed to load providers
        </h3>
        <p className="text-sm text-text-secondary mb-6 leading-relaxed">
          {message}
        </p>
        <button
          onClick={onRetry}
          className="btn btn-primary inline-flex items-center gap-2"
        >
          <RefreshCw className="w-4 h-4" />
          <span>Retry Connection</span>
        </button>
      </div>
    </div>
  );
}

const alignClass: Record<string, string> = {
  left: "text-left",
  center: "text-center",
  right: "text-right",
};

export function ProvidersTableHeader() {
  const headers = [
    { label: "Provider", align: "left", width: "w-[22%]" },
    { label: "Group", align: "left", width: "w-[12%]" },
    { label: "Types", align: "left", width: "w-[15%]" },
    { label: "Status", align: "left", width: "w-[15%]" },
    { label: "Errors", align: "center", width: "w-[8%]" },
    { label: "Recovery", align: "left", width: "w-[12%]" },
    { label: "Routing", align: "left", width: "w-[10%]" },
    { label: "", align: "right", width: "w-[6%]" }, // Actions column
  ];

  return (
    <thead className="bg-bg-secondary border-b border-border/60">
      <tr>
        {headers.map((header) => (
          <th
            key={header.label}
            className={`py-3.5 px-4 text-[11px] font-semibold text-text-secondary uppercase tracking-wider ${alignClass[header.align]} ${header.width}`}
          >
            {header.label}
          </th>
        ))}
      </tr>
    </thead>
  );
}
