import type { LogFilter } from "../../api/types";

export function isLogFilterActive(filter: LogFilter): boolean {
  return !!(
    filter.provider_id ||
    filter.api_type ||
    filter.success !== undefined ||
    filter.is_sse !== undefined ||
    filter.is_websocket !== undefined ||
    filter.start_time ||
    filter.end_time ||
    filter.has_retries !== undefined ||
    filter.min_retry_count !== undefined ||
    filter.session_committed !== undefined ||
    filter.client_visible !== undefined ||
    filter.sticky_written !== undefined ||
    filter.probe_outcome ||
    filter.terminal_cause ||
    filter.commit_source ||
    filter.recovery_action
  );
}

export function createClearedLogFilterPatch(): Partial<LogFilter> {
  return {
    provider_id: undefined,
    api_type: undefined,
    success: undefined,
    is_sse: undefined,
    is_websocket: undefined,
    start_time: undefined,
    end_time: undefined,
    min_latency: undefined,
    user_id: undefined,
    has_retries: undefined,
    min_retry_count: undefined,
    session_committed: undefined,
    client_visible: undefined,
    sticky_written: undefined,
    probe_outcome: undefined,
    terminal_cause: undefined,
    commit_source: undefined,
    recovery_action: undefined,
  };
}
