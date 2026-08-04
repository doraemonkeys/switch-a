import type { ReactNode } from "react";
import type { RequestAttempt } from "@/api/types";
import { BADGE_STYLES, getStatusCodeBadgeClass } from "@/lib/utils";
import {
  buildAttemptPresentation,
  formatRequestBody,
  type AttemptPresentation,
  type AttemptVisualState,
} from "../model/attempt-presentation";
import {
  AttemptHeader,
  AttemptLifecycleBadges,
  AttemptProviderMeta,
  AttemptSelectionMetadata,
} from "./AttemptMetadata";
import { ErrorBodyParser } from "./ErrorBodyParser";
import { RequestEvidenceViewer } from "./RequestEvidenceViewer";

const CARD_CLASSES: Record<AttemptVisualState | "success_last", string> = {
  success_last:
    "border-green-200 bg-green-50/50 dark:border-green-800 dark:bg-green-900/10",
  success: "border-border-light bg-bg-tertiary",
  semantic:
    "border-amber-200 bg-amber-50/50 dark:border-amber-800 dark:bg-amber-900/10",
  failure: "border-red-200 bg-red-50/50 dark:border-red-800 dark:bg-red-900/10",
  upgrade:
    "border-blue-100 bg-blue-50/30 dark:border-blue-900 dark:bg-blue-900/5",
  warning: "border-red-200 bg-red-50/50 dark:border-red-800 dark:bg-red-900/10",
  no_response:
    "border-red-200 bg-red-50/50 dark:border-red-800 dark:bg-red-900/10",
  neutral: "border-border-light bg-bg-tertiary",
};
const DOT_CLASSES: Record<AttemptVisualState, string> = {
  success: "bg-green-500",
  semantic: "bg-amber-500",
  failure: "bg-red-500",
  upgrade: "bg-sky-500",
  warning: "bg-amber-500",
  no_response: "bg-gray-400",
  neutral: "bg-gray-400",
};
const WEBSOCKET_SUCCESS_BADGE_CLASS =
  "inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-green-100 text-green-800 dark:bg-green-900/30 dark:text-green-300";

function StatusBadge({
  attempt,
  presentation,
}: {
  attempt: RequestAttempt;
  presentation: AttemptPresentation;
}): ReactNode {
  if (presentation.isNoResponse) {
    return (
      <span className="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-gray-100 text-gray-800 dark:bg-gray-900/30 dark:text-gray-300">
        No Response
      </span>
    );
  }
  if (presentation.isWebSocketUpgrade) {
    return (
      <span
        className={
          presentation.isSuccess
            ? WEBSOCKET_SUCCESS_BADGE_CLASS
            : BADGE_STYLES.INFO
        }
      >
        101 Upgrade
      </span>
    );
  }
  if (attempt.status_code <= 0) {
    return null;
  }
  return (
    <span
      className={`inline-flex items-center px-2 py-0.5 rounded text-xs font-medium ${getStatusCodeBadgeClass(attempt.status_code)}`}
    >
      {attempt.status_code}
    </span>
  );
}

export interface AttemptNodeProps {
  attempt: RequestAttempt;
  isFirst: boolean;
  isLast: boolean;
  providerName?: string;
  userAgent?: string;
  firstAttemptTime?: string;
  displayAttemptNumber: number;
  isWebSocket: boolean;
  isAttributedAttempt: boolean;
  continuityOriginProviderName?: string;
}

export function AttemptNode({
  attempt,
  isFirst,
  isLast,
  providerName,
  userAgent,
  firstAttemptTime,
  displayAttemptNumber,
  isWebSocket,
  isAttributedAttempt,
  continuityOriginProviderName,
}: AttemptNodeProps) {
  const presentation = buildAttemptPresentation(attempt, isWebSocket);
  const cardState =
    isLast && presentation.isSuccess
      ? "success_last"
      : presentation.visualState;
  return (
    <li className="relative pl-8">
      <span
        aria-hidden="true"
        className={`absolute left-1.5 top-1.5 w-3 h-3 rounded-full ring-2 ring-bg-secondary ${DOT_CLASSES[presentation.visualState]}`}
      />
      <article
        className={`p-3 rounded-lg border ${CARD_CLASSES[cardState]}`}
        aria-label={`${isWebSocket ? "Provider attempt" : "Attempt"} ${displayAttemptNumber}`}
      >
        <AttemptHeader
          isWebSocket={isWebSocket}
          displayAttemptNumber={displayAttemptNumber}
          latencyMs={attempt.latency_ms}
          statusBadge={
            <StatusBadge attempt={attempt} presentation={presentation} />
          }
        />
        <AttemptProviderMeta
          providerName={providerName}
          providerID={attempt.provider_id}
          isAttributedAttempt={isAttributedAttempt}
          createdAt={attempt.created_at}
          isFirst={isFirst}
          firstAttemptTime={firstAttemptTime}
        />
        <AttemptSelectionMetadata
          switchMode={attempt.switch_mode}
          providerAttempt={attempt.provider_attempt}
          providerSwitchCount={attempt.provider_switch_count}
          continuitySeeded={attempt.continuity_seeded}
          continuityOriginProviderID={attempt.continuity_origin_provider_id}
          continuityOriginProviderName={continuityOriginProviderName}
          continuitySeedAgeMs={attempt.continuity_seed_age_ms}
        />
        <AttemptLifecycleBadges
          phaseLabel={presentation.phaseLabel}
          outcome={presentation.outcome}
        />

        {presentation.hasError && (
          <div className="mt-2 p-2 rounded bg-red-100/50 dark:bg-red-900/20 border border-red-200 dark:border-red-800">
            <p className="text-xs text-red-700 dark:text-red-300 font-mono break-words">
              {attempt.error}
            </p>
          </div>
        )}

        {attempt.attempt_evidence_json && (
          <div className="mt-3">
            <h4 className="text-xs text-text-muted mb-1.5">
              Structured Evidence
            </h4>
            <RequestEvidenceViewer
              evidenceJson={attempt.attempt_evidence_json}
            />
          </div>
        )}

        {presentation.hasBodySnippet && (
          <div className="mt-2">
            <p className="text-xs text-text-muted mb-1.5">Response Body:</p>
            <ErrorBodyParser
              body={attempt.body_snippet!}
              statusCode={attempt.status_code}
              userAgent={userAgent}
            />
          </div>
        )}

        {presentation.hasRequestBodySnippet && (
          <details className="mt-2 text-xs text-text-muted">
            <summary className="cursor-pointer hover:text-text-secondary transition-colors">
              Request Body
            </summary>
            <pre className="mt-1.5 p-2 rounded bg-bg-tertiary text-xs font-mono text-text-secondary overflow-x-auto whitespace-pre-wrap break-words max-h-32 overflow-y-auto border border-border-light">
              {formatRequestBody(attempt.req_body_snippet!)}
            </pre>
          </details>
        )}
      </article>
    </li>
  );
}
