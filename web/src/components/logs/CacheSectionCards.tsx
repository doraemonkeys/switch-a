import type { ReactNode } from "react";
import { formatTokenLocale, calculateEffectiveCost } from "./utils";

// =============================================================================
// Token Card Components
// =============================================================================

// --- TokenStatCard: Generic token stat card for main token display ---

interface TokenStatCardProps {
  icon: ReactNode;
  label: string;
  value: string;
  iconColor?: string;
}

export function TokenStatCard({
  icon,
  label,
  value,
  iconColor = "text-text-muted",
}: TokenStatCardProps) {
  return (
    <div className="px-2.5 py-2 rounded-md bg-bg-secondary border border-border-light">
      <div className="flex items-center gap-1.5 mb-1">
        <span className={iconColor}>{icon}</span>
        <span className="text-[11px] text-text-muted uppercase tracking-wide">
          {label}
        </span>
      </div>
      <p
        className={`text-sm font-mono truncate ${
          value === "—" ? "text-text-muted" : "text-text-primary"
        }`}
      >
        {value}
      </p>
    </div>
  );
}

// --- CacheStatCard: Cache-specific stat card with hint support ---

interface CacheStatCardProps {
  icon: ReactNode;
  label: string;
  value: string;
  hint?: string;
  hintColor?: string;
}

export function CacheStatCard({
  icon,
  label,
  value,
  hint,
  hintColor = "text-text-muted",
}: CacheStatCardProps) {
  return (
    <div className="px-2.5 py-2 rounded-md bg-bg-secondary/50 border border-border-light">
      <div className="flex items-center gap-1.5 mb-1">
        <span className="text-text-muted">{icon}</span>
        <span className="text-[11px] text-text-muted uppercase tracking-wide">
          {label}
        </span>
      </div>
      <p
        className={`text-sm font-mono ${
          value === "—" ? "text-text-muted" : "text-text-primary"
        }`}
      >
        {value}
      </p>
      {hint && <p className={`text-[10px] mt-0.5 ${hintColor}`}>{hint}</p>}
    </div>
  );
}

interface EffectiveCostDisplayProps {
  promptTokens: number;
  cacheReadTokens: number;
  cacheCreationTokens: number;
}

export function EffectiveCostDisplay({
  promptTokens,
  cacheReadTokens,
  cacheCreationTokens,
}: EffectiveCostDisplayProps) {
  const { billable, uncached } = calculateEffectiveCost(
    promptTokens,
    cacheReadTokens,
    cacheCreationTokens,
  );

  return (
    <div className="pt-2 border-t border-emerald-200/50 dark:border-emerald-800/30">
      <div className="flex items-start gap-2">
        {/* Coins Icon */}
        <svg
          className="w-4 h-4 text-amber-500 mt-0.5 shrink-0"
          fill="none"
          stroke="currentColor"
          strokeWidth={2}
          viewBox="0 0 24 24"
        >
          <circle cx="8" cy="8" r="6" />
          <path d="M18.09 10.37A6 6 0 1 1 10.34 18" />
          <path d="M7 6h1v4" />
          <path d="M16.71 13.88l.7.71-2.82 2.82" />
        </svg>
        <div className="text-xs space-y-0.5">
          <p className="text-text-primary font-medium">
            Effective Cost:{" "}
            <span className="font-mono text-amber-600 dark:text-amber-400">
              ~{formatTokenLocale(billable)}
            </span>{" "}
            billable input tokens
          </p>
          <p className="text-text-muted">
            ({formatTokenLocale(cacheReadTokens)}×0.1
            {cacheCreationTokens > 0 && (
              <> + {formatTokenLocale(cacheCreationTokens)}×1.25</>
            )}
            {uncached > 0 && <> + {formatTokenLocale(uncached)} uncached</>})
          </p>
        </div>
      </div>
    </div>
  );
}

// =============================================================================
// Cache Section Component
// =============================================================================

interface CacheSectionProps {
  cacheHitRate: number | null;
  hasCacheRead: boolean;
  hasCacheCreation: boolean;
  cacheReadTokens: number;
  cacheCreationTokens: number;
  promptTokens: number;
}

export function CacheSection({
  cacheHitRate,
  hasCacheRead,
  hasCacheCreation,
  cacheReadTokens,
  cacheCreationTokens,
  promptTokens,
}: CacheSectionProps) {
  return (
    <div className="rounded-lg border border-emerald-200 dark:border-emerald-800/50 bg-emerald-50/50 dark:bg-emerald-900/10 p-3 space-y-3">
      <div className="flex items-center gap-2 text-sm font-medium text-emerald-700 dark:text-emerald-400">
        {/* Target Icon */}
        <svg
          className="w-4 h-4"
          fill="none"
          stroke="currentColor"
          strokeWidth={2}
          viewBox="0 0 24 24"
        >
          <circle cx="12" cy="12" r="10" />
          <circle cx="12" cy="12" r="6" />
          <circle cx="12" cy="12" r="2" />
        </svg>
        Claude Cache
      </div>

      {/* Cache Hit Rate Progress Bar */}
      {cacheHitRate !== null && (
        <div className="space-y-1.5">
          <div className="flex items-center justify-between text-xs">
            <span className="text-text-secondary">Cache Hit</span>
            <span className="font-mono text-emerald-600 dark:text-emerald-400">
              {cacheHitRate}%
            </span>
          </div>
          <div className="w-full h-2 bg-bg-secondary rounded-full overflow-hidden">
            <div
              className="h-full bg-emerald-500 rounded-full transition-all duration-300"
              style={{ width: `${cacheHitRate}%` }}
            />
          </div>
        </div>
      )}

      {/* Cache Read/Write Stats */}
      <div className="grid grid-cols-2 gap-2">
        {/* Cache Read */}
        <CacheStatCard
          icon={
            <svg
              className="w-3.5 h-3.5"
              fill="none"
              stroke="currentColor"
              strokeWidth={2}
              viewBox="0 0 24 24"
            >
              <path
                strokeLinecap="round"
                strokeLinejoin="round"
                d="M12 6.253v13m0-13C10.832 5.477 9.246 5 7.5 5S4.168 5.477 3 6.253v13C4.168 18.477 5.754 18 7.5 18s3.332.477 4.5 1.253m0-13C13.168 5.477 14.754 5 16.5 5c1.747 0 3.332.477 4.5 1.253v13C19.832 18.477 18.247 18 16.5 18c-1.746 0-3.332.477-4.5 1.253"
              />
            </svg>
          }
          label="Read"
          value={
            hasCacheRead ? `${formatTokenLocale(cacheReadTokens)} tokens` : "—"
          }
          hint={hasCacheRead ? "(billed 10%)" : undefined}
          hintColor="text-emerald-600 dark:text-emerald-400"
        />

        {/* Cache Write */}
        <CacheStatCard
          icon={
            <svg
              className="w-3.5 h-3.5"
              fill="none"
              stroke="currentColor"
              strokeWidth={2}
              viewBox="0 0 24 24"
            >
              <path
                strokeLinecap="round"
                strokeLinejoin="round"
                d="M15.232 5.232l3.536 3.536m-2.036-5.036a2.5 2.5 0 113.536 3.536L6.5 21.036H3v-3.572L16.732 3.732z"
              />
            </svg>
          }
          label="Write"
          value={
            hasCacheCreation
              ? `${formatTokenLocale(cacheCreationTokens)} tokens`
              : "—"
          }
          hint={hasCacheCreation ? "(billed 125%)" : undefined}
          hintColor="text-amber-600 dark:text-amber-400"
        />
      </div>

      {/* Effective Cost Calculation */}
      {hasCacheRead && (
        <EffectiveCostDisplay
          promptTokens={promptTokens}
          cacheReadTokens={cacheReadTokens}
          cacheCreationTokens={cacheCreationTokens}
        />
      )}
    </div>
  );
}
