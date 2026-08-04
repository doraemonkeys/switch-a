import type { RequestAttempt } from "@/api/types";
import { sortRequestAttempts } from "../model/attempt-presentation";
import { AttemptNode } from "./AttemptNode";

interface RequestAttemptTimelineProps {
  attempts: RequestAttempt[];
  providerNames?: Map<string, string>;
  userAgent?: string;
  isWebSocket?: boolean;
  attributedProviderId?: string;
}

export function RequestAttemptTimeline({
  attempts,
  providerNames,
  userAgent,
  isWebSocket = false,
  attributedProviderId,
}: RequestAttemptTimelineProps) {
  if (!attempts || attempts.length === 0) {
    return null;
  }
  // Backend ordinals can repeat across provider attempts, so the persisted row
  // ID is the deterministic tie-breaker rather than incidental array order.
  const sortedAttempts = sortRequestAttempts(attempts);
  const firstAttemptTime = sortedAttempts[0]?.created_at;
  const attributedAttemptID =
    isWebSocket && attributedProviderId
      ? [...sortedAttempts]
          .reverse()
          .find((attempt) => attempt.provider_id === attributedProviderId)?.id
      : undefined;
  return (
    <div className="relative">
      <span
        aria-hidden="true"
        className="absolute left-3 top-0 bottom-0 w-0.5 bg-border-light"
      />
      <ol className="space-y-4" aria-label="Request attempts">
        {sortedAttempts.map((attempt, index) => (
          <AttemptNode
            key={attempt.id}
            attempt={attempt}
            isFirst={index === 0}
            isLast={index === sortedAttempts.length - 1}
            providerName={providerNames?.get(attempt.provider_id)}
            userAgent={userAgent}
            firstAttemptTime={firstAttemptTime}
            displayAttemptNumber={index + 1}
            isWebSocket={isWebSocket}
            isAttributedAttempt={attempt.id === attributedAttemptID}
            continuityOriginProviderName={
              attempt.continuity_origin_provider_id
                ? providerNames?.get(attempt.continuity_origin_provider_id)
                : undefined
            }
          />
        ))}
      </ol>
    </div>
  );
}
