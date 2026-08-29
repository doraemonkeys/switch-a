import type {
  AuthMode,
  ConfigKey,
  FailoverScope,
  ProviderUsageLimitPolicy,
  Strategy,
} from "../config/constants";
import type { BackoffPolicy } from "./retry-policy-types";
import type { RoutingPolicyModelMatchType } from "./routing-policy-types";

export interface ExportedAPIType {
  api_type: string;
  base_url: string;
  credential_session_id: string;
}

export interface ExportedProvider {
  id: string;
  name: string;
  api_types: ExportedAPIType[];
  auth_mode: AuthMode;
  usage_limit_policy?: ProviderUsageLimitPolicy;
  group_id?: string | null;
  weight: number;
  priority: number;
  concurrency: number;
  max_retries: number;
  backoff?: BackoffPolicy;
  vendor?: string;
  failover_scope?: FailoverScope;
  accept_failover?: FailoverScope;
  enabled: boolean;
}

export type CredentialSessionKind = "api_key" | "chatgpt";
export type CredentialSessionTransferMode = "static_secret" | "reauthenticate";

export interface ExportedCredentialSubject {
  kind: "pending" | "account" | "keyed_digest";
  value?: string;
  key_version?: string;
}

export interface ExportedCredentialUsageWindow {
  used_percent: number;
  window_seconds: number;
  reset_at?: string;
}

export interface ExportedCredentialUsageSnapshot {
  fetched_at?: string;
  plan_type?: string;
  five_hour?: ExportedCredentialUsageWindow;
  one_week?: ExportedCredentialUsageWindow;
}

export interface ExportedCredentialAuthState {
  status: "not_connected" | "active" | "reauth_required";
  status_reason?: string;
  last_error?: string;
  last_transition_at?: string;
  email?: string;
  account_id?: string;
  plan_type?: string;
  expires_at?: string;
  last_refresh_at?: string;
  usage_snapshot?: ExportedCredentialUsageSnapshot;
  refresh_fail_count?: number;
  last_refresh_failure_at?: string;
}

export interface ExportedCredentialSession {
  id: string;
  name: string;
  kind: CredentialSessionKind;
  transfer_mode: CredentialSessionTransferMode;
  secret_data?: string;
  version: number;
  subject: ExportedCredentialSubject;
  auth_state: ExportedCredentialAuthState;
}

export interface ExportedGroup {
  id: string;
  name: string;
  strategy: Strategy;
  priority: number;
  weight: number;
  enabled: boolean;
}

export interface ExportedRoutingPolicy {
  api_type: string;
  enabled: boolean;
  model_match_type?: RoutingPolicyModelMatchType | null;
  model_match_value?: string | null;
  target_provider_id?: string | null;
  allowed_group_ids: string[];
  allowed_vendors: string[];
}

export interface ExportedInternalErrorRule {
  id: string;
  name: string;
  enabled: boolean;
  target: { kind: "global" } | { kind: "provider"; provider_id: string };
  api_type: string | null;
  keywords: string[];
  match_mode: "any" | "all";
  action:
    | { type: "passthrough" }
    | {
        type: "retry_only" | "retry_then_switch";
        max_retries: number;
        backoff: Required<BackoffPolicy>;
        visible_response?: "disconnect_client" | "commit_current";
      };
}

export interface ExportedConfig {
  version: string;
  exported_at: string;
  providers: ExportedProvider[];
  credential_sessions: ExportedCredentialSession[];
  groups: ExportedGroup[];
  routing_policies: ExportedRoutingPolicy[];
  settings: Partial<Record<ConfigKey, string>>;
  internal_error_rules: ExportedInternalErrorRule[];
}

export type ImportMode = "full" | "settings_only" | "selection";

export interface FullImportScope {
  mode: "full";
}

export interface SettingsOnlyImportScope {
  mode: "settings_only";
}

export interface SelectionImportScope {
  mode: "selection";
  selection: {
    group_ids: string[];
    provider_ids: string[];
  };
}

export type ImportScope =
  FullImportScope | SettingsOnlyImportScope | SelectionImportScope;

export interface ImportConfigRequest {
  version: string;
  import_scope: ImportScope;
  providers: ExportedProvider[];
  credential_sessions: ExportedCredentialSession[];
  groups: ExportedGroup[];
  routing_policies: ExportedRoutingPolicy[];
  settings: Partial<Record<ConfigKey, string>>;
  internal_error_rules: ExportedInternalErrorRule[];
}

export interface ChangeCount {
  add: number;
  update: number;
  delete: number;
  unchanged: number;
}

export interface ImportChanges {
  providers: ChangeCount;
  credential_sessions: ChangeCount;
  groups: ChangeCount;
  routing_policies: ChangeCount;
  settings: ChangeCount;
  internal_error_rules: ChangeCount;
}

export interface ImportPreviewResponse {
  dry_run: boolean;
  changes: ImportChanges;
  warnings: string[];
  credential_reauthentication_requirements: CredentialReauthenticationRequirement[];
  rule_set_revision: string;
  rule_set_etag: string;
}

export interface CredentialReauthenticationRequirement {
  credential_session_id: string;
  name: string;
}

export interface AppliedCount {
  added: number;
  updated: number;
  deleted: number;
}

export interface ImportedCounts {
  providers: AppliedCount;
  credential_sessions: AppliedCount;
  groups: AppliedCount;
  routing_policies: AppliedCount;
  settings: AppliedCount;
  internal_error_rules: AppliedCount;
}

export interface ImportResult {
  success: boolean;
  applied: ImportedCounts;
  credential_reauthentication_requirements: CredentialReauthenticationRequirement[];
  rule_set_revision: string;
  rule_set_etag: string;
}
