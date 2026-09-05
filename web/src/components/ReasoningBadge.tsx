import type { ReasoningObservationState } from "../api/types";
import { BADGE_STYLES } from "../lib/utils";

interface ReasoningBadgeProps {
  observationState?: ReasoningObservationState | null;
  effort?: string | null;
  mode?: string | null;
  budgetTokens?: number | null;
}

const BADGE_CLASS =
  "inline-flex max-w-20 items-center truncate rounded-full px-2 py-0.5 text-xs font-medium";
const NEUTRAL_BADGE_CLASS =
  "bg-gray-100 text-gray-800 dark:bg-gray-900/30 dark:text-gray-300";

export function ReasoningBadge({
  observationState,
  effort,
  mode,
  budgetTokens,
}: ReasoningBadgeProps) {
  const details = formatReasoningDetails(effort, mode, budgetTokens);
  const compactLabel = getCompactLabel(effort, mode, budgetTokens);

  switch (observationState) {
    case "pending":
      return (
        <span
          className={`${BADGE_CLASS} ${NEUTRAL_BADGE_CLASS}`}
          title="Requested reasoning observation is waiting for the complete input."
        >
          Pending
        </span>
      );
    case "captured":
      return (
        <span
          className={`${BADGE_CLASS} ${NEUTRAL_BADGE_CLASS}`}
          title={withDetails(
            "Captured requested reasoning configuration.",
            details,
          )}
        >
          {compactLabel ?? "Captured"}
        </span>
      );
    case "invalid":
      return (
        <span
          className={`${BADGE_CLASS} ${BADGE_STYLES.WARNING}`}
          title={withDetails(
            "Invalid requested reasoning configuration: at least one field could not be observed.",
            details,
          )}
        >
          {compactLabel ?? "Invalid"}
        </span>
      );
    case "ambiguous":
      return (
        <span
          className={`${BADGE_CLASS} ${BADGE_STYLES.WARNING}`}
          title={withDetails(
            "Ambiguous requested reasoning configuration: duplicate fields were present; showing the last successfully decoded values.",
            details,
          )}
        >
          {compactLabel ?? "Ambiguous"}
        </span>
      );
    case "absent":
      return (
        <span title="No supported reasoning configuration was requested.">
          —
        </span>
      );
    case "unsupported":
      return (
        <span title="Requested reasoning configuration is not observed for this API type, endpoint, or transport.">
          —
        </span>
      );
    default:
      return (
        <span title="Reasoning observation is unavailable for this legacy log.">
          —
        </span>
      );
  }
}

function getCompactLabel(
  effort?: string | null,
  mode?: string | null,
  budgetTokens?: number | null,
): string | null {
  if (effort !== null && effort !== undefined) {
    return effort === "" ? `""` : effort;
  }
  if (mode !== null && mode !== undefined) {
    return mode === "" ? `""` : mode;
  }
  if (budgetTokens !== null && budgetTokens !== undefined) {
    return `${budgetTokens} tokens`;
  }
  return null;
}

function formatReasoningDetails(
  effort?: string | null,
  mode?: string | null,
  budgetTokens?: number | null,
): string {
  const details: string[] = [];
  if (effort !== null && effort !== undefined) {
    details.push(`Effort: ${JSON.stringify(effort)}`);
  }
  if (mode !== null && mode !== undefined) {
    details.push(`Thinking mode: ${JSON.stringify(mode)}`);
  }
  if (budgetTokens !== null && budgetTokens !== undefined) {
    details.push(`Thinking budget: ${budgetTokens} tokens`);
  }
  return details.join("; ");
}

function withDetails(summary: string, details: string): string {
  return details ? `${summary} ${details}.` : summary;
}
