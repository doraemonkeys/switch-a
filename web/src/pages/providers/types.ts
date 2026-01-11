export type ProviderStatusType = "healthy" | "unhealthy" | "disabled";
export type StatusFilter = "" | "healthy" | "unhealthy" | "disabled";

/**
 * Determines provider status based on enabled state and optional health availability.
 * @param enabled - Whether the provider is enabled
 * @param available - Optional health availability (from ProviderStatus.health.available)
 * @returns Provider status: "disabled" if not enabled, "unhealthy" if available is false, "healthy" otherwise
 */
export function getProviderStatus(
  enabled: boolean,
  available?: boolean,
): ProviderStatusType {
  if (!enabled) return "disabled";
  return available !== false ? "healthy" : "unhealthy";
}

export const statusDotClass: Record<ProviderStatusType, string> = {
  healthy: "bg-success",
  unhealthy: "bg-danger",
  disabled: "bg-text-muted",
};

export const statusBadgeClass: Record<ProviderStatusType, string> = {
  healthy: "bg-success-light text-success-dark",
  unhealthy: "bg-danger-light text-danger-dark",
  disabled: "bg-gray-100 text-gray-600",
};

export const statusLabel: Record<ProviderStatusType, string> = {
  healthy: "Healthy",
  unhealthy: "Unhealthy",
  disabled: "Disabled",
};
