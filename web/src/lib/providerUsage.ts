import type {
  ProviderAuthProfile,
  ProviderUsageSnapshot,
  ProviderUsageWindow,
} from "../api/types";

const KNOWN_PLAN_LABELS: Record<string, string> = {
  business: "Business",
  enterprise: "Enterprise",
  free: "Free",
  plus: "Plus",
  pro: "Pro",
  team: "Team",
};

function normalizePlanType(planType?: string | null): string {
  return planType?.trim().toLowerCase() ?? "";
}

function formatPercentValue(value: number): string {
  const rounded = Number.isInteger(value) ? value.toFixed(0) : value.toFixed(1);
  return rounded.replace(/\.0$/, "");
}

export function resolveProviderUsage(
  authProfile?: ProviderAuthProfile | null,
): ProviderUsageSnapshot | null {
  return authProfile?.usage ?? null;
}

export function resolveProviderPlanType(
  authProfile?: ProviderAuthProfile | null,
): string | null {
  const usagePlan = authProfile?.usage?.plan_type?.trim();
  if (usagePlan) {
    return usagePlan;
  }

  const directPlan = authProfile?.plan_type?.trim();
  return directPlan || null;
}

export function formatProviderPlanType(planType?: string | null): string {
  const normalized = normalizePlanType(planType);
  if (!normalized) {
    return "Unknown";
  }
  return KNOWN_PLAN_LABELS[normalized] ?? planType?.trim() ?? "Unknown";
}

export function formatProviderUsagePercent(
  window?: ProviderUsageWindow | null,
): string {
  if (!window) {
    return "—";
  }
  return `${formatPercentValue(window.used_percent)}%`;
}

export function formatProviderUsageWindowSummary(
  label: string,
  window?: ProviderUsageWindow | null,
): string {
  if (!window) {
    return `${label} —`;
  }
  return `${label} ${formatProviderUsagePercent(window)} used`;
}

export function formatProviderResetAt(resetAt?: string | null): string {
  if (!resetAt) {
    return "—";
  }
  return new Date(resetAt).toLocaleString();
}
