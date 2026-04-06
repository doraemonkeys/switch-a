import type { LogFilter } from "../../api/types";

export function isLogFilterActive(filter: LogFilter): boolean {
  return !!(
    filter.provider_id ||
    filter.api_type ||
    filter.semantics_version ||
    filter.completion_state ||
    filter.service_outcome ||
    filter.client_action ||
    filter.termination_actor ||
    filter.termination_reason ||
    filter.client_transport_status_code !== undefined ||
    filter.is_sse !== undefined ||
    filter.is_websocket !== undefined ||
    filter.start_time ||
    filter.end_time ||
    filter.has_retries !== undefined ||
    filter.min_retry_count !== undefined ||
    filter.session_committed !== undefined ||
    filter.client_visible !== undefined ||
    filter.commit_source
  );
}

export function createClearedLogFilterPatch(): Partial<LogFilter> {
  return {
    provider_id: undefined,
    api_type: undefined,
    semantics_version: undefined,
    completion_state: undefined,
    service_outcome: undefined,
    client_action: undefined,
    termination_actor: undefined,
    termination_reason: undefined,
    client_transport_status_code: undefined,
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
    commit_source: undefined,
  };
}
