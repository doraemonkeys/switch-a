import type { LogFilter, Provider } from "../api/types";
import { API_TYPES } from "../config/constants";

// API Types list derived from constants
const API_TYPES_LIST = Object.values(API_TYPES);

// Helper function to get current retries filter value for select
function getRetriesFilterValue(filter: LogFilter): string {
  if (filter.has_retries === true) return "has_retries";
  if (filter.has_retries === false) return "no_retries";
  if (filter.min_retry_count !== undefined && filter.min_retry_count >= 1)
    return "1+";
  return "";
}

// Helper function to handle retries filter change
function handleRetriesFilterChange(
  value: string,
  onFilterChange: (filter: Partial<LogFilter>) => void,
): void {
  switch (value) {
    case "has_retries":
      onFilterChange({ has_retries: true, min_retry_count: undefined });
      break;
    case "no_retries":
      onFilterChange({ has_retries: false, min_retry_count: undefined });
      break;
    case "1+":
      onFilterChange({ has_retries: undefined, min_retry_count: 1 });
      break;
    default:
      onFilterChange({ has_retries: undefined, min_retry_count: undefined });
  }
}

// Helper function to get retries filter label for badge
function getRetriesFilterLabel(filter: LogFilter): string | null {
  if (filter.has_retries === true) return "Has Retries";
  if (filter.has_retries === false) return "No Retries";
  if (filter.min_retry_count !== undefined && filter.min_retry_count >= 1)
    return "1+ Retries";
  return null;
}

// Date range presets
const DATE_PRESETS = [
  { label: "All Time", value: "" },
  { label: "Last 1 Hour", value: "1h" },
  { label: "Last 24 Hours", value: "24h" },
  { label: "Last 7 Days", value: "7d" },
  { label: "Last 30 Days", value: "30d" },
] as const;

// Tolerance thresholds for date preset detection (10% buffer)
// These account for slight timing differences when comparing preset ranges
const DATE_PRESET_TOLERANCE = {
  ONE_HOUR: 1.1, // 1 hour + 10% tolerance
  ONE_DAY: 24.1, // 24 hours + ~10% tolerance
  SEVEN_DAYS: 24 * 7.1, // 7 days + ~10% tolerance
  THIRTY_DAYS: 24 * 30.1, // 30 days + ~10% tolerance
} as const;

// Helper to check if any filter is active
function hasActiveFilters(filter: LogFilter): boolean {
  return !!(
    filter.provider_id ||
    filter.api_type ||
    filter.success !== undefined ||
    filter.is_sse !== undefined ||
    filter.start_time ||
    filter.end_time ||
    filter.has_retries !== undefined ||
    filter.min_retry_count !== undefined
  );
}

// Helper to determine current date preset from filter
function getCurrentDatePreset(filter: LogFilter): string {
  if (!filter.start_time) return "";
  const start = new Date(filter.start_time);
  const now = new Date();
  const diffHours = (now.getTime() - start.getTime()) / (60 * 60 * 1000);
  if (diffHours <= DATE_PRESET_TOLERANCE.ONE_HOUR) return "1h";
  if (diffHours <= DATE_PRESET_TOLERANCE.ONE_DAY) return "24h";
  if (diffHours <= DATE_PRESET_TOLERANCE.SEVEN_DAYS) return "7d";
  if (diffHours <= DATE_PRESET_TOLERANCE.THIRTY_DAYS) return "30d";
  return "";
}

// Helper to handle date preset change
function applyDatePreset(
  preset: string,
  onFilterChange: (filter: Partial<LogFilter>) => void,
): void {
  if (!preset) {
    onFilterChange({ start_time: undefined, end_time: undefined });
    return;
  }
  const now = new Date();
  const offsets: Record<string, number> = {
    "1h": 60 * 60 * 1000,
    "24h": 24 * 60 * 60 * 1000,
    "7d": 7 * 24 * 60 * 60 * 1000,
    "30d": 30 * 24 * 60 * 60 * 1000,
  };
  const offset = offsets[preset];
  if (!offset) return;
  onFilterChange({
    start_time: new Date(now.getTime() - offset).toISOString(),
    end_time: now.toISOString(),
  });
}

interface FilterSelectProps {
  id: string;
  label: string;
  value: string;
  onChange: (value: string) => void;
  options: { value: string; label: string }[];
  minWidth?: string;
}

function FilterSelect({
  id,
  label,
  value,
  onChange,
  options,
  minWidth = "120px",
}: FilterSelectProps) {
  return (
    <div className="flex flex-col gap-1">
      <label htmlFor={id} className="text-xs text-text-muted font-medium">
        {label}
      </label>
      <select
        id={id}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className="input input-sm"
        style={{ minWidth }}
      >
        {options.map((opt) => (
          <option key={opt.value} value={opt.value}>
            {opt.label}
          </option>
        ))}
      </select>
    </div>
  );
}

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
  const isActive = hasActiveFilters(filter);

  return (
    <div className="card p-4">
      <div className="flex flex-wrap items-center gap-4">
        <FilterSelect
          id="provider-filter"
          label="Provider"
          value={filter.provider_id || ""}
          onChange={(v) => onFilterChange({ provider_id: v || undefined })}
          options={[
            { value: "", label: "All Providers" },
            ...providers.map((p) => ({ value: p.id, label: p.name })),
          ]}
          minWidth="150px"
        />
        <FilterSelect
          id="status-filter"
          label="Status"
          value={filter.success === undefined ? "" : String(filter.success)}
          onChange={(v) =>
            onFilterChange({ success: v === "" ? undefined : v === "true" })
          }
          options={[
            { value: "", label: "All Status" },
            { value: "true", label: "Success" },
            { value: "false", label: "Failed" },
          ]}
        />
        <FilterSelect
          id="request-type-filter"
          label="Request Type"
          value={filter.is_sse === undefined ? "" : String(filter.is_sse)}
          onChange={(v) =>
            onFilterChange({ is_sse: v === "" ? undefined : v === "true" })
          }
          options={[
            { value: "", label: "All Types" },
            { value: "true", label: "SSE Stream" },
            { value: "false", label: "Regular" },
          ]}
        />
        <FilterSelect
          id="api-type-filter"
          label="API Type"
          value={filter.api_type || ""}
          onChange={(v) => onFilterChange({ api_type: v || undefined })}
          options={[
            { value: "", label: "All Types" },
            ...API_TYPES_LIST.map((t) => ({ value: t, label: t })),
          ]}
        />
        <FilterSelect
          id="retries-filter"
          label="Retries"
          value={getRetriesFilterValue(filter)}
          onChange={(v) => handleRetriesFilterChange(v, onFilterChange)}
          options={[
            { value: "", label: "All" },
            { value: "has_retries", label: "Has Retries" },
            { value: "no_retries", label: "No Retries" },
            { value: "1+", label: "1+ Retries" },
          ]}
          minWidth="130px"
        />
        <FilterSelect
          id="time-range-filter"
          label="Time Range"
          value={getCurrentDatePreset(filter)}
          onChange={(v) => applyDatePreset(v, onFilterChange)}
          options={DATE_PRESETS.map((p) => ({
            value: p.value,
            label: p.label,
          }))}
          minWidth="140px"
        />
        {isActive && (
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
      {isActive && (
        <ActiveFiltersSummary
          filter={filter}
          providers={providers}
          onFilterChange={onFilterChange}
        />
      )}
    </div>
  );
}

interface ActiveFiltersSummaryProps {
  filter: LogFilter;
  providers: Provider[];
  onFilterChange: (filter: Partial<LogFilter>) => void;
}

function ActiveFiltersSummary({
  filter,
  providers,
  onFilterChange,
}: ActiveFiltersSummaryProps) {
  return (
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
        {filter.is_sse !== undefined && (
          <FilterBadge
            label={`Stream: ${filter.is_sse ? "SSE" : "Regular"}`}
            onRemove={() => onFilterChange({ is_sse: undefined })}
          />
        )}
        {filter.api_type && (
          <FilterBadge
            label={`Type: ${filter.api_type}`}
            onRemove={() => onFilterChange({ api_type: undefined })}
          />
        )}
        {getRetriesFilterLabel(filter) && (
          <FilterBadge
            label={`Retries: ${getRetriesFilterLabel(filter)}`}
            onRemove={() =>
              onFilterChange({
                has_retries: undefined,
                min_retry_count: undefined,
              })
            }
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
