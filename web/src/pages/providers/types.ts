export type ProviderStatusType =
  | "healthy"
  | "unhealthy"
  | "pending-recovery"
  | "disabled";

export type StatusFilter =
  | ""
  | "healthy"
  | "unhealthy"
  | "pending-recovery"
  | "disabled";

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
  healthy: "bg-success-light text-success-dark",
  unhealthy: "bg-danger-light text-danger-dark",
  "pending-recovery": "bg-warning-light text-warning-dark",
  disabled: "bg-gray-100 text-gray-600",
};

export const statusLabel: Record<ProviderStatusType, string> = {
  healthy: "Healthy",
  unhealthy: "Circuit Open",
  "pending-recovery": "Pending",
  disabled: "Disabled",
};
