import type {
  APITypeInput,
  CredentialSessionKind,
  ProviderInput,
} from "../../api";

export type ProviderCredentialMode = CredentialSessionKind | "mixed";

export interface ProviderAPITypeDraft extends APITypeInput {
  client_key: string;
  /** A write-only replacement secret; it is materialized as a new session. */
  api_key: string;
}

export interface ProviderFormData extends Omit<ProviderInput, "api_types"> {
  api_types: ProviderAPITypeDraft[];
  credential_mode: ProviderCredentialMode;
  /** Explicit bindings stay authoritative; this write-only secret only fills unbound routes. */
  new_shared_api_key: string;
}

let nextClientKey = 0;
/** Generate a unique client-side key for stable React reconciliation. */
export function generateClientKey(): string {
  return `apitype-${++nextClientKey}`;
}

export const PROVIDER_STATUS_TYPES = [
  "healthy",
  "unhealthy",
  "pending-recovery",
  "disabled",
] as const;

export type ProviderStatusType = (typeof PROVIDER_STATUS_TYPES)[number];

// Visibility is broader than runtime status: "enabled" intentionally includes
// healthy, circuit-open, and probing providers.
export const PROVIDER_VISIBILITY_FILTERS = [
  "enabled",
  ...PROVIDER_STATUS_TYPES,
] as const;

export type ProviderVisibilityFilter =
  "" | (typeof PROVIDER_VISIBILITY_FILTERS)[number];

/**
 * Determines provider status based on enabled state and health info.
 */
export function getProviderStatus(
  enabled: boolean,
  available?: boolean,
  disabledUntil?: string | null,
): ProviderStatusType {
  if (!enabled) return "disabled";
  // Treat both `undefined` (no health info yet) and `true` as healthy.
  // Only when `available === false` do we check for unhealthy/pending-recovery.
  if (available !== false) return "healthy";

  // If available is false:
  if (disabledUntil) {
    const now = new Date();
    const until = new Date(disabledUntil);
    if (until <= now) {
      return "pending-recovery";
    }
  }
  return "unhealthy";
}

export const statusDotClass: Record<ProviderStatusType, string> = {
  healthy: "bg-success",
  unhealthy: "bg-danger",
  "pending-recovery": "bg-warning",
  disabled: "bg-text-muted",
};

export const statusBadgeClass: Record<ProviderStatusType, string> = {
  healthy: "bg-success-light text-success",
  unhealthy: "bg-danger-light text-danger",
  "pending-recovery": "bg-warning-light text-warning",
  disabled: "bg-bg-tertiary text-text-secondary",
};

export const statusLabel: Record<ProviderStatusType, string> = {
  healthy: "Healthy",
  unhealthy: "Circuit Open",
  "pending-recovery": "Probing",
  disabled: "Disabled",
};
