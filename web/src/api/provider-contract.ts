import type {
  CredentialSession,
  CredentialSessionAuthState,
  CredentialSubject,
  Provider,
  ProviderCredentialSession,
  ProviderUsageSnapshot,
} from "./types";

type JsonRecord = Record<string, unknown>;

const CREDENTIAL_KINDS = ["api_key", "chatgpt"] as const;
const SUBJECT_KINDS = ["pending", "account", "keyed_digest"] as const;
const AUTH_STATUSES = ["not_connected", "active", "reauth_required"] as const;

export class ProviderContractError extends Error {
  constructor(message: string) {
    super(`Invalid provider API response: ${message}`);
    this.name = "ProviderContractError";
  }
}

function fail(message: string): never {
  throw new ProviderContractError(message);
}

function record(value: unknown, label: string): JsonRecord {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    fail(`${label} must be an object`);
  }
  return value as JsonRecord;
}

function stringValue(value: unknown, label: string): string {
  if (typeof value !== "string") {
    fail(`${label} must be a string`);
  }
  return value;
}

function numberValue(value: unknown, label: string): number {
  if (typeof value !== "number" || !Number.isFinite(value)) {
    fail(`${label} must be a finite number`);
  }
  return value;
}

function booleanValue(value: unknown, label: string): boolean {
  if (typeof value !== "boolean") {
    fail(`${label} must be a boolean`);
  }
  return value;
}

function optionalString(value: unknown, label: string): string | undefined {
  return value === undefined ? undefined : stringValue(value, label);
}

function enumValue<T extends readonly string[]>(
  value: unknown,
  allowed: T,
  label: string,
): T[number] {
  const parsed = stringValue(value, label);
  if (!allowed.includes(parsed)) {
    fail(`${label} must be one of: ${allowed.join(", ")}`);
  }
  return parsed as T[number];
}

function parseUsageSnapshot(
  value: unknown,
  label: string,
): ProviderUsageSnapshot | null | undefined {
  if (value === undefined || value === null) {
    return value;
  }
  const source = record(value, label);
  return source as unknown as ProviderUsageSnapshot;
}

function parseAuthState(
  value: unknown,
  label: string,
): CredentialSessionAuthState {
  const source = record(value, label);
  return {
    status: enumValue(source.status, AUTH_STATUSES, `${label}.status`),
    status_reason: optionalString(
      source.status_reason,
      `${label}.status_reason`,
    ),
    last_error: optionalString(source.last_error, `${label}.last_error`),
    last_transition_at: optionalString(
      source.last_transition_at,
      `${label}.last_transition_at`,
    ),
    email: optionalString(source.email, `${label}.email`),
    account_id: optionalString(source.account_id, `${label}.account_id`),
    plan_type: optionalString(source.plan_type, `${label}.plan_type`),
    expires_at: optionalString(source.expires_at, `${label}.expires_at`),
    last_refresh_at: optionalString(
      source.last_refresh_at,
      `${label}.last_refresh_at`,
    ),
    usage_snapshot: parseUsageSnapshot(
      source.usage_snapshot,
      `${label}.usage_snapshot`,
    ),
    refresh_fail_count:
      source.refresh_fail_count === undefined
        ? undefined
        : numberValue(source.refresh_fail_count, `${label}.refresh_fail_count`),
    last_refresh_failure_at: optionalString(
      source.last_refresh_failure_at,
      `${label}.last_refresh_failure_at`,
    ),
  };
}

function parseSubject(value: unknown, label: string): CredentialSubject {
  const source = record(value, label);
  return {
    kind: enumValue(source.kind, SUBJECT_KINDS, `${label}.kind`),
    value: optionalString(source.value, `${label}.value`),
    key_version: optionalString(source.key_version, `${label}.key_version`),
  };
}

function parseProviderCredentialSession(
  value: unknown,
  label: string,
): ProviderCredentialSession {
  const source = record(value, label);
  return {
    id: stringValue(source.id, `${label}.id`),
    kind: enumValue(source.kind, CREDENTIAL_KINDS, `${label}.kind`),
    version: numberValue(source.version, `${label}.version`),
    subject: parseSubject(source.subject, `${label}.subject`),
    auth_state: parseAuthState(source.auth_state, `${label}.auth_state`),
  };
}

export function parseCredentialSession(value: unknown): CredentialSession {
  const source = record(value, "credential_session");
  const base = parseProviderCredentialSession(source, "credential_session");
  if (!Array.isArray(source.referenced_route_target_ids)) {
    fail("credential_session.referenced_route_target_ids must be an array");
  }
  return {
    ...base,
    secret_data:
      base.kind === "api_key"
        ? stringValue(source.secret_data, "credential_session.secret_data")
        : undefined,
    referenced_route_target_ids: source.referenced_route_target_ids.map(
      (id, index) =>
        stringValue(
          id,
          `credential_session.referenced_route_target_ids[${index}]`,
        ),
    ),
    created_at: stringValue(source.created_at, "credential_session.created_at"),
    updated_at: stringValue(source.updated_at, "credential_session.updated_at"),
  };
}

export function parseCredentialSessions(value: unknown): CredentialSession[] {
  if (!Array.isArray(value)) {
    fail("credential sessions response must be an array");
  }
  return value.map(parseCredentialSession);
}

export function parseProvider(value: unknown): Provider {
  const source = record(value, "provider");
  if (!Array.isArray(source.api_types)) {
    fail("provider.api_types must be an array");
  }
  if (!Array.isArray(source.credential_sessions)) {
    fail("provider.credential_sessions must be an array");
  }

  const sessions = source.credential_sessions.map((session, index) =>
    parseProviderCredentialSession(
      session,
      `provider.credential_sessions[${index}]`,
    ),
  );
  const sessionIDs = new Set(sessions.map((session) => session.id));
  const apiTypes = source.api_types.map((entry, index) => {
    const route = record(entry, `provider.api_types[${index}]`);
    const credentialSessionID = stringValue(
      route.credential_session_id,
      `provider.api_types[${index}].credential_session_id`,
    );
    if (!sessionIDs.has(credentialSessionID)) {
      fail(
        `provider.api_types[${index}] references absent credential session ${credentialSessionID}`,
      );
    }
    return {
      api_type: stringValue(
        route.api_type,
        `provider.api_types[${index}].api_type`,
      ),
      base_url: stringValue(
        route.base_url,
        `provider.api_types[${index}].base_url`,
      ),
      credential_session_id: credentialSessionID,
    };
  });

  return {
    id: stringValue(source.id, "provider.id"),
    name: stringValue(source.name, "provider.name"),
    api_types: apiTypes,
    auth_mode: stringValue(
      source.auth_mode,
      "provider.auth_mode",
    ) as Provider["auth_mode"],
    credential_sessions: sessions,
    usage_limit_policy: optionalString(
      source.usage_limit_policy,
      "provider.usage_limit_policy",
    ) as Provider["usage_limit_policy"],
    usage_limit_policy_explicit:
      source.usage_limit_policy_explicit === undefined
        ? undefined
        : booleanValue(
            source.usage_limit_policy_explicit,
            "provider.usage_limit_policy_explicit",
          ),
    group_id:
      source.group_id === null
        ? null
        : stringValue(source.group_id, "provider.group_id"),
    weight: numberValue(source.weight, "provider.weight"),
    priority: numberValue(source.priority, "provider.priority"),
    concurrency: numberValue(source.concurrency, "provider.concurrency"),
    max_retries: numberValue(source.max_retries, "provider.max_retries"),
    backoff: source.backoff as Provider["backoff"],
    vendor: stringValue(source.vendor, "provider.vendor"),
    failover_scope: stringValue(
      source.failover_scope,
      "provider.failover_scope",
    ) as Provider["failover_scope"],
    accept_failover: stringValue(
      source.accept_failover,
      "provider.accept_failover",
    ) as Provider["accept_failover"],
    enabled: booleanValue(source.enabled, "provider.enabled"),
    created_at: stringValue(source.created_at, "provider.created_at"),
    updated_at: stringValue(source.updated_at, "provider.updated_at"),
    health: source.health as Provider["health"],
  };
}

export function parseProviders(value: unknown): Provider[] {
  if (!Array.isArray(value)) {
    fail("providers response must be an array");
  }
  return value.map(parseProvider);
}
