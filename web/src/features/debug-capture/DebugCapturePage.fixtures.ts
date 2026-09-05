import type {
  DebugCaptureFailureObservation,
  DebugCaptureRecordDetail,
  DebugCaptureRecordsPage,
  DebugCaptureSessionInfo,
  DebugCaptureStatus,
  Provider,
} from "@/api";

export const MIB = 1_024 * 1_024;
export const EXPORT_ID = "ce_AAAAAAAAAAAAAAAAAAAAAAAA";
export const EXPORT_DOWNLOAD_PATH =
  "/admin/api/debug-capture/exports/ce_AAAAAAAAAAAAAAAAAAAAAAAA/download";
export const DOWNLOAD_TOKEN = "A".repeat(43);
export const EXPORT_DOWNLOAD_URL = `${EXPORT_DOWNLOAD_PATH}?download_token=${DOWNLOAD_TOKEN}`;

export const emptyFailureObservation: DebugCaptureFailureObservation = {
  primary: {
    site: "unknown",
    peer: "unknown",
    class: "unknown",
    code: "unknown",
  },
  secondary: {
    site: "unknown",
    peer: "unknown",
    class: "unknown",
    code: "unknown",
  },
  has_secondary: false,
  truncated: false,
};

export const sessionInfo: DebugCaptureSessionInfo = {
  session_id: "session-a",
  generation: 1,
  started_at: "2026-08-01T00:00:00Z",
  providers: [{ id: "provider-a", name: "Provider A" }],
  provider_ids: ["provider-a"],
  completed_records_per_provider: 10,
  retained_bytes_limit: 256 * MIB,
};

export const stoppedStatus: DebugCaptureStatus = {
  state: "stopped",
  process_memory: {
    ceiling_bytes: 512 * MIB,
    charged_bytes: 0,
    retained_bytes: 0,
    pinned_bytes: 0,
    releasing_bytes: 0,
    temporary_bytes: 0,
  },
  pending_export_count: 0,
  active_download_count: 0,
  session: null,
};

export const activeStatus: DebugCaptureStatus = {
  ...stoppedStatus,
  state: "active",
  process_memory: {
    ...stoppedStatus.process_memory,
    charged_bytes: 2 * MIB,
    retained_bytes: MIB,
  },
  session: {
    ...sessionInfo,
    retained_bytes: MIB,
    active_record_count: 1,
    completed_record_count: 1,
    gateway_trace_count: 1,
    evicted_record_count: 0,
    overflowed_record_count: 0,
    history_truncated_trace_count: 0,
    dropped_trace_count: 0,
    dropped_exchange_count: 0,
    dropped_transition_count: 0,
  },
};

export const sessionBStatus: DebugCaptureStatus = {
  ...activeStatus,
  session: {
    ...activeStatus.session!,
    session_id: "session-b",
    generation: 2,
  },
};

export const provider: Provider = {
  id: "provider-a",
  name: "Provider A",
  api_types: [],
  auth_mode: "auto",
  credential_sessions: [],
  group_id: null,
  weight: 1,
  priority: 0,
  concurrency: 10,
  max_retries: 0,
  vendor: "",
  failover_scope: "any",
  accept_failover: "any",
  enabled: true,
  created_at: "2026-08-01T00:00:00Z",
  updated_at: "2026-08-01T00:00:00Z",
};

export const recordsPage: DebugCaptureRecordsPage = {
  session_id: "session-a",
  snapshot_watermark: "watermark-a",
  records: [
    {
      session_id: "session-a",
      record_id: "record-a",
      gateway_trace_id: "gateway-trace-a",
      gateway_request_id: "gateway-a",
      exchange_index: 0,
      record_sequence: 1,
      provider: {
        id: "provider-a",
        name: "Provider A",
        api_type: "claude",
        target_url: "https://example.test/v1/messages",
      },
      protocol: "http",
      selection_mode: "initial",
      selection_source: "strategy",
      provider_attempt_index: 0,
      credential_phase: "initial",
      lifecycle_state: "active",
      source_completion: "pending",
      capture_completion: "overflowed",
      started_at: "2026-08-01T00:00:01Z",
      has_failure: false,
      failure: emptyFailureObservation,
      upstream_observed_bytes: 12,
      application_write_confirmed_bytes: 4,
    },
  ],
  gateway_traces: [
    {
      gateway_trace_id: "gateway-trace-a",
      gateway_request_id: "gateway-a",
      history_truncated_before: false,
      history_truncated_after: false,
      entries: [
        {
          kind: "transition",
          entry_id: "transition-a",
          sequence: 1,
          provider: {
            id: "provider-b",
            name: "Provider B",
            api_type: "claude",
            target_url: "https://provider-b.test",
          },
          provider_attempt_index: 0,
          selection_mode: "replacement",
          selection_source: "strategy",
          credential_phase: "initial",
          termination_reason: "capture_fault",
          has_failure: true,
          failure: {
            primary: {
              site: "transport",
              peer: "provider",
              class: "transport",
              code: "connection",
              system_error_code: 10_061,
              message:
                "provider\u0000 connection failed after partial metadata capture",
            },
            secondary: {
              site: "response_drain",
              peer: "upstream",
              class: "read",
              code: "drain_read",
              http_status_code: 502,
            },
            has_secondary: true,
            truncated: true,
          },
          metadata_truncated: true,
        },
        {
          kind: "record",
          entry_id: "record-entry-a",
          sequence: 2,
          record_id: "record-a",
          provider: {
            id: "provider-a",
            name: "Provider A",
            api_type: "claude",
            target_url: "https://example.test/v1/messages",
          },
          provider_attempt_index: 0,
          selection_mode: "initial",
          selection_source: "strategy",
          credential_phase: "initial",
          has_failure: false,
          failure: emptyFailureObservation,
          metadata_truncated: false,
        },
      ],
    },
  ],
  eviction_gap: { detected: false, record_count: 0 },
};

export const recordDetail: DebugCaptureRecordDetail = {
  summary: recordsPage.records[0],
  snapshot_state: "active_partial",
  gateway_trace: recordsPage.gateway_traces[0],
  http: {
    request: {
      method: "POST",
      url: "https://example.test/v1/messages",
      host: "example.test",
      headers: { "Content-Type": ["application/json"] },
      content_length: 17,
      ingress: {
        protocol: "HTTP/1.1",
        content_length: -1,
        transfer_encoding: ["chunked"],
        declared_trailer_keys: ["X-End"],
        state: "receiving",
        received_bytes: 17,
        capture_truncated: false,
      },
    },
    request_body: {
      data_base64: btoa('{"hello":"world"}'),
      preview_bytes: 17,
      captured_bytes: 17,
      truncated: false,
      checksum_sha256: "request-checksum",
    },
    response: {
      status_code: 200,
      protocol: "HTTP/2.0",
      headers: { "Content-Type": ["application/json"] },
      content_length: 11,
    },
    response_body: {
      data_base64: btoa('{"ok":true}'),
      preview_bytes: 11,
      captured_bytes: 11,
      truncated: false,
      checksum_sha256: "response-checksum",
    },
  },
};

export const websocketRecordDetail: DebugCaptureRecordDetail = {
  summary: {
    ...recordsPage.records[0],
    protocol: "websocket",
    lifecycle_state: "completed",
    source_completion: "partial",
    capture_completion: "complete",
    termination_reason: "websocket_relay_error",
    has_failure: true,
    failure: {
      primary: {
        site: "websocket_relay",
        peer: "client",
        class: "write",
        code: "relay_write",
        message: "relay failed after the close frame",
      },
      secondary: {
        site: "websocket_close",
        peer: "upstream",
        class: "websocket_close",
        code: "websocket_close",
        websocket_close_code: 1011,
      },
      has_secondary: true,
      truncated: true,
    },
  },
  snapshot_state: "final",
  gateway_trace: null,
  websocket: {
    request: {
      method: "GET",
      url: "wss://example.test/v1/socket",
      host: "example.test",
      headers: { Upgrade: ["websocket"] },
      content_length: 0,
    },
    request_body: {
      data_base64: "",
      preview_bytes: 0,
      captured_bytes: 0,
      truncated: false,
      checksum_sha256: "request-checksum",
    },
    handshake: {
      status_code: 101,
      protocol: "HTTP/1.1",
      headers: { Upgrade: ["websocket"] },
    },
    handshake_body: {
      data_base64: "",
      preview_bytes: 0,
      captured_bytes: 0,
      truncated: false,
      checksum_sha256: "handshake-checksum",
    },
    messages: [
      {
        message_id: "message-a",
        sequence: 1,
        relative_millis: 42,
        direction: "upstream_to_client",
        message_type: "text",
        source: "live",
        disposition: "write_failed",
        client_visible: false,
        has_failure: true,
        failure: {
          primary: {
            site: "websocket_message",
            peer: "client",
            class: "write",
            code: "message_write",
            system_error_code: 10_054,
            message: "client write failed",
          },
          secondary: emptyFailureObservation.secondary,
          has_secondary: false,
          truncated: true,
        },
        payload: {
          data_base64: btoa("partial payload"),
          preview_bytes: 15,
          captured_bytes: 15,
          truncated: false,
          checksum_sha256: "message-checksum",
        },
      },
    ],
    events_truncated: false,
    close: {
      direction: "upstream_to_client",
      code: 1011,
      reason: "upstream failure",
      reason_truncated: true,
      clean: false,
    },
  },
};
