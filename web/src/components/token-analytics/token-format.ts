import type { StatsGranularity, StatsPeriod } from "../../api/types";

export {
  DEFAULT_GRANULARITY_BY_PERIOD,
  GRANULARITY_OPTIONS_BY_PERIOD,
  isGranularityAllowed,
} from "../../features/analytics-window/analytics-window";

export type TokenValue = string | number | bigint | undefined | null;

/**
 * Safely parses any token value representation into a BigInt to prevent
 * floating-point inaccuracies or safe-integer overflows during aggregations.
 */
export function parseTokenBigInt(val: TokenValue): bigint {
  if (val === undefined || val === null) {
    return 0n;
  }
  if (typeof val === "bigint") {
    return val;
  }
  if (typeof val === "number") {
    if (!Number.isFinite(val) || val <= 0) return 0n;
    return BigInt(Math.floor(val));
  }
  const trimmed = val.trim();
  if (!trimmed || !/^\d+$/.test(trimmed)) {
    return 0n;
  }
  try {
    return BigInt(trimmed);
  } catch {
    return 0n;
  }
}

/**
 * Formats a token quantity into a compact readable string (e.g. 12.45M, 820.4K, 420K, 0).
 */
export function formatTokenCompact(val: TokenValue): string {
  const bi = parseTokenBigInt(val);
  if (bi === 0n) return "0";

  const num = Number(bi);
  if (!Number.isFinite(num)) {
    return bi.toString();
  }

  if (num >= 1_000_000_000) {
    const formatted = (num / 1_000_000_000).toFixed(2);
    return `${cleanTrailingZeros(formatted)}B`;
  }
  if (num >= 1_000_000) {
    const formatted = (num / 1_000_000).toFixed(2);
    return `${cleanTrailingZeros(formatted)}M`;
  }
  if (num >= 1_000) {
    const formatted = (num / 1_000).toFixed(1);
    return `${cleanTrailingZeros(formatted)}K`;
  }
  return num.toLocaleString();
}

/**
 * Formats a token quantity with full thousand separators (e.g. 12,451,200).
 */
export function formatTokenLocale(val: TokenValue): string {
  const bi = parseTokenBigInt(val);
  return bi.toLocaleString();
}

function cleanTrailingZeros(val: string): string {
  return val.replace(/\.00?$/, "").replace(/(\.[1-9])0$/, "$1");
}

/**
 * Computes the percentage of a part relative to a total, returning a float between 0 and 100.
 */
export function calculateTokenPercent(
  part: TokenValue,
  total: TokenValue,
): number {
  const p = parseTokenBigInt(part);
  const t = parseTokenBigInt(total);
  if (t <= 0n || p <= 0n) return 0;
  if (p >= t) return 100;

  // Use BigInt fixed point multiplication to maintain accuracy before converting to number
  const basisPoints = (p * 10000n) / t;
  return Number(basisPoints) / 100;
}

export const PERIOD_LABELS: Record<StatsPeriod, string> = {
  "24h": "Last 24 hours",
  "7d": "Last 7 days",
  "30d": "Last 30 days",
  all: "All time",
};

export const PERIOD_OPTIONS: Array<{ value: StatsPeriod; label: string }> = [
  { value: "24h", label: PERIOD_LABELS["24h"] },
  { value: "7d", label: PERIOD_LABELS["7d"] },
  { value: "30d", label: PERIOD_LABELS["30d"] },
  { value: "all", label: PERIOD_LABELS.all },
];

export const GRANULARITY_LABELS: Record<StatsGranularity, string> = {
  "5m": "5m bucket",
  "15m": "15m bucket",
  "1h": "1h bucket",
  "6h": "6h bucket",
  "1d": "1d bucket",
};

export interface MetricColorConfig {
  name: string;
  hex: string;
  bgClass: string;
  textClass: string;
  borderClass: string;
  badgeBgClass: string;
}

export const TOKEN_SEMANTICS: Record<string, MetricColorConfig> = {
  total: {
    name: "Total Tokens",
    hex: "#6366f1",
    bgClass: "bg-indigo-500",
    textClass: "text-indigo-600 dark:text-indigo-400",
    borderClass: "border-indigo-500",
    badgeBgClass:
      "bg-indigo-50 dark:bg-indigo-950/40 text-indigo-600 dark:text-indigo-400",
  },
  fresh: {
    name: "Fresh Input",
    hex: "#0284c7",
    bgClass: "bg-sky-500",
    textClass: "text-sky-600 dark:text-sky-400",
    borderClass: "border-sky-500",
    badgeBgClass: "bg-sky-50 dark:bg-sky-950/40 text-sky-600 dark:text-sky-400",
  },
  cacheRead: {
    name: "Cache Read",
    hex: "#10b981",
    bgClass: "bg-emerald-500",
    textClass: "text-emerald-600 dark:text-emerald-400",
    borderClass: "border-emerald-500",
    badgeBgClass:
      "bg-emerald-50 dark:bg-emerald-950/40 text-emerald-600 dark:text-emerald-400",
  },
  cacheCreation: {
    name: "Cache Creation",
    hex: "#d97706",
    bgClass: "bg-amber-500",
    textClass: "text-amber-600 dark:text-amber-400",
    borderClass: "border-amber-500",
    badgeBgClass:
      "bg-amber-50 dark:bg-amber-950/40 text-amber-600 dark:text-amber-400",
  },
  unclassifiedInput: {
    name: "Unclassified Input",
    hex: "#94a3b8",
    bgClass: "bg-slate-400",
    textClass: "text-slate-500 dark:text-slate-400",
    borderClass: "border-slate-400",
    badgeBgClass:
      "bg-slate-100 dark:bg-slate-800 text-slate-600 dark:text-slate-400",
  },
  standardOutput: {
    name: "Standard Output",
    hex: "#8b5cf6",
    bgClass: "bg-violet-500",
    textClass: "text-violet-600 dark:text-violet-400",
    borderClass: "border-violet-500",
    badgeBgClass:
      "bg-violet-50 dark:bg-violet-950/40 text-violet-600 dark:text-violet-400",
  },
  reasoning: {
    name: "Reasoning CoT",
    hex: "#d946ef",
    bgClass: "bg-fuchsia-500",
    textClass: "text-fuchsia-600 dark:text-fuchsia-400",
    borderClass: "border-fuchsia-500",
    badgeBgClass:
      "bg-fuchsia-50 dark:bg-fuchsia-950/40 text-fuchsia-600 dark:text-fuchsia-400",
  },
  unclassifiedOutput: {
    name: "Unclassified Output",
    hex: "#a1a1aa",
    bgClass: "bg-zinc-400",
    textClass: "text-zinc-500 dark:text-zinc-400",
    borderClass: "border-zinc-400",
    badgeBgClass:
      "bg-zinc-100 dark:bg-zinc-800 text-zinc-600 dark:text-zinc-400",
  },
  coverage: {
    name: "Coverage & Quality",
    hex: "#64748b",
    bgClass: "bg-slate-500",
    textClass: "text-slate-600 dark:text-slate-400",
    borderClass: "border-slate-500",
    badgeBgClass:
      "bg-slate-100 dark:bg-slate-800 text-slate-600 dark:text-slate-400",
  },
};
