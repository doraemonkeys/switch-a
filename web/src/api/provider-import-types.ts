import type { BackoffPolicy } from "./retry-policy-types";

export type ProviderImportCandidateStatus =
  "ready" | "existing" | "duplicate" | "invalid" | "unsupported";

export interface ProviderImportIssue {
  code: string;
  message: string;
}

export type ProviderImportConflictKind =
  | "duplicate_provider_id"
  | "duplicate_account_binding"
  | "provider_already_exists"
  | "provider_not_found"
  | "account_binding_mismatch"
  | "account_already_bound"
  | "group_not_found"
  | "credential_version_mismatch";

export interface ProviderImportConflictDetail {
  candidate_id: string;
  conflicting_candidate_id?: string;
  kind: ProviderImportConflictKind;
  provider_id?: string;
  conflicting_provider_id?: string;
  account_id?: string;
  group_id?: string;
  expected_credential_version?: number;
  current_credential_version?: number;
}

export type ProviderImportMappingNote = ProviderImportIssue;

export interface ProviderImportCandidate {
  candidate_id: string;
  source_index: number;
  status: ProviderImportCandidateStatus;
  name: string;
  provider_id: string;
  email?: string;
  account_id?: string;
  plan_type?: string;
  expires_at?: string;
  priority: number;
  concurrency: number;
  existing_provider_id?: string;
  existing_provider_name?: string;
  default_selected: boolean;
  message?: string;
  warnings: ProviderImportIssue[];
}

export interface ProviderImportSummary {
  total: number;
  ready: number;
  existing: number;
  duplicate: number;
  invalid: number;
  unsupported: number;
}

export interface ProviderImportCreateDefaults {
  weight: number;
  max_retries: number;
  backoff: BackoffPolicy;
}

export interface ProviderImportPreview {
  import_id: string;
  expires_at: string;
  create_defaults: ProviderImportCreateDefaults;
  items: ProviderImportCandidate[];
  summary: ProviderImportSummary;
  warnings: ProviderImportMappingNote[];
}

export type ProviderImportCommitAction = "create" | "update";

export interface ProviderImportCreateCommitItem {
  candidate_id: string;
  action: "create";
  provider_id: string;
  name: string;
  priority: number;
  weight: number;
  concurrency: number;
  max_retries: number;
  backoff: BackoffPolicy;
}

export interface ProviderImportUpdateCommitItem {
  candidate_id: string;
  action: "update";
  provider_id: string;
}

export type ProviderImportCommitItem =
  ProviderImportCreateCommitItem | ProviderImportUpdateCommitItem;

export interface ProviderImportCommitRequest {
  group_id: string | null;
  items: ProviderImportCommitItem[];
}

export type ProviderImportCommitOutcome = "created" | "updated";

export interface ProviderImportCommitResultItem {
  candidate_id: string;
  outcome: ProviderImportCommitOutcome;
  provider_id: string;
  name?: string;
}

export interface ProviderImportCommitSummary {
  created: number;
  updated: number;
  skipped: number;
}

export interface ProviderImportCommitResult {
  import_id: string;
  summary: ProviderImportCommitSummary;
  items: ProviderImportCommitResultItem[];
}
