import type {
  CommitSource,
  LogFilter,
  Provider,
  RecoveryAction,
  TerminalCause,
  WebSocketProbeOutcome,
} from "../api/types";
import { API_TYPES } from "../config/constants";
import { isLogFilterActive } from "./logs/filtering";

// API Types list derived from constants
const API_TYPES_LIST = Object.values(API_TYPES);
const FILTER_VALUE_ALL = "";
const FILTER_VALUE_TRUE = "true";
const FILTER_VALUE_FALSE = "false";

const TERMINAL_CAUSE_OPTIONS: Array<{
  value: TerminalCause | typeof FILTER_VALUE_ALL;
  label: string;
}> = [
  { value: FILTER_VALUE_ALL, label: "All Causes" },
  { value: "provider_unavailable", label: "Provider Unavailable" },
  {
    value: "provider_configuration_error",
    label: "Provider Configuration Error",
  },
  { value: "clean_close", label: "Clean Close" },
  { value: "client_disconnect", label: "Client Disconnect" },
  { value: "upstream_transport_error", label: "Upstream Transport Error" },
  { value: "upstream_semantic_error", label: "Upstream Semantic Error" },
  {
    value: "upstream_handshake_rejected",
    label: "Upstream Handshake Rejected",
  },
  { value: "client_upgrade_rejected", label: "Client Upgrade Rejected" },
  { value: "internal_error", label: "Internal Error" },
  { value: "unknown", label: "Unknown" },
];

const PROBE_OUTCOME_OPTIONS: Array<{
  value: WebSocketProbeOutcome | typeof FILTER_VALUE_ALL;
  label: string;
}> = [
  { value: FILTER_VALUE_ALL, label: "All Outcomes" },
  { value: "bypassed", label: "Bypassed" },
  { value: "demand_resolution_failed", label: "Demand Resolution Failed" },
  { value: "unsupported", label: "Unsupported" },
  { value: "observed_usable_model", label: "Observed Usable Model" },
  {
    value: "completed_without_usable_model",
    label: "Completed Without Usable Model",
  },
  { value: "transport_failed", label: "Transport Failed" },
  { value: "unknown", label: "Unknown" },
];

const RECOVERY_ACTION_OPTIONS: Array<{
  value: RecoveryAction | typeof FILTER_VALUE_ALL;
  label: string;
}> = [
  { value: FILTER_VALUE_ALL, label: "All Actions" },
  { value: "none", label: "None" },
  { value: "transparent_retry", label: "Transparent Retry" },
  { value: "reconnect_required", label: "Reconnect Required" },
];

const COMMIT_SOURCE_OPTIONS: Array<{
  value: CommitSource | typeof FILTER_VALUE_ALL;
  label: string;
}> = [
  { value: FILTER_VALUE_ALL, label: "All Sources" },
  { value: "semantic_event", label: "Semantic Event" },
  { value: "upstream_message", label: "First Upstream Message" },
  { value: "unknown", label: "Unknown" },
];

function getBooleanFilterValue(value: boolean | undefined): string {
  if (value === true) return FILTER_VALUE_TRUE;
  if (value === false) return FILTER_VALUE_FALSE;
  return FILTER_VALUE_ALL;
}

function parseBooleanFilterValue(value: string): boolean | undefined {
  if (value === FILTER_VALUE_ALL) {
    return undefined;
  }
  return value === FILTER_VALUE_TRUE;
}

function getTerminalCauseLabel(terminalCause: TerminalCause): string {
  const matchingOption = TERMINAL_CAUSE_OPTIONS.find(
    (option) => option.value === terminalCause,
  );
  return matchingOption?.label ?? terminalCause;
}

function getProbeOutcomeLabel(probeOutcome: WebSocketProbeOutcome): string {
  const matchingOption = PROBE_OUTCOME_OPTIONS.find(
    (option) => option.value === probeOutcome,
  );
  return matchingOption?.label ?? probeOutcome;
}

function getRecoveryActionLabel(recoveryAction: RecoveryAction): string {
  const matchingOption = RECOVERY_ACTION_OPTIONS.find(
    (option) => option.value === recoveryAction,
  );
  return matchingOption?.label ?? recoveryAction;
}

function getCommitSourceLabel(commitSource: CommitSource): string {
  const matchingOption = COMMIT_SOURCE_OPTIONS.find(
    (option) => option.value === commitSource,
  );
  return matchingOption?.label ?? commitSource;
}

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

// Derive the current request type filter value for the select control.
function getRequestTypeFilterValue(filter: LogFilter): string {
  if (filter.is_websocket === true) return "ws";
  if (filter.is_sse === true) return "sse";
  if (filter.is_sse === false && filter.is_websocket === false)
    return "regular";
  return "";
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
  const isActive = isLogFilterActive(filter);
  const requestTypeValue = getRequestTypeFilterValue(filter);

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
          value={getBooleanFilterValue(filter.success)}
          onChange={(value) =>
            onFilterChange({ success: parseBooleanFilterValue(value) })
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
          value={requestTypeValue}
          onChange={(v) => {
            switch (v) {
              case "sse":
                onFilterChange({ is_sse: true, is_websocket: undefined });
                break;
              case "ws":
                onFilterChange({ is_sse: undefined, is_websocket: true });
                break;
              case "regular":
                onFilterChange({ is_sse: false, is_websocket: false });
                break;
              default:
                onFilterChange({ is_sse: undefined, is_websocket: undefined });
            }
          }}
          options={[
            { value: "", label: "All Types" },
            { value: "sse", label: "SSE Stream" },
            { value: "ws", label: "WebSocket" },
            { value: "regular", label: "Regular" },
          ]}
        />
        <FilterSelect
          id="session-committed-filter"
          label="Commit State"
          value={getBooleanFilterValue(filter.session_committed)}
          onChange={(value) =>
            onFilterChange({
              session_committed: parseBooleanFilterValue(value),
            })
          }
          options={[
            { value: FILTER_VALUE_ALL, label: "All Sessions" },
            { value: FILTER_VALUE_TRUE, label: "Committed" },
            { value: FILTER_VALUE_FALSE, label: "Uncommitted" },
          ]}
          minWidth="140px"
        />
        <FilterSelect
          id="client-visible-filter"
          label="Client Visible"
          value={getBooleanFilterValue(filter.client_visible)}
          onChange={(value) =>
            onFilterChange({
              client_visible: parseBooleanFilterValue(value),
            })
          }
          options={[
            { value: FILTER_VALUE_ALL, label: "All Visibility" },
            { value: FILTER_VALUE_TRUE, label: "Visible" },
            { value: FILTER_VALUE_FALSE, label: "Not Visible" },
          ]}
          minWidth="140px"
        />
        <FilterSelect
          id="sticky-written-filter"
          label="Sticky Write"
          value={getBooleanFilterValue(filter.sticky_written)}
          onChange={(value) =>
            onFilterChange({
              sticky_written: parseBooleanFilterValue(value),
            })
          }
          options={[
            { value: FILTER_VALUE_ALL, label: "All Sticky" },
            { value: FILTER_VALUE_TRUE, label: "Written" },
            { value: FILTER_VALUE_FALSE, label: "Not Written" },
          ]}
          minWidth="140px"
        />
        <FilterSelect
          id="probe-outcome-filter"
          label="Probe Outcome"
          value={filter.probe_outcome || FILTER_VALUE_ALL}
          onChange={(value) =>
            onFilterChange({
              probe_outcome:
                value === FILTER_VALUE_ALL
                  ? undefined
                  : (value as WebSocketProbeOutcome),
            })
          }
          options={PROBE_OUTCOME_OPTIONS}
          minWidth="220px"
        />
        <FilterSelect
          id="terminal-cause-filter"
          label="Terminal Cause"
          value={filter.terminal_cause || FILTER_VALUE_ALL}
          onChange={(value) =>
            onFilterChange({
              terminal_cause:
                value === FILTER_VALUE_ALL
                  ? undefined
                  : (value as TerminalCause),
            })
          }
          options={TERMINAL_CAUSE_OPTIONS}
          minWidth="180px"
        />
        <FilterSelect
          id="commit-source-filter"
          label="Commit Source"
          value={filter.commit_source || FILTER_VALUE_ALL}
          onChange={(value) =>
            onFilterChange({
              commit_source:
                value === FILTER_VALUE_ALL
                  ? undefined
                  : (value as CommitSource),
            })
          }
          options={COMMIT_SOURCE_OPTIONS}
          minWidth="220px"
        />
        <FilterSelect
          id="recovery-action-filter"
          label="Recovery Action"
          value={filter.recovery_action || FILTER_VALUE_ALL}
          onChange={(value) =>
            onFilterChange({
              recovery_action:
                value === FILTER_VALUE_ALL
                  ? undefined
                  : (value as RecoveryAction),
            })
          }
          options={RECOVERY_ACTION_OPTIONS}
          minWidth="190px"
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
        {filter.session_committed !== undefined && (
          <FilterBadge
            label={`Commit: ${filter.session_committed ? "Committed" : "Uncommitted"}`}
            onRemove={() => onFilterChange({ session_committed: undefined })}
          />
        )}
        {filter.client_visible !== undefined && (
          <FilterBadge
            label={`Client Visible: ${filter.client_visible ? "Visible" : "Not Visible"}`}
            onRemove={() => onFilterChange({ client_visible: undefined })}
          />
        )}
        {filter.sticky_written !== undefined && (
          <FilterBadge
            label={`Sticky Write: ${filter.sticky_written ? "Written" : "Not Written"}`}
            onRemove={() => onFilterChange({ sticky_written: undefined })}
          />
        )}
        {filter.probe_outcome && (
          <FilterBadge
            label={`Probe Outcome: ${getProbeOutcomeLabel(filter.probe_outcome)}`}
            onRemove={() => onFilterChange({ probe_outcome: undefined })}
          />
        )}
        {filter.terminal_cause && (
          <FilterBadge
            label={`Terminal Cause: ${getTerminalCauseLabel(filter.terminal_cause)}`}
            onRemove={() => onFilterChange({ terminal_cause: undefined })}
          />
        )}
        {filter.commit_source && (
          <FilterBadge
            label={`Commit Source: ${getCommitSourceLabel(filter.commit_source)}`}
            onRemove={() => onFilterChange({ commit_source: undefined })}
          />
        )}
        {filter.recovery_action && (
          <FilterBadge
            label={`Recovery Action: ${getRecoveryActionLabel(filter.recovery_action)}`}
            onRemove={() => onFilterChange({ recovery_action: undefined })}
          />
        )}
        {/* is_sse and is_websocket badges are coupled: "regular" sets both to false,
            so removing one badge must clear both to avoid inconsistent filter state. */}
        {filter.is_sse !== undefined && (
          <FilterBadge
            label={`Type: ${filter.is_sse ? "SSE" : "Regular"}`}
            onRemove={() =>
              onFilterChange({ is_sse: undefined, is_websocket: undefined })
            }
          />
        )}
        {filter.is_websocket !== undefined && filter.is_sse === undefined && (
          <FilterBadge
            label={`Type: ${filter.is_websocket ? "WebSocket" : "Regular"}`}
            onRemove={() =>
              onFilterChange({ is_sse: undefined, is_websocket: undefined })
            }
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
