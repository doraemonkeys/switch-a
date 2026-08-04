export type DebugCaptureState = "stopped" | "active";
export type CaptureProtocol = "http" | "websocket";
export type CaptureLifecycleState = "active" | "completed";
export type CaptureSourceCompletion = "pending" | "complete" | "partial";
export type CaptureCompletion = "complete" | "overflowed";
export type CaptureSnapshotState = "final" | "active_partial";
export type CaptureSelectionMode = "initial" | "replacement" | "failover";
export type CaptureSelectionSource =
  "strategy" | "sticky_continuity" | "active_continuity";
export type CaptureCredentialPhase = "initial" | "refreshed";
export type CaptureTerminationReason =
  | "eof"
  | "status_failover_drain"
  | "credential_refresh_drain"
  | "client_disconnect"
  | "timeout"
  | "canceled"
  | "preparation_error"
  | "gateway_finished"
  | "transport_error"
  | "read_error"
  | "write_error"
  | "websocket_close"
  | "websocket_relay_error"
  | "capture_fault";

export type DebugCaptureFailureSite =
  | "unknown"
  | "gateway"
  | "preparation"
  | "transport"
  | "response_status"
  | "response_drain"
  | "response_read"
  | "response_write"
  | "websocket_handshake"
  | "websocket_upgrade"
  | "websocket_replay"
  | "websocket_relay"
  | "websocket_message"
  | "websocket_close";

export type DebugCaptureFailurePeer =
  "unknown" | "gateway" | "client" | "upstream" | "provider";

export type DebugCaptureFailureClass =
  | "unknown"
  | "timeout"
  | "canceled"
  | "configuration"
  | "transport"
  | "http_status"
  | "read"
  | "write"
  | "protocol"
  | "websocket_close"
  | "upstream_semantic";

export type DebugCaptureFailureCode =
  | "unknown"
  | "missing_base_url"
  | "missing_api_key"
  | "missing_credentials"
  | "request_build"
  | "credential_apply"
  | "gateway_context"
  | "dns"
  | "connection"
  | "round_trip"
  | "unexpected_status"
  | "failure_body_read"
  | "drain_read"
  | "upstream_read"
  | "client_write"
  | "client_accept"
  | "websocket_dial"
  | "handshake_rejected"
  | "websocket_upgrade"
  | "replay_write"
  | "relay_read"
  | "relay_write"
  | "message_read"
  | "message_write"
  | "protocol_violation"
  | "websocket_close"
  | "provider_semantic";

export interface DebugCaptureFailureFact {
  site: DebugCaptureFailureSite;
  peer: DebugCaptureFailurePeer;
  class: DebugCaptureFailureClass;
  code: DebugCaptureFailureCode;
  http_status_code?: number;
  websocket_close_code?: number;
  system_error_code?: number;
  provider_error_type?: string;
  provider_error_code?: string;
  message?: string;
}

export interface DebugCaptureFailureObservation {
  primary: DebugCaptureFailureFact;
  secondary: DebugCaptureFailureFact;
  has_secondary: boolean;
  truncated: boolean;
}

interface DebugCaptureFailureCarrier {
  has_failure: boolean;
  failure: DebugCaptureFailureObservation;
}

export interface DebugCaptureProviderIdentity {
  id: string;
  name: string;
}

export interface DebugCaptureProviderSnapshot extends DebugCaptureProviderIdentity {
  api_type: string;
  target_url: string;
}

export interface DebugCaptureSessionInfo {
  session_id: string;
  generation: number;
  started_at: string;
  providers: DebugCaptureProviderIdentity[];
  provider_ids: string[];
  completed_records_per_provider: number;
  retained_bytes_limit: number;
}

export interface DebugCaptureSessionStatus extends DebugCaptureSessionInfo {
  retained_bytes: number;
  active_record_count: number;
  completed_record_count: number;
  gateway_trace_count: number;
  evicted_record_count: number;
  overflowed_record_count: number;
  history_truncated_trace_count: number;
  dropped_trace_count: number;
  dropped_exchange_count: number;
  dropped_transition_count: number;
}

export interface DebugCaptureProcessMemoryStatus {
  ceiling_bytes: number;
  charged_bytes: number;
  retained_bytes: number;
  pinned_bytes: number;
  releasing_bytes: number;
  temporary_bytes: number;
}

export interface DebugCaptureStatus {
  state: DebugCaptureState;
  process_memory: DebugCaptureProcessMemoryStatus;
  pending_export_count: number;
  active_download_count: number;
  session: DebugCaptureSessionStatus | null;
}

export interface StartDebugCaptureRequest {
  provider_ids: string[];
  completed_records_per_provider?: number;
  retained_bytes_limit?: number;
  acknowledge_raw_payload_risk: true;
}

export interface DebugCaptureRecordSummary extends DebugCaptureFailureCarrier {
  session_id: string;
  record_id: string;
  gateway_trace_id: string;
  gateway_request_id: string;
  exchange_index: number;
  record_sequence: number;
  provider: DebugCaptureProviderSnapshot;
  protocol: CaptureProtocol;
  selection_mode: CaptureSelectionMode;
  selection_source: CaptureSelectionSource;
  provider_attempt_index: number;
  credential_phase: CaptureCredentialPhase;
  lifecycle_state: CaptureLifecycleState;
  source_completion?: CaptureSourceCompletion;
  capture_completion: CaptureCompletion;
  started_at: string;
  completed_at?: string;
  termination_reason?: CaptureTerminationReason;
  upstream_observed_bytes: number;
  application_write_confirmed_bytes: number;
}

interface DebugCaptureTraceEntryBase extends DebugCaptureFailureCarrier {
  entry_id: string;
  sequence: number;
  provider: DebugCaptureProviderSnapshot;
  provider_attempt_index: number;
  selection_mode: CaptureSelectionMode;
  selection_source: CaptureSelectionSource;
  credential_phase: CaptureCredentialPhase;
  termination_reason?: CaptureTerminationReason;
  metadata_truncated: boolean;
}

export interface DebugCaptureRecordTraceEntry extends DebugCaptureTraceEntryBase {
  kind: "record";
  record_id: string;
}

export interface DebugCaptureTransitionTraceEntry extends DebugCaptureTraceEntryBase {
  kind: "transition";
  record_id?: never;
}

export type DebugCaptureTraceEntry =
  DebugCaptureRecordTraceEntry | DebugCaptureTransitionTraceEntry;

export interface DebugCaptureTraceSummary {
  gateway_trace_id: string;
  gateway_request_id: string;
  entries: DebugCaptureTraceEntry[];
  history_truncated_before: boolean;
  history_truncated_after: boolean;
}

export interface DebugCaptureEvictionGap {
  detected: boolean;
  record_count: number;
}

export interface DebugCaptureRecordsPage {
  session_id: string;
  snapshot_watermark: string;
  records: DebugCaptureRecordSummary[];
  gateway_traces: DebugCaptureTraceSummary[];
  next_cursor?: string;
  eviction_gap: DebugCaptureEvictionGap;
}

export interface DebugCaptureRecordsQuery {
  limit?: number;
  cursor?: string;
  snapshot_watermark?: string;
}

export interface DebugCaptureBlobPreview {
  data_base64: string;
  preview_bytes: number;
  captured_bytes: number;
  truncated: boolean;
  checksum_sha256: string;
}

export type DebugCaptureHeaders = Record<string, string[]>;

export interface DebugCaptureRequestSnapshot {
  method: string;
  url: string;
  host: string;
  headers: DebugCaptureHeaders;
  content_length: number;
  trailers?: DebugCaptureHeaders;
}

export interface DebugCaptureHTTPResponseSnapshot {
  status_code: number;
  protocol: string;
  headers: DebugCaptureHeaders;
  content_length: number;
  declared_trailer_keys?: string[];
  trailers?: DebugCaptureHeaders;
}

export interface DebugCaptureHTTPExchangeDetail {
  request: DebugCaptureRequestSnapshot;
  request_body: DebugCaptureBlobPreview;
  response?: DebugCaptureHTTPResponseSnapshot;
  response_body: DebugCaptureBlobPreview;
}

export type DebugCaptureWebSocketDirection =
  "client_to_upstream" | "upstream_to_client";
export type DebugCaptureWebSocketMessageType = "text" | "binary";
export type DebugCaptureWebSocketSource = "live" | "replay";
export type DebugCaptureWebSocketDisposition =
  "forwarded" | "suppressed" | "write_failed";

export interface DebugCaptureMessageSnapshot extends DebugCaptureFailureCarrier {
  message_id: string;
  sequence: number;
  relative_millis: number;
  direction: DebugCaptureWebSocketDirection;
  message_type: DebugCaptureWebSocketMessageType;
  source: DebugCaptureWebSocketSource;
  source_message_id?: string;
  disposition?: DebugCaptureWebSocketDisposition;
  client_visible: boolean;
  payload: DebugCaptureBlobPreview;
}

export interface DebugCaptureWebSocketHandshakeSnapshot {
  status_code: number;
  protocol: string;
  headers: DebugCaptureHeaders;
}

export interface DebugCaptureWebSocketCloseSnapshot {
  direction: DebugCaptureWebSocketDirection;
  code: number;
  reason?: string;
  reason_truncated: boolean;
  clean: boolean;
}

export interface DebugCaptureWebSocketExchangeDetail {
  request: DebugCaptureRequestSnapshot;
  request_body: DebugCaptureBlobPreview;
  handshake?: DebugCaptureWebSocketHandshakeSnapshot;
  handshake_body: DebugCaptureBlobPreview;
  messages: DebugCaptureMessageSnapshot[];
  events_truncated: boolean;
  close?: DebugCaptureWebSocketCloseSnapshot;
}

export interface DebugCaptureRecordDetail {
  summary: DebugCaptureRecordSummary;
  snapshot_state: CaptureSnapshotState;
  gateway_trace: DebugCaptureTraceSummary | null;
  http?: DebugCaptureHTTPExchangeDetail;
  websocket?: DebugCaptureWebSocketExchangeDetail;
}

export type DebugCaptureExportScope = "all" | "records";

export interface CreateDebugCaptureExportRequest {
  scope: DebugCaptureExportScope;
  record_ids?: string[];
}

export interface DebugCaptureDownloadGrant {
  export_id: string;
  session_id: string;
  record_count: number;
  expires_at: string;
  download_url: string;
}
