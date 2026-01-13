import { useMemo } from "react";
import type { LogFilter, Provider } from "../api/types";
import { API_TYPES } from "../config/constants";

// API Types list derived from constants
const API_TYPES_LIST = Object.values(API_TYPES);

// Date range presets
const DATE_PRESETS = [
  { label: "All Time", value: "" },
  { label: "Last 1 Hour", value: "1h" },
  { label: "Last 24 Hours", value: "24h" },
  { label: "Last 7 Days", value: "7d" },
  { label: "Last 30 Days", value: "30d" },
] as const;

interface LogFiltersProps {
  filter: LogFilter;
  onFilterChange: (filter: Partial<LogFilter>) => void;
  providers: Provider[];
  onClear: () => void;
}

export function LogFilters({
  filter,
  onFilterChange,
  providers,
  onClear,
}: LogFiltersProps) {
  // Check if any filter is active
  const hasActiveFilters = useMemo(() => {
    return !!(
      filter.provider_id ||
      filter.api_type ||
      filter.success !== undefined ||
      filter.start_time ||
      filter.end_time
    );
  }, [filter]);

  // Handle date preset change
  const handleDatePresetChange = (preset: string) => {
    if (!preset) {
      onFilterChange({ start_time: undefined, end_time: undefined });
      return;
    }

    const now = new Date();
    let startTime: Date;

    switch (preset) {
      case "1h":
        startTime = new Date(now.getTime() - 60 * 60 * 1000);
        break;
      case "24h":
        startTime = new Date(now.getTime() - 24 * 60 * 60 * 1000);
        break;
      case "7d":
        startTime = new Date(now.getTime() - 7 * 24 * 60 * 60 * 1000);
        break;
      case "30d":
        startTime = new Date(now.getTime() - 30 * 24 * 60 * 60 * 1000);
        break;
      default:
        return;
    }

    onFilterChange({
      start_time: startTime.toISOString(),
      end_time: now.toISOString(),
    });
  };

  // Determine current date preset based on filter
  const currentDatePreset = useMemo(() => {
    if (!filter.start_time) return "";

    const start = new Date(filter.start_time);
    const now = new Date();
    const diffMs = now.getTime() - start.getTime();
    const diffHours = diffMs / (60 * 60 * 1000);

    if (diffHours <= 1.1) return "1h";
    if (diffHours <= 24.1) return "24h";
    if (diffHours <= 24 * 7.1) return "7d";
    if (diffHours <= 24 * 30.1) return "30d";
    return "";
  }, [filter.start_time]);

  return (
    <div className="card p-4">
      <div className="flex flex-wrap items-center gap-4">
        {/* Provider Filter */}
        <div className="flex flex-col gap-1">
          <label className="text-xs text-text-muted font-medium">
            Provider
          </label>
          <select
            value={filter.provider_id || ""}
            onChange={(e) =>
              onFilterChange({
                provider_id: e.target.value || undefined,
              })
            }
            className="input input-sm min-w-[150px]"
          >
            <option value="">All Providers</option>
            {providers.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name}
              </option>
            ))}
          </select>
        </div>

        {/* Status Filter */}
        <div className="flex flex-col gap-1">
          <label className="text-xs text-text-muted font-medium">Status</label>
          <select
            value={filter.success === undefined ? "" : String(filter.success)}
            onChange={(e) => {
              const val = e.target.value;
              onFilterChange({
                success: val === "" ? undefined : val === "true",
              });
            }}
            className="input input-sm min-w-[120px]"
          >
            <option value="">All Status</option>
            <option value="true">Success</option>
            <option value="false">Failed</option>
          </select>
        </div>

        {/* API Type Filter */}
        <div className="flex flex-col gap-1">
          <label className="text-xs text-text-muted font-medium">
            API Type
          </label>
          <select
            value={filter.api_type || ""}
            onChange={(e) =>
              onFilterChange({
                api_type: e.target.value || undefined,
              })
            }
            className="input input-sm min-w-[120px]"
          >
            <option value="">All Types</option>
            {API_TYPES_LIST.map((type) => (
              <option key={type} value={type}>
                {type}
              </option>
            ))}
          </select>
        </div>

        {/* Date Range Filter */}
        <div className="flex flex-col gap-1">
          <label className="text-xs text-text-muted font-medium">
            Time Range
          </label>
          <select
            value={currentDatePreset}
            onChange={(e) => handleDatePresetChange(e.target.value)}
            className="input input-sm min-w-[140px]"
          >
            {DATE_PRESETS.map((preset) => (
              <option key={preset.value} value={preset.value}>
                {preset.label}
              </option>
            ))}
          </select>
        </div>

        {/* Clear Filters Button */}
        {hasActiveFilters && (
          <div className="flex flex-col gap-1">
            <label className="text-xs text-transparent font-medium">
              Clear
            </label>
            <button
              onClick={onClear}
              className="btn btn-secondary btn-sm flex items-center gap-1"
            >
              <span>✕</span>
              Clear Filters
            </button>
          </div>
        )}
      </div>

      {/* Active Filters Summary */}
      {hasActiveFilters && (
        <div className="mt-3 pt-3 border-t border-border-light">
          <div className="flex flex-wrap items-center gap-2 text-sm">
            <span className="text-text-muted">Active filters:</span>
            {filter.provider_id && (
              <FilterBadge
                label={`Provider: ${providers.find((p) => p.id === filter.provider_id)?.name || filter.provider_id}`}
                onRemove={() => onFilterChange({ provider_id: undefined })}
              />
            )}
            {filter.success !== undefined && (
              <FilterBadge
                label={`Status: ${filter.success ? "Success" : "Failed"}`}
                onRemove={() => onFilterChange({ success: undefined })}
              />
            )}
            {filter.api_type && (
              <FilterBadge
                label={`Type: ${filter.api_type}`}
                onRemove={() => onFilterChange({ api_type: undefined })}
              />
            )}
            {filter.start_time && (
              <FilterBadge
                label={`Since: ${new Date(filter.start_time).toLocaleDateString()}`}
                onRemove={() =>
                  onFilterChange({ start_time: undefined, end_time: undefined })
                }
              />
            )}
          </div>
        </div>
      )}
    </div>
  );
}

interface FilterBadgeProps {
  label: string;
  onRemove: () => void;
}

function FilterBadge({ label, onRemove }: FilterBadgeProps) {
  return (
    <span className="inline-flex items-center gap-1 px-2 py-1 rounded-md bg-primary/10 text-primary text-xs font-medium">
      {label}
      <button
        onClick={onRemove}
        className="hover:text-primary-hover cursor-pointer ml-1"
        aria-label={`Remove ${label} filter`}
      >
        ✕
      </button>
    </span>
  );
}
