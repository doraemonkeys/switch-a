import type { LogFilter } from "../../api/types";

const TABLE_SCOPING_FILTER_KEYS = [
  "provider_id",
  "api_type",
  "semantics_version",
  "completion_state",
  "service_outcome",
  "client_action",
  "termination_actor",
  "termination_reason",
  "client_transport_status_code",
  "is_sse",
  "is_websocket",
  "user_id",
  "start_time",
  "end_time",
  "min_latency",
  "min_retry_count",
  "has_retries",
  "session_committed",
  "client_visible",
  "commit_source",
] as const satisfies readonly (keyof LogFilter)[];

function hasFilterValue(value: LogFilter[keyof LogFilter]): boolean {
  return value !== undefined && value !== "";
}

export function isLogFilterActive(filter: LogFilter): boolean {
  return TABLE_SCOPING_FILTER_KEYS.some((key) => hasFilterValue(filter[key]));
}

export function createClearedLogFilterPatch(): Partial<LogFilter> {
  return Object.fromEntries(
    TABLE_SCOPING_FILTER_KEYS.map((key) => [key, undefined]),
  ) as Partial<LogFilter>;
}
