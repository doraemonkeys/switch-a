import type { ReactNode } from "react";
import type { RequestAttemptSwitchMode } from "@/api/types";
import { BADGE_STYLES } from "@/lib/utils";
import type { AttemptPresentation } from "../model/attempt-presentation";
import {
  formatFullTimestamp,
  formatProviderLabel,
  formatSeedAge,
  formatTime,
  formatTimeOffset,
  getSwitchModeLabel,
} from "../model/attempt-presentation";

const ATTRIBUTION_NOTE =
  "RequestLog.provider_id attributes the final client-visible outcome to this provider.";
const PHASE_BADGE_CLASS =
  "inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-slate-100 text-slate-700 dark:bg-slate-900/30 dark:text-slate-300";
const TONE_BADGES = {
  info: BADGE_STYLES.INFO,
  warning:
    "inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300",
  error:
    "inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-300",
} as const;
const SUPPLEMENTAL_BADGE_CLASS =
  "inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-slate-100 text-slate-700 dark:bg-slate-900/30 dark:text-slate-300";
const CONTINUITY_PANEL_CLASS =
  "mt-2 rounded-lg border border-sky-200 bg-sky-50/70 px-3 py-2 text-xs text-sky-800 dark:border-sky-800 dark:bg-sky-950/20 dark:text-sky-200";

export function AttemptHeader({
  isWebSocket,
  displayAttemptNumber,
  latencyMs,
  statusBadge,
}: {
  isWebSocket: boolean;
  displayAttemptNumber: number;
  latencyMs: number;
  statusBadge: ReactNode;
}) {
  return (
    <div className="flex items-center justify-between gap-4">
      <div className="flex items-center gap-2">
        <h3 className="text-sm font-medium text-text-primary">
          {`${isWebSocket ? "Provider Attempt" : "Attempt"} ${displayAttemptNumber}`}
        </h3>
        {statusBadge}
      </div>
      <span className="text-sm text-text-secondary font-mono">
        {latencyMs}ms
      </span>
    </div>
  );
}

export function AttemptProviderMeta({
  providerName,
  providerID,
  isAttributedAttempt,
  createdAt,
  isFirst,
  firstAttemptTime,
}: {
  providerName?: string;
  providerID: string;
  isAttributedAttempt: boolean;
  createdAt?: string;
  isFirst: boolean;
  firstAttemptTime?: string;
}) {
  return (
    <>
      <div className="flex items-center justify-between mt-1 gap-3">
        <div className="flex items-center gap-2 flex-wrap">
          <p className="text-sm text-text-secondary">
            Provider: {providerName || providerID}
          </p>
          {isAttributedAttempt && (
            <span
              className={`inline-flex items-center px-2 py-0.5 rounded text-xs font-medium ${BADGE_STYLES.INFO}`}
              title={ATTRIBUTION_NOTE}
            >
              Outcome owner
            </span>
          )}
        </div>
        {createdAt && (
          <time
            className="text-xs text-text-muted font-mono flex items-center gap-1.5"
            title={formatFullTimestamp(createdAt)}
            dateTime={createdAt}
          >
            <svg
              aria-hidden="true"
              className="w-3 h-3"
              fill="none"
              stroke="currentColor"
              viewBox="0 0 24 24"
            >
              <path
                strokeLinecap="round"
                strokeLinejoin="round"
                strokeWidth={2}
                d="M12 8v4l3 3m6-3a9 9 0 11-18 0 9 9 0 0118 0z"
              />
            </svg>
            {isFirst || !firstAttemptTime ? (
              formatTime(createdAt)
            ) : (
              <span className="text-amber-600 dark:text-amber-400">
                {formatTimeOffset(createdAt, firstAttemptTime)}
              </span>
            )}
          </time>
        )}
      </div>
      {isAttributedAttempt && (
        <p className="mt-2 text-xs text-blue-700 dark:text-blue-300">
          {ATTRIBUTION_NOTE}
        </p>
      )}
    </>
  );
}

export function AttemptLifecycleBadges({
  phaseLabel,
  outcome,
}: Pick<AttemptPresentation, "phaseLabel" | "outcome">) {
  if (!phaseLabel && !outcome) {
    return null;
  }
  return (
    <div className="mt-2 flex flex-wrap items-center gap-2">
      {phaseLabel && (
        <span className={PHASE_BADGE_CLASS}>Phase: {phaseLabel}</span>
      )}
      {outcome && (
        <span className={TONE_BADGES[outcome.tone]}>{outcome.text}</span>
      )}
    </div>
  );
}

function switchModeClass(mode?: RequestAttemptSwitchMode | null): string {
  if (mode === "replacement") {
    return "inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-sky-100 text-sky-800 dark:bg-sky-950/30 dark:text-sky-300";
  }
  if (mode === "failover") {
    return "inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-amber-100 text-amber-800 dark:bg-amber-950/30 dark:text-amber-300";
  }
  return SUPPLEMENTAL_BADGE_CLASS;
}

export function AttemptSelectionMetadata({
  switchMode,
  providerAttempt,
  providerSwitchCount,
  continuitySeeded,
  continuityOriginProviderID,
  continuityOriginProviderName,
  continuitySeedAgeMs,
}: {
  switchMode?: RequestAttemptSwitchMode | null;
  providerAttempt?: number;
  providerSwitchCount?: number;
  continuitySeeded?: boolean;
  continuityOriginProviderID?: string;
  continuityOriginProviderName?: string;
  continuitySeedAgeMs?: number | null;
}) {
  const switchModeLabel = getSwitchModeLabel(switchMode);
  const origin = formatProviderLabel(
    continuityOriginProviderID,
    continuityOriginProviderName,
  );
  const seedAge = formatSeedAge(continuitySeedAgeMs);
  const hasContinuity =
    Boolean(continuitySeeded) || Boolean(origin) || seedAge !== null;
  if (
    !switchModeLabel &&
    (providerAttempt ?? 0) <= 0 &&
    (providerSwitchCount ?? 0) <= 0 &&
    !hasContinuity
  ) {
    return null;
  }
  return (
    <>
      <div className="mt-2 flex flex-wrap items-center gap-2">
        {switchModeLabel && (
          <span className={switchModeClass(switchMode)}>
            Mode: {switchModeLabel}
          </span>
        )}
        {(providerAttempt ?? 0) > 0 && (
          <span className={SUPPLEMENTAL_BADGE_CLASS}>
            Provider attempt {providerAttempt}
          </span>
        )}
        {(providerSwitchCount ?? 0) > 0 && (
          <span className={SUPPLEMENTAL_BADGE_CLASS}>
            Provider switches {providerSwitchCount}
          </span>
        )}
      </div>
      {hasContinuity && (
        <div className={CONTINUITY_PANEL_CLASS}>
          <p className="font-medium">Continuity provenance</p>
          <p className="mt-1">
            {continuitySeeded
              ? "Heuristic seed matched"
              : "Continuity metadata recorded"}
            {origin ? ` from ${origin}` : ""}
            {seedAge ? ` · seed age ${seedAge}` : ""}
          </p>
        </div>
      )}
    </>
  );
}
