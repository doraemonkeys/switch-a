import type {
  ClientAction,
  CommitSource,
  CompletionState,
  LogFilter,
  Provider,
  SemanticsVersion,
  ServiceOutcome,
  TerminationActor,
  TerminationReason,
} from "../api/types";
import type { APICatalog } from "../api/api-catalog";
import { useAPICatalog } from "../api/useApi";
import { isLogFilterActive } from "./logs/filtering";

const FILTER_VALUE_ALL = "";
const FILTER_VALUE_TRUE = "true";
const FILTER_VALUE_FALSE = "false";

const SEMANTICS_VERSION_OPTIONS: Array<{
  value: SemanticsVersion | typeof FILTER_VALUE_ALL;
  label: string;
}> = [
  { value: FILTER_VALUE_ALL, label: "All Rows" },
  { value: "normalized_v1", label: "Normalized" },
  { value: "legacy_pre_assessment", label: "Legacy" },
];

const COMPLETION_STATE_OPTIONS: Array<{
  value: CompletionState | typeof FILTER_VALUE_ALL;
  label: string;
}> = [
  { value: FILTER_VALUE_ALL, label: "All States" },
  { value: "completed", label: "Completed" },
  { value: "incomplete", label: "Incomplete" },
  { value: "unknown", label: "Unknown" },
];

const SERVICE_OUTCOME_OPTIONS: Array<{
  value: ServiceOutcome | typeof FILTER_VALUE_ALL;
  label: string;
}> = [
  { value: FILTER_VALUE_ALL, label: "All Outcomes" },
  { value: "completed", label: "Completed" },
  { value: "interrupted", label: "Interrupted" },
  { value: "never_started", label: "Never Started" },
  { value: "abandoned_by_client", label: "Abandoned by Client" },
  { value: "unknown", label: "Unknown" },
];

const CLIENT_ACTION_OPTIONS: Array<{
  value: ClientAction | typeof FILTER_VALUE_ALL;
  label: string;
}> = [
  { value: FILTER_VALUE_ALL, label: "All Actions" },
  { value: "none", label: "None" },
  { value: "transparent_retry", label: "Transparent Retry" },
  { value: "reconnect_required", label: "Reconnect Required" },
];

const TERMINATION_ACTOR_OPTIONS: Array<{
  value: TerminationActor | typeof FILTER_VALUE_ALL;
  label: string;
}> = [
  { value: FILTER_VALUE_ALL, label: "All Actors" },
  { value: "client", label: "Client" },
  { value: "gateway", label: "Gateway" },
  { value: "upstream", label: "Upstream" },
  { value: "internal", label: "Internal" },
  { value: "unknown", label: "Unknown" },
];

const TERMINATION_REASON_OPTIONS: Array<{
  value: TerminationReason | typeof FILTER_VALUE_ALL;
  label: string;
}> = [
  { value: FILTER_VALUE_ALL, label: "All Reasons" },
  { value: "provider_unavailable", label: "Provider Unavailable" },
  {
    value: "provider_configuration_error",
    label: "Provider Configuration Error",
  },
  { value: "usage_limit_reached", label: "Usage Limit Reached" },
  {
    value: "websocket_connection_limit_reached",
    label: "WebSocket Connection Limit Reached",
  },
  { value: "client_request_error", label: "Client Request Error" },
  { value: "client_disconnect", label: "Client Disconnect" },
  { value: "timeout", label: "Timeout" },
  { value: "transport_error", label: "Transport Error" },
  { value: "upstream_semantic_error", label: "Upstream Semantic Error" },
  {
    value: "upstream_handshake_rejected",
    label: "Upstream Handshake Rejected",
  },
  { value: "client_upgrade_rejected", label: "Client Upgrade Rejected" },
  { value: "internal_error", label: "Internal Error" },
  { value: "clean_close", label: "Clean Close" },
  { value: "unknown", label: "Unknown" },
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

function getOptionLabel(
  options: Array<{ value: string; label: string }>,
  value: string,
): string {
  return options.find((option) => option.value === value)?.label ?? value;
}

function getSemanticsVersionLabel(semanticsVersion: SemanticsVersion): string {
  return getOptionLabel(SEMANTICS_VERSION_OPTIONS, semanticsVersion);
}

function getCompletionStateLabel(completionState: CompletionState): string {
  return getOptionLabel(COMPLETION_STATE_OPTIONS, completionState);
}

function getServiceOutcomeLabel(serviceOutcome: ServiceOutcome): string {
  return getOptionLabel(SERVICE_OUTCOME_OPTIONS, serviceOutcome);
}

function getClientActionLabel(clientAction: ClientAction): string {
  return getOptionLabel(CLIENT_ACTION_OPTIONS, clientAction);
}

function getTerminationActorLabel(terminationActor: TerminationActor): string {
  return getOptionLabel(TERMINATION_ACTOR_OPTIONS, terminationActor);
}

function getTerminationReasonLabel(
  terminationReason: TerminationReason,
): string {
  return getOptionLabel(TERMINATION_REASON_OPTIONS, terminationReason);
}

function getCommitSourceLabel(commitSource: CommitSource): string {
  return getOptionLabel(COMMIT_SOURCE_OPTIONS, commitSource);
}

function getRetriesFilterValue(filter: LogFilter): string {
  if (filter.has_retries === true) return "has_retries";
  if (filter.has_retries === false) return "no_retries";
  if (filter.min_retry_count !== undefined && filter.min_retry_count >= 1)
    return "1+";
  return "";
}

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

function getRetriesFilterLabel(filter: LogFilter): string | null {
  if (filter.has_retries === true) return "Has Retries";
  if (filter.has_retries === false) return "No Retries";
  if (filter.min_retry_count !== undefined && filter.min_retry_count >= 1)
    return "1+ Retries";
  return null;
}

function getAPITypeSuggestions(
  providers: Provider[],
  selectedAPIType: string | undefined,
  catalog: APICatalog | null,
): string[] {
  const providerAPITypeValues = providers.flatMap((provider) =>
    provider.api_types.map((apiType) => apiType.api_type.trim()),
  );
  const knownAPITypeValues = [
    ...(catalog?.api_types.map((entry) => entry.api_type) ?? []),
    ...providerAPITypeValues,
  ];

  if (selectedAPIType?.trim()) {
    knownAPITypeValues.push(selectedAPIType.trim());
  }

  return Array.from(
    new Set(knownAPITypeValues.filter((apiType) => apiType !== "")),
  ).sort((left, right) => left.localeCompare(right));
}

const DATE_PRESETS = [
  { label: "All Time", value: "" },
  { label: "Last 1 Hour", value: "1h" },
  { label: "Last 24 Hours", value: "24h" },
  { label: "Last 7 Days", value: "7d" },
  { label: "Last 30 Days", value: "30d" },
] as const;

const DATE_PRESET_TOLERANCE = {
  ONE_HOUR: 1.1,
  ONE_DAY: 24.1,
  SEVEN_DAYS: 24 * 7.1,
  THIRTY_DAYS: 24 * 30.1,
} as const;

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

interface FilterInputProps {
  id: string;
  label: string;
  value: string;
  type?: "number" | "text";
  placeholder?: string;
  minWidth?: string;
  listId?: string;
  listOptions?: string[];
  onChange: (value: string) => void;
}

function FilterInput({
  id,
  label,
  value,
  type = "text",
  placeholder,
  minWidth = "120px",
  listId,
  listOptions,
  onChange,
}: FilterInputProps) {
  return (
    <div className="flex flex-col gap-1">
      <label htmlFor={id} className="text-xs text-text-muted font-medium">
        {label}
      </label>
      <input
        id={id}
        type={type}
        value={value}
        placeholder={placeholder}
        list={listId}
        onChange={(e) => onChange(e.target.value)}
        className="input input-sm"
        style={{ minWidth }}
      />
      {listId && listOptions && listOptions.length > 0 && (
        <datalist id={listId}>
          {listOptions.map((option) => (
            <option key={option} value={option} />
          ))}
        </datalist>
      )}
    </div>
  );
}

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

interface SemanticFilterControlsProps {
  filter: LogFilter;
  onFilterChange: (filter: Partial<LogFilter>) => void;
  providers: Provider[];
}

function SemanticFilterControls({
  filter,
  onFilterChange,
  providers,
}: SemanticFilterControlsProps) {
  const providerOptions = [
    { value: FILTER_VALUE_ALL, label: "All Providers" },
    ...providers.map((provider) => ({
      value: provider.id,
      label: provider.name,
    })),
  ];

  return (
    <>
      <FilterSelect
        id="provider-filter"
        label="Provider"
        value={filter.provider_id || FILTER_VALUE_ALL}
        onChange={(value) =>
          onFilterChange({ provider_id: value || undefined })
        }
        options={providerOptions}
        minWidth="150px"
      />
      <FilterSelect
        id="semantics-version-filter"
        label="Semantics"
        value={filter.semantics_version || FILTER_VALUE_ALL}
        onChange={(value) =>
          onFilterChange({
            semantics_version:
              value === FILTER_VALUE_ALL
                ? undefined
                : (value as SemanticsVersion),
          })
        }
        options={SEMANTICS_VERSION_OPTIONS}
        minWidth="150px"
      />
      <FilterSelect
        id="service-outcome-filter"
        label="Service Outcome"
        value={filter.service_outcome || FILTER_VALUE_ALL}
        onChange={(value) =>
          onFilterChange({
            service_outcome:
              value === FILTER_VALUE_ALL
                ? undefined
                : (value as ServiceOutcome),
          })
        }
        options={SERVICE_OUTCOME_OPTIONS}
        minWidth="180px"
      />
      <FilterSelect
        id="client-action-filter"
        label="Client Action"
        value={filter.client_action || FILTER_VALUE_ALL}
        onChange={(value) =>
          onFilterChange({
            client_action:
              value === FILTER_VALUE_ALL ? undefined : (value as ClientAction),
          })
        }
        options={CLIENT_ACTION_OPTIONS}
        minWidth="180px"
      />
      <FilterSelect
        id="termination-reason-filter"
        label="Termination Reason"
        value={filter.termination_reason || FILTER_VALUE_ALL}
        onChange={(value) =>
          onFilterChange({
            termination_reason:
              value === FILTER_VALUE_ALL
                ? undefined
                : (value as TerminationReason),
          })
        }
        options={TERMINATION_REASON_OPTIONS}
        minWidth="220px"
      />
      <FilterSelect
        id="termination-actor-filter"
        label="Termination Actor"
        value={filter.termination_actor || FILTER_VALUE_ALL}
        onChange={(value) =>
          onFilterChange({
            termination_actor:
              value === FILTER_VALUE_ALL
                ? undefined
                : (value as TerminationActor),
          })
        }
        options={TERMINATION_ACTOR_OPTIONS}
        minWidth="170px"
      />
      <FilterSelect
        id="completion-state-filter"
        label="Completion State"
        value={filter.completion_state || FILTER_VALUE_ALL}
        onChange={(value) =>
          onFilterChange({
            completion_state:
              value === FILTER_VALUE_ALL
                ? undefined
                : (value as CompletionState),
          })
        }
        options={COMPLETION_STATE_OPTIONS}
        minWidth="170px"
      />
      <FilterInput
        id="transport-code-filter"
        label="Transport Code"
        type="number"
        value={
          filter.client_transport_status_code !== undefined
            ? String(filter.client_transport_status_code)
            : FILTER_VALUE_ALL
        }
        placeholder="101"
        minWidth="130px"
        onChange={(value) =>
          onFilterChange({
            client_transport_status_code:
              value === FILTER_VALUE_ALL ? undefined : Number(value),
          })
        }
      />
    </>
  );
}

interface OperationalFilterControlsProps {
  filter: LogFilter;
  onFilterChange: (filter: Partial<LogFilter>) => void;
  onClear: () => void;
  isActive: boolean;
  requestTypeValue: string;
  apiTypeSuggestions: string[];
}

function OperationalFilterControls({
  filter,
  onFilterChange,
  onClear,
  isActive,
  requestTypeValue,
  apiTypeSuggestions,
}: OperationalFilterControlsProps) {
  return (
    <>
      <FilterSelect
        id="request-type-filter"
        label="Request Type"
        value={requestTypeValue}
        onChange={(value) => {
          switch (value) {
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
          { value: FILTER_VALUE_ALL, label: "All Types" },
          { value: "sse", label: "SSE Stream" },
          { value: "ws", label: "WebSocket" },
          { value: "regular", label: "Regular" },
        ]}
      />
      <FilterSelect
        id="session-committed-filter"
        label="Session Committed"
        value={getBooleanFilterValue(filter.session_committed)}
        onChange={(value) =>
          onFilterChange({
            session_committed: parseBooleanFilterValue(value),
          })
        }
        options={[
          { value: FILTER_VALUE_ALL, label: "All Sessions" },
          { value: FILTER_VALUE_TRUE, label: "Committed" },
          { value: FILTER_VALUE_FALSE, label: "Not Committed" },
        ]}
        minWidth="160px"
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
        id="commit-source-filter"
        label="Commit Source"
        value={filter.commit_source || FILTER_VALUE_ALL}
        onChange={(value) =>
          onFilterChange({
            commit_source:
              value === FILTER_VALUE_ALL ? undefined : (value as CommitSource),
          })
        }
        options={COMMIT_SOURCE_OPTIONS}
        minWidth="200px"
      />
      <FilterInput
        id="api-type-filter"
        label="API Type"
        value={filter.api_type || FILTER_VALUE_ALL}
        placeholder="claude or custom:tool"
        listId="api-type-filter-options"
        listOptions={apiTypeSuggestions}
        onChange={(value) =>
          onFilterChange({ api_type: value.trim() || undefined })
        }
      />
      <FilterSelect
        id="retries-filter"
        label="Retries"
        value={getRetriesFilterValue(filter)}
        onChange={(value) => handleRetriesFilterChange(value, onFilterChange)}
        options={[
          { value: FILTER_VALUE_ALL, label: "All" },
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
        onChange={(value) => applyDatePreset(value, onFilterChange)}
        options={DATE_PRESETS.map((preset) => ({
          value: preset.value,
          label: preset.label,
        }))}
        minWidth="140px"
      />
      {isActive && (
        <div className="flex flex-col gap-1">
          <label className="text-xs text-transparent font-medium">Clear</label>
          <button
            onClick={onClear}
            className="btn btn-secondary btn-sm flex items-center gap-1"
          >
            <span>✕</span>
            Clear Filters
          </button>
        </div>
      )}
    </>
  );
}

export function LogFilters({
  filter,
  onFilterChange,
  providers,
  onClear,
}: LogFiltersProps) {
  const { catalog } = useAPICatalog();
  const isActive = isLogFilterActive(filter);
  const requestTypeValue = getRequestTypeFilterValue(filter);
  const apiTypeSuggestions = getAPITypeSuggestions(
    providers,
    filter.api_type,
    catalog,
  );

  return (
    <div className="card p-4">
      <div className="flex flex-wrap items-center gap-4">
        <SemanticFilterControls
          filter={filter}
          onFilterChange={onFilterChange}
          providers={providers}
        />
        <OperationalFilterControls
          filter={filter}
          onFilterChange={onFilterChange}
          onClear={onClear}
          isActive={isActive}
          requestTypeValue={requestTypeValue}
          apiTypeSuggestions={apiTypeSuggestions}
        />
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
            label={`Provider: ${providers.find((provider) => provider.id === filter.provider_id)?.name || filter.provider_id}`}
            onRemove={() => onFilterChange({ provider_id: undefined })}
          />
        )}
        {filter.semantics_version && (
          <FilterBadge
            label={`Semantics: ${getSemanticsVersionLabel(filter.semantics_version)}`}
            onRemove={() => onFilterChange({ semantics_version: undefined })}
          />
        )}
        {filter.service_outcome && (
          <FilterBadge
            label={`Service Outcome: ${getServiceOutcomeLabel(filter.service_outcome)}`}
            onRemove={() => onFilterChange({ service_outcome: undefined })}
          />
        )}
        {filter.client_action && (
          <FilterBadge
            label={`Client Action: ${getClientActionLabel(filter.client_action)}`}
            onRemove={() => onFilterChange({ client_action: undefined })}
          />
        )}
        {filter.termination_reason && (
          <FilterBadge
            label={`Termination Reason: ${getTerminationReasonLabel(filter.termination_reason)}`}
            onRemove={() => onFilterChange({ termination_reason: undefined })}
          />
        )}
        {filter.termination_actor && (
          <FilterBadge
            label={`Termination Actor: ${getTerminationActorLabel(filter.termination_actor)}`}
            onRemove={() => onFilterChange({ termination_actor: undefined })}
          />
        )}
        {filter.completion_state && (
          <FilterBadge
            label={`Completion: ${getCompletionStateLabel(filter.completion_state)}`}
            onRemove={() => onFilterChange({ completion_state: undefined })}
          />
        )}
        {filter.client_transport_status_code !== undefined && (
          <FilterBadge
            label={`Transport Code: ${filter.client_transport_status_code}`}
            onRemove={() =>
              onFilterChange({ client_transport_status_code: undefined })
            }
          />
        )}
        {filter.session_committed !== undefined && (
          <FilterBadge
            label={`Committed: ${filter.session_committed ? "Yes" : "No"}`}
            onRemove={() => onFilterChange({ session_committed: undefined })}
          />
        )}
        {filter.client_visible !== undefined && (
          <FilterBadge
            label={`Client Visible: ${filter.client_visible ? "Visible" : "Not Visible"}`}
            onRemove={() => onFilterChange({ client_visible: undefined })}
          />
        )}
        {filter.commit_source && (
          <FilterBadge
            label={`Commit Source: ${getCommitSourceLabel(filter.commit_source)}`}
            onRemove={() => onFilterChange({ commit_source: undefined })}
          />
        )}
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
            label={`API Type: ${filter.api_type}`}
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
