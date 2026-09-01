import {
  Filter,
  KeyRound,
  LayoutGrid,
  List,
  Search,
  SlidersHorizontal,
  Sparkles,
  X,
} from "lucide-react";

export type CredentialKindFilter = "all" | "api_key" | "chatgpt";
export type CredentialStatusFilter = "all" | "active" | "reauth_required";
export type CredentialUsageFilter = "all" | "in_use" | "unused";
export type CredentialSortOption = "updated" | "name" | "routes";
export type CredentialViewMode = "grid" | "table";

interface CredentialFilterToolbarProps {
  search: string;
  onSearchChange: (value: string) => void;
  kindFilter: CredentialKindFilter;
  onKindFilterChange: (kind: CredentialKindFilter) => void;
  statusFilter: CredentialStatusFilter;
  onStatusFilterChange: (status: CredentialStatusFilter) => void;
  usageFilter: CredentialUsageFilter;
  onUsageFilterChange: (usage: CredentialUsageFilter) => void;
  sortOption: CredentialSortOption;
  onSortChange: (sort: CredentialSortOption) => void;
  viewMode: CredentialViewMode;
  onViewModeChange: (mode: CredentialViewMode) => void;
  totalCount: number;
  filteredCount: number;
  onResetFilters: () => void;
}

export function CredentialFilterToolbar({
  search,
  onSearchChange,
  kindFilter,
  onKindFilterChange,
  statusFilter,
  onStatusFilterChange,
  usageFilter,
  onUsageFilterChange,
  sortOption,
  onSortChange,
  viewMode,
  onViewModeChange,
  totalCount,
  filteredCount,
  onResetFilters,
}: CredentialFilterToolbarProps) {
  const hasActiveFilters =
    search.trim() !== "" ||
    kindFilter !== "all" ||
    statusFilter !== "all" ||
    usageFilter !== "all";

  return (
    <div className="flex flex-col gap-3 rounded-2xl border border-border bg-white p-4 shadow-xs">
      {/* Upper row: Search bar & Quick Kind Tabs & View Mode */}
      <div className="flex flex-col gap-3 lg:flex-row lg:items-center lg:justify-between">
        {/* Search Input */}
        <div className="relative flex-1">
          <Search className="absolute left-3.5 top-1/2 h-4 w-4 -translate-y-1/2 text-text-muted" />
          <input
            type="text"
            className="input h-10 w-full pl-9 pr-8 text-sm placeholder:text-text-muted focus:ring-primary/20"
            placeholder="Search by credential name, id, provider, or route..."
            value={search}
            onChange={(e) => onSearchChange(e.target.value)}
          />
          {search && (
            <button
              type="button"
              className="absolute right-2.5 top-1/2 -translate-y-1/2 rounded-full p-1 text-text-muted hover:bg-bg-secondary hover:text-text-primary"
              onClick={() => onSearchChange("")}
              aria-label="Clear search"
            >
              <X className="h-3.5 w-3.5" />
            </button>
          )}
        </div>

        {/* Kind Filter Tabs */}
        <div className="inline-flex rounded-xl bg-bg-secondary p-1">
          <button
            type="button"
            className={`rounded-lg px-3 py-1.5 text-xs font-medium transition-all ${
              kindFilter === "all"
                ? "bg-white text-text-primary shadow-xs font-semibold"
                : "text-text-secondary hover:text-text-primary"
            }`}
            onClick={() => onKindFilterChange("all")}
          >
            All Types
          </button>
          <button
            type="button"
            className={`inline-flex items-center gap-1.5 rounded-lg px-3 py-1.5 text-xs font-medium transition-all ${
              kindFilter === "api_key"
                ? "bg-white text-text-primary shadow-xs font-semibold"
                : "text-text-secondary hover:text-text-primary"
            }`}
            onClick={() => onKindFilterChange("api_key")}
          >
            <KeyRound className="h-3.5 w-3.5 text-primary" />
            API Keys
          </button>
          <button
            type="button"
            className={`inline-flex items-center gap-1.5 rounded-lg px-3 py-1.5 text-xs font-medium transition-all ${
              kindFilter === "chatgpt"
                ? "bg-white text-text-primary shadow-xs font-semibold"
                : "text-text-secondary hover:text-text-primary"
            }`}
            onClick={() => onKindFilterChange("chatgpt")}
          >
            <Sparkles className="h-3.5 w-3.5 text-emerald-600" />
            ChatGPT Sessions
          </button>
        </div>

        {/* View Mode Toggle */}
        <div className="inline-flex items-center rounded-xl bg-bg-secondary p-1">
          <button
            type="button"
            className={`rounded-lg p-1.5 text-xs transition-all ${
              viewMode === "grid"
                ? "bg-white text-primary shadow-xs"
                : "text-text-secondary hover:text-text-primary"
            }`}
            onClick={() => onViewModeChange("grid")}
            aria-label="Grid View"
            title="Card Grid View"
          >
            <LayoutGrid className="h-4 w-4" />
          </button>
          <button
            type="button"
            className={`rounded-lg p-1.5 text-xs transition-all ${
              viewMode === "table"
                ? "bg-white text-primary shadow-xs"
                : "text-text-secondary hover:text-text-primary"
            }`}
            onClick={() => onViewModeChange("table")}
            aria-label="Table View"
            title="Dense Table View"
          >
            <List className="h-4 w-4" />
          </button>
        </div>
      </div>

      {/* Lower row: Status, Usage, Sorting, and Filter summary */}
      <div className="flex flex-wrap items-center justify-between gap-3 border-t border-border/50 pt-3 text-xs">
        <div className="flex flex-wrap items-center gap-2">
          {/* Status Filter */}
          <div className="flex items-center gap-1.5">
            <Filter className="h-3.5 w-3.5 text-text-muted" />
            <select
              className="rounded-lg border border-border bg-white px-2.5 py-1 text-xs text-text-primary focus:border-primary focus:outline-none"
              value={statusFilter}
              onChange={(e) =>
                onStatusFilterChange(e.target.value as CredentialStatusFilter)
              }
              aria-label="Filter by auth status"
            >
              <option value="all">All Statuses</option>
              <option value="active">Active Only</option>
              <option value="reauth_required">Needs Re-auth</option>
            </select>
          </div>

          {/* Usage Filter */}
          <select
            className="rounded-lg border border-border bg-white px-2.5 py-1 text-xs text-text-primary focus:border-primary focus:outline-none"
            value={usageFilter}
            onChange={(e) =>
              onUsageFilterChange(e.target.value as CredentialUsageFilter)
            }
            aria-label="Filter by usage"
          >
            <option value="all">All Usages</option>
            <option value="in_use">Bound to Routes</option>
            <option value="unused">Unreferenced / Unused</option>
          </select>

          {/* Sort Selector */}
          <div className="flex items-center gap-1.5">
            <SlidersHorizontal className="h-3.5 w-3.5 text-text-muted" />
            <select
              className="rounded-lg border border-border bg-white px-2.5 py-1 text-xs text-text-primary focus:border-primary focus:outline-none"
              value={sortOption}
              onChange={(e) =>
                onSortChange(e.target.value as CredentialSortOption)
              }
              aria-label="Sort credentials"
            >
              <option value="updated">Recently Updated</option>
              <option value="name">Name (A-Z)</option>
              <option value="routes">Most Routes First</option>
            </select>
          </div>

          {/* Reset Filters button */}
          {hasActiveFilters && (
            <button
              type="button"
              onClick={onResetFilters}
              className="inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs text-text-muted hover:bg-bg-secondary hover:text-text-primary"
            >
              <X className="h-3 w-3" />
              Reset Filters
            </button>
          )}
        </div>

        {/* Count Summary */}
        <div className="text-xs text-text-muted">
          Showing{" "}
          <span className="font-semibold text-text-primary">
            {filteredCount}
          </span>{" "}
          of {totalCount} credentials
        </div>
      </div>
    </div>
  );
}
