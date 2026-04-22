// =============================================================================
// Request Evidence Types
// =============================================================================
//
// Extracted from ./types.ts because the transport observability v2 schema added
// enough surface area that the aggregate `types.ts` crossed the sloc-guard limit.
// Evidence types form their own semantic domain (gateway / upstream_handshake /
// transport / upstream_event envelopes) so colocating them here keeps the
// domain boundary intentional rather than accidental.

export interface RequestEvidenceGateway {
  terminal_status_code?: number;
  terminal_error_code?: string;
  terminal_message_snippet?: string;
}

export interface RequestEvidenceUpstreamHandshake {
  status_code?: number;
  body_snippet?: string;
}

/**
 * v1 transport evidence (pre-"v":2 schema).
 *
 * Intentionally preserved so historical data authored before the v2 schema
 * migration continues to render through the v1 renderer without remapping.
 */
export interface RequestEvidenceTransport {
  source?: string;
  message_snippet?: string;
  is_timeout?: boolean;
  is_client_cancel?: boolean;
  raw_error_snippet?: string;
}

// =============================================================================
// Transport Evidence v2 (evidence JSON top-level "v": 2)
// =============================================================================

/** Attribution of the transport-level fault: client-side or upstream-side. */
export type TransportEvidenceSource = "upstream" | "client";

/**
 * Three-state lifecycle boundary describing whether payload had become visible
 * to the client before the fault. Drives the humanized "stage-phrase" used in
 * summaries (e.g. "before payload visible").
 */
export type TransportEvidenceStage =
  | "pre_connection_visible"
  | "pre_payload_visible"
  | "post_payload_visible";

/** Root-cause classification used by summary text; distinct from signal. */
export type TransportEvidenceKind =
  | "timeout"
  | "disconnect"
  | "protocol_error"
  | "local_error";

/** Protocol-layer observation feeding the detail view. */
export type TransportEvidenceSignal =
  // SSE
  | "sse_idle_timeout"
  | "upstream_read_error"
  | "client_write_error"
  // WebSocket
  | "eof"
  | "unexpected_eof"
  | "close_without_status"
  | "close_error"
  | "timeout"
  | "canceled"
  // Shared fallback
  | "unknown_transport";

/**
 * v2 transport evidence. Emitted when evidence JSON carries `"v": 2`. Supersedes
 * the v1 shape (message_snippet / is_timeout / is_client_cancel); the v1
 * structure remains usable only through the v1 renderer path.
 */
export interface RequestEvidenceTransportV2 {
  source?: TransportEvidenceSource;
  stage?: TransportEvidenceStage;
  kind?: TransportEvidenceKind;
  signal?: TransportEvidenceSignal;
  raw_error_snippet?: string;
  /** WebSocket only; absent unless a real *websocket.CloseError was observed. */
  close_code?: number;
  /** WebSocket only. */
  close_reason_snippet?: string;
}

export interface RequestEvidenceUpstreamEvent {
  envelope_type?: string;
  provider_error_type?: string;
  provider_error_code?: string;
  status_code?: number;
  message_snippet?: string;
  raw_payload_snippet?: string;
}

/**
 * Parsed evidence envelope. `v` routes rendering:
 *   - `2` → v2 renderer consumes `RequestEvidenceTransportV2` shape.
 *   - missing or `1` → v1 renderer consumes `RequestEvidenceTransport` shape.
 *
 * The `transport` slot is a union across versions; readers must branch on `v`
 * rather than probing for missing v2-specific keys, so that fields like
 * `signal === "unknown_transport"` are not silently mistaken for legacy data.
 */
export interface RequestEvidence {
  /** Schema version; `2` enables the v2 renderer path. */
  v?: number;
  gateway?: RequestEvidenceGateway | null;
  upstream_handshake?: RequestEvidenceUpstreamHandshake | null;
  transport?: RequestEvidenceTransport | RequestEvidenceTransportV2 | null;
  upstream_event?: RequestEvidenceUpstreamEvent | null;
}
