import {
  SUCCESS_RATE_THRESHOLDS,
  ERROR_COUNT_THRESHOLDS,
} from "../../config/constants";

// Type for stat card variants
export type StatVariantValue = "success" | "warning" | "danger";
export type StatVariant = StatVariantValue | undefined;

// Helper to determine aria-sort value for sortable table headers
export type AriaSortValue = "ascending" | "descending" | "none";

// Helper functions for determining stat variants
export function getSuccessRateVariant(rate: number | undefined): StatVariant {
  if (rate === undefined) return undefined;
  if (rate >= SUCCESS_RATE_THRESHOLDS.SUCCESS) return "success";
  if (rate >= SUCCESS_RATE_THRESHOLDS.WARNING) return "warning";
  return "danger";
}

export function getErrorCountVariant(count: number | undefined): StatVariant {
  if (count === undefined) return undefined;
  if (count === 0) return "success";
  if (count < ERROR_COUNT_THRESHOLDS.WARNING_MAX) return "warning";
  return "danger";
}

export function getStatVariantClass(variant: StatVariant): string {
  if (variant === "success") return "text-success";
  if (variant === "warning") return "text-warning";
  if (variant === "danger") return "text-danger";
  return "text-text-primary";
}

export function getAriaSortValue(
  field: string,
  currentSortBy: string,
  currentSortOrder: string,
): AriaSortValue {
  if (currentSortBy !== field) return "none";
  return currentSortOrder === "asc" ? "ascending" : "descending";
}
