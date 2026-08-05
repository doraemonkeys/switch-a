import type { BackoffPolicy } from "@/api/retry-policy-types";
import type { ErrorCode } from "@/config/constants";

export const INTERNAL_ERROR_SCHEMA_VERSION = 1 as const;

export type RuleBackoffPolicy = Required<BackoffPolicy>;

export type InternalErrorRuleTarget =
  | { readonly kind: "global" }
  | { readonly kind: "provider"; readonly provider_id: string };

export type InternalErrorRuleAction =
  | { readonly type: "passthrough" }
  | {
      readonly type: "retry_only";
      readonly max_retries: number;
      readonly backoff: RuleBackoffPolicy;
      readonly visible_response?: "disconnect_client" | "commit_current";
    }
  | {
      readonly type: "retry_then_switch";
      readonly max_retries: number;
      readonly backoff: RuleBackoffPolicy;
      readonly visible_response?: "disconnect_client" | "commit_current";
    };

export interface InternalErrorRuleSpec {
  readonly name: string;
  readonly enabled: boolean;
  readonly target: InternalErrorRuleTarget;
  readonly api_type: string | null;
  readonly keywords: readonly string[];
  readonly match_mode: "any" | "all";
  readonly action: InternalErrorRuleAction;
}

export interface InternalErrorRule extends InternalErrorRuleSpec {
  readonly id: string;
  readonly position: number;
  readonly created_at: string;
  readonly updated_at: string;
}

export interface InternalErrorRuleListResponse {
  readonly schema_version: typeof INTERNAL_ERROR_SCHEMA_VERSION;
  readonly rule_set_revision: string;
  readonly rules: readonly InternalErrorRule[];
}

export interface InternalErrorRuleResponse {
  readonly schema_version: typeof INTERNAL_ERROR_SCHEMA_VERSION;
  readonly rule_set_revision: string;
  readonly rule: InternalErrorRule;
}

export interface InternalErrorRuleStat {
  readonly rule_id: string;
  readonly hit_count: string;
  readonly last_hit_at: string | null;
}

export interface InternalErrorRuleStatsResponse {
  readonly schema_version: typeof INTERNAL_ERROR_SCHEMA_VERSION;
  readonly rule_set_revision: string;
  readonly stats: readonly InternalErrorRuleStat[];
}

export interface InternalErrorRuleMutationRequest {
  readonly schema_version: typeof INTERNAL_ERROR_SCHEMA_VERSION;
  readonly rule: InternalErrorRuleSpec;
}

export interface InternalErrorRuleReorderRequest {
  readonly schema_version: typeof INTERNAL_ERROR_SCHEMA_VERSION;
  readonly ordered_rule_ids: readonly string[];
}

export type TestMessageBody =
  | { readonly encoding: "utf8"; readonly value: string }
  | { readonly encoding: "base64"; readonly value: string };

export interface TestMessageInput {
  readonly api_type: string;
  readonly provider_id: string | null;
  readonly content_type: string;
  readonly content_encoding: string;
  readonly body: TestMessageBody;
}

export interface TestMessageRequest extends TestMessageInput {
  readonly schema_version: typeof INTERNAL_ERROR_SCHEMA_VERSION;
}

export type SemanticFieldName = "type" | "code" | "message" | "reason";

export interface TestMessageMatch {
  readonly rule_id: string;
  readonly matched_keywords: readonly string[];
  readonly matched_keyword_indexes: readonly number[];
  readonly matched_fields: readonly SemanticFieldName[];
}

export interface TestMessageExtractedError {
  readonly frame_index: number;
  readonly type?: string;
  readonly code?: string;
  readonly message?: string;
  readonly reason?: string;
  readonly matches: readonly TestMessageMatch[];
}

export interface TestMessageWinner extends TestMessageMatch {
  readonly error_index: number;
}

export type TestMessageAnalysisReason =
  | "request_probe_memory_exhausted"
  | "process_probe_memory_exhausted"
  | "unsupported_response_protocol"
  | "unsupported_content_encoding"
  | "content_decoding_failed"
  | "malformed_protocol_frame"
  | "decoded_event_too_large"
  | "semantic_field_too_large"
  | "analysis_internal_error";

export interface TestMessageResponse {
  readonly schema_version: typeof INTERNAL_ERROR_SCHEMA_VERSION;
  readonly rule_set_revision: string;
  readonly response_protocol_id: string | null;
  readonly analysis_status: "complete" | "fail_open";
  readonly analysis_reason: TestMessageAnalysisReason | null;
  readonly errors: readonly TestMessageExtractedError[];
  readonly decisive_error_index: number | null;
  readonly winner: TestMessageWinner | null;
}

export type InternalErrorAPIErrorCode = Extract<
  ErrorCode,
  | "VALIDATION_ERROR"
  | "NOT_FOUND"
  | "CONFLICT"
  | "REVISION_MISMATCH"
  | "REQUEST_TOO_LARGE"
  | "PRECONDITION_REQUIRED"
  | "INTERNAL_ERROR"
>;

export type InternalErrorNotFoundDetails =
  | { readonly rule_id: string; readonly provider_id?: never }
  | { readonly provider_id: string; readonly rule_id?: never };

export type InternalErrorAPIErrorResponse =
  | {
      readonly code: "VALIDATION_ERROR";
      readonly message: string;
      readonly details: { readonly field: string };
    }
  | {
      readonly code: "NOT_FOUND";
      readonly message: string;
      readonly details: InternalErrorNotFoundDetails;
    }
  | {
      readonly code: "CONFLICT";
      readonly message: string;
      readonly details: { readonly limit: number } | Record<string, never>;
    }
  | {
      readonly code: "REVISION_MISMATCH";
      readonly message: string;
      readonly details: { readonly current_revision: string };
    }
  | {
      readonly code: "REQUEST_TOO_LARGE";
      readonly message: string;
      readonly details: { readonly limit_bytes: number };
    }
  | {
      readonly code: "PRECONDITION_REQUIRED" | "INTERNAL_ERROR";
      readonly message: string;
      readonly details: Record<string, never>;
    };

export type InternalErrorRuleETag = `"internal-error-rules/${string}"`;

export interface RevisionedInternalErrorResource<T> {
  readonly value: T;
  readonly etag: InternalErrorRuleETag;
}

export interface DeletedInternalErrorRule {
  readonly rule_set_revision: string;
  readonly etag: InternalErrorRuleETag;
}
