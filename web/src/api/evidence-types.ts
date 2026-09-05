import type { InternalErrorRuleSpec } from "@/features/error-detection/contracts/types";

export interface RequestEvidenceGateway {
  readonly terminal_status_code?: number;
  readonly terminal_error_code?: string;
  readonly terminal_message_snippet?: string;
}

export interface RequestEvidenceUpstreamHandshake {
  readonly status_code?: number;
  readonly body_snippet?: string;
}

/** Historical transport evidence used by envelopes with no version or v1. */
export interface RequestEvidenceTransport {
  readonly source?: string;
  readonly message_snippet?: string;
  readonly is_timeout?: boolean;
  readonly is_client_cancel?: boolean;
  readonly raw_error_snippet?: string;
}

export type TransportEvidenceSource = "upstream" | "client";
export type TransportEvidenceStage =
  "pre_connection_visible" | "pre_payload_visible" | "post_payload_visible";
export type TransportEvidenceKind =
  "timeout" | "disconnect" | "protocol_error" | "local_error";
export type TransportEvidenceSignal =
  | "sse_idle_timeout"
  | "upstream_read_error"
  | "client_write_error"
  | "eof"
  | "unexpected_eof"
  | "close_without_status"
  | "close_error"
  | "timeout"
  | "canceled"
  | "unknown_transport";

export interface RequestEvidenceTransportV2 {
  readonly source?: TransportEvidenceSource;
  readonly stage?: TransportEvidenceStage;
  readonly kind?: TransportEvidenceKind;
  readonly signal?: TransportEvidenceSignal;
  readonly raw_error_snippet?: string;
  readonly close_code?: number;
  readonly close_reason_snippet?: string;
}

export interface RequestEvidenceUpstreamEvent {
  readonly envelope_type?: string;
  readonly provider_error_type?: string;
  readonly provider_error_code?: string;
  readonly status_code?: number;
  readonly message_snippet?: string;
  readonly raw_payload_snippet?: string;
}

export type SemanticBoundaryReason =
  | "request_probe_memory_exhausted"
  | "process_probe_memory_exhausted"
  | "unsupported_response_protocol"
  | "unsupported_content_encoding"
  | "content_decoding_failed"
  | "malformed_protocol_frame"
  | "decoded_event_too_large"
  | "semantic_field_too_large"
  | "analysis_internal_error"
  | "no_retry_candidate"
  | "passthrough_only"
  | "semantic_match"
  | "client_visible_event"
  | "probe_duration_elapsed"
  | "upstream_eof_no_match"
  | "upstream_read_failure"
  | "client_cancelled";

export type SemanticDecisionValue =
  | "passthrough"
  | "observe_only"
  | "commit_current"
  | "retry_same"
  | "switch_provider"
  | "abort_client";

export type SemanticDecisionReason =
  | "action_passthrough"
  | "observer_only"
  | "retry_budget_available"
  | "rule_retry_budget_exhausted"
  | "global_attempt_budget_exhausted"
  | "reserved_switch_attempt"
  | "alternate_provider_unavailable"
  | "alternate_reservation_failed"
  | "provider_deleted"
  | "provider_disabled"
  | "api_removed"
  | "routing_changed"
  | "group_disabled"
  | "auth_unavailable"
  | "provider_lookup_error"
  | "response_already_visible"
  | "client_retry_requested"
  | "client_cancelled";

export type SemanticFieldName = "type" | "code" | "message" | "reason";
export type SemanticRuleAction =
  "passthrough" | "retry_only" | "retry_then_switch";
export type SemanticAlternateOutcome =
  | "not_requested"
  | "reserved"
  | "activated"
  | "unavailable"
  | "failed"
  | "released";
export type SemanticSwitchMode = "replacement" | "failover";
export type SemanticSwitchReason =
  "internal_error_rule_exhausted" | "internal_error_provider_unavailable";
export type SemanticHealthVerdict = "success" | "failure" | "neutral";
export type SemanticHealthCause =
  | "normal_completion"
  | "transport_failure"
  | "http_status_failure"
  | "semantic_retry_then_switch"
  | "semantic_neutral"
  | "client_cancelled"
  | "incomplete";

export interface SemanticEvidenceIdentity {
  readonly request_id: string;
  readonly operation_id: string;
  readonly provider_id: string;
  readonly logical_attempt: string;
  readonly provider_attempt: string;
  readonly credential_phase: string;
}

export interface SemanticEvidenceResponse {
  readonly protocol_id: string;
  readonly state: "probing" | "forwarding" | "discarded";
  readonly match_timing: "probing" | "forwarding";
  readonly boundary_reason: SemanticBoundaryReason;
  readonly elapsed_ms: string;
  readonly peak_probe_bytes: string;
  readonly raw_probe_bytes: string;
  readonly decoded_probe_bytes: string;
  readonly upstream_bytes_read: string;
  readonly client_body_bytes_written: string;
  readonly headers_committed: boolean;
  readonly visible_to_client: boolean;
}

export interface SemanticRuleSnapshot extends InternalErrorRuleSpec {
  readonly position: number;
}

export interface SemanticEvidenceRule {
  readonly revision: string;
  readonly winner_id: string;
  readonly normalized_snapshot: SemanticRuleSnapshot;
  readonly matching_rule_ids: readonly string[];
  readonly matched_keywords: readonly string[];
  readonly matched_keyword_indexes: readonly number[];
  readonly matched_fields: readonly SemanticFieldName[];
}

export interface SemanticEvidenceRetry {
  readonly action: SemanticRuleAction;
  readonly global_attempts_started: string;
  readonly global_attempts_remaining: string | null;
  readonly global_attempts_unlimited: boolean;
  readonly rule_retries_scheduled: string;
  readonly rule_retry_limit: number;
}

export interface SemanticEvidenceAlternate {
  readonly outcome: SemanticAlternateOutcome;
  readonly provider_id: string | null;
  readonly switch_mode: SemanticSwitchMode | null;
  readonly switch_reason: SemanticSwitchReason | null;
}

export interface SemanticEvidenceDecision {
  readonly value: SemanticDecisionValue;
  readonly reason: SemanticDecisionReason;
}

export interface SemanticEvidenceHealth {
  readonly verdict: SemanticHealthVerdict;
  readonly cause: SemanticHealthCause;
  readonly circuit_opened: boolean;
}

export interface SemanticErrorEvidence {
  readonly schema_version: 1;
  readonly identity: SemanticEvidenceIdentity;
  readonly response: SemanticEvidenceResponse;
  readonly rule: SemanticEvidenceRule;
  readonly retry: SemanticEvidenceRetry;
  readonly alternate: SemanticEvidenceAlternate;
  readonly decision: SemanticEvidenceDecision;
  readonly health: SemanticEvidenceHealth;
}

interface RequestEvidenceSections<TTransport> {
  readonly gateway?: RequestEvidenceGateway | null;
  readonly upstream_handshake?: RequestEvidenceUpstreamHandshake | null;
  readonly transport?: TTransport | null;
  readonly upstream_event?: RequestEvidenceUpstreamEvent | null;
}

export interface RequestEvidenceV1 extends RequestEvidenceSections<RequestEvidenceTransport> {
  readonly v?: 1;
}

export interface RequestEvidenceV2 extends RequestEvidenceSections<RequestEvidenceTransportV2> {
  readonly client_disguise?:
    import("./client-disguise/evidence").DisguiseEvidence | null;
  readonly v: 2;
  readonly semantic_error?: SemanticErrorEvidence | null;
}

export type RequestEvidence = RequestEvidenceV1 | RequestEvidenceV2;

export type RequestEvidenceUnavailableReason =
  "malformed_json" | "unsupported_version" | "invalid_schema";

export type RequestEvidenceDecodeResult =
  | { readonly state: "absent" }
  | { readonly state: "available"; readonly evidence: RequestEvidence }
  | {
      readonly state: "unavailable";
      readonly reason: RequestEvidenceUnavailableReason;
      readonly detail: string;
    };
