import { list, record, str, stringMap } from "./decoder";
export interface DisguiseEvidence {
  diagnostic_id: string;
  decision: string;
  truncated?: boolean;
  applied_scopes?: string[];
  context: Record<string, string>;
  platform_facts: Record<string, string>;
  candidates: {
    provider_id: string;
    credential_session_id?: string;
    outcome: string;
    reason?: string;
    platform?: string;
  }[];
  differences: {
    carrier: string;
    location: string;
    original: string;
    derived: string;
  }[];
  failure?: {
    phase: string;
    location: string;
    error_chain: string[];
    original_snippet?: string;
    derived_snippet?: string;
  };
}
export function parseDisguiseEvidence(value: unknown): DisguiseEvidence {
  const item = record(value);
  const optional = (source: Record<string, unknown>, key: string) =>
    source[key] == null ? undefined : str(source[key]);
  const context: Record<string, string> = {};
  for (const key of [
    "request_id",
    "operation_id",
    "provider_id",
    "credential_session_id",
    "account_id",
    "client_identity_id",
    "generation_id",
    "device_id",
    "client_version",
    "revision_id",
    "transport_sample_id",
    "source_id",
    "captured_at",
    "client_type",
    "platform",
    "arch",
    "phase",
  ]) {
    const field = optional(item, key);
    if (field) context[key] = field;
  }
  const failure = item.failure == null ? null : record(item.failure);
  return {
    diagnostic_id: str(item.diagnostic_id),
    decision: str(item.decision),
    truncated: item.truncated === true,
    applied_scopes: list(item.applied_scopes ?? [], str),
    context,
    platform_facts:
      item.platform_facts == null ? {} : stringMap(item.platform_facts),
    candidates: list(item.candidates ?? [], (value) => {
      const candidate = record(value);
      return {
        provider_id: str(candidate.provider_id),
        credential_session_id: optional(candidate, "credential_session_id"),
        outcome: str(candidate.outcome),
        reason: optional(candidate, "reason"),
        platform: optional(candidate, "platform"),
      };
    }),
    differences: list(item.differences ?? [], (value) => {
      const difference = record(value);
      return {
        carrier: str(difference.carrier),
        location: str(difference.location),
        original: str(difference.original),
        derived: str(difference.derived),
      };
    }),
    ...(failure
      ? {
          failure: {
            phase: str(failure.phase),
            location: str(failure.location),
            error_chain: list(failure.error_chain, str),
            original_snippet: optional(failure, "original_snippet"),
            derived_snippet: optional(failure, "derived_snippet"),
          },
        }
      : {}),
  };
}
