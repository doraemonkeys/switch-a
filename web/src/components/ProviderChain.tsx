import type { RequestAttempt } from "../api/types";

interface ProviderChainProps {
  attempts: RequestAttempt[];
  /** Provider name map for display */
  providerNames?: Map<string, string>;
  /** Final success status of the request */
  success: boolean;
}

interface ChainNode {
  providerId: string;
  providerName: string;
  status: "success" | "failed";
  statusCode: number;
  attemptNumber: number;
}

/** Determines the status for an attempt based on position and final result */
function getAttemptStatus(
  isLast: boolean,
  finalSuccess?: boolean,
): ChainNode["status"] {
  if (!isLast) return "failed";
  return finalSuccess ? "success" : "failed";
}

/**
 * Updates the last node in the chain with new attempt data.
 * Note: This function mutates the chain array in-place for performance.
 */
function updateLastNode(
  chain: ChainNode[],
  attempt: RequestAttempt,
  finalSuccess?: boolean,
): void {
  const lastNode = chain[chain.length - 1];
  lastNode.status = finalSuccess ? "success" : "failed";
  lastNode.statusCode = attempt.status_code;
  lastNode.attemptNumber = attempt.attempt + 1;
}

/**
 * Extracts the provider chain from attempts data.
 * Groups consecutive attempts to the same provider and shows the final status.
 */
function extractChain(
  attempts: RequestAttempt[],
  providerNames?: Map<string, string>,
  finalSuccess?: boolean,
): ChainNode[] {
  if (!attempts || attempts.length === 0) return [];

  // Match backend ordering so compact chain summaries never disagree with the
  // detailed timeline when attempt ordinals tie.
  const sorted = [...attempts].sort(
    (a, b) => a.attempt - b.attempt || a.id - b.id,
  );

  // Group by provider, keeping the last attempt for each provider
  const chain: ChainNode[] = [];
  let currentProviderId: string | null = null;

  for (let i = 0; i < sorted.length; i++) {
    const attempt = sorted[i];
    const isLast = i === sorted.length - 1;
    const isNewProvider = attempt.provider_id !== currentProviderId;

    if (isNewProvider) {
      currentProviderId = attempt.provider_id;
      chain.push({
        providerId: attempt.provider_id,
        providerName:
          providerNames?.get(attempt.provider_id) || attempt.provider_id,
        status: getAttemptStatus(isLast, finalSuccess),
        statusCode: attempt.status_code,
        attemptNumber: attempt.attempt + 1,
      });
    } else if (isLast) {
      updateLastNode(chain, attempt, finalSuccess);
    }
  }

  return chain;
}

/**
 * Visual provider chain showing the failover path with horizontal arrows.
 *
 * Example:
 *   hongmacc-1 ───❌───▸ yes.vg-1 ───✅
 *
 * Features:
 * - Horizontal layout with flowing arrows
 * - Color-coded status indicators (red X for failed, green check for success)
 * - Provider names with status badges
 * - Compact design for modal display
 */
export function ProviderChain({
  attempts,
  providerNames,
  success,
}: ProviderChainProps) {
  const chain = extractChain(attempts, providerNames, success);

  if (chain.length === 0) return null;

  // Single provider - show simple inline display
  if (chain.length === 1) {
    const node = chain[0];
    return (
      <div className="flex items-center gap-2">
        <span className="text-sm font-medium text-text-primary">
          {node.providerName}
        </span>
        <StatusIcon status={node.status} />
      </div>
    );
  }

  // Multiple providers - show chain with arrows
  return (
    <div className="flex items-center flex-wrap gap-y-2">
      {chain.map((node, index) => (
        <div key={`${node.providerId}-${index}`} className="flex items-center">
          {/* Provider Node */}
          <ChainNodeBadge node={node} />

          {/* Arrow to next provider */}
          {index < chain.length - 1 && <ChainArrow status={node.status} />}
        </div>
      ))}
    </div>
  );
}

function ChainNodeBadge({ node }: { node: ChainNode }) {
  const bgClass =
    node.status === "success"
      ? "bg-green-50 border-green-200 dark:bg-green-900/20 dark:border-green-800"
      : "bg-red-50 border-red-200 dark:bg-red-900/20 dark:border-red-800";

  const textClass =
    node.status === "success"
      ? "text-green-700 dark:text-green-300"
      : "text-red-700 dark:text-red-300";

  const tooltipText = `Status: ${node.statusCode} (Attempt #${node.attemptNumber})`;

  return (
    <div
      className={`flex items-center gap-1.5 px-2.5 py-1 rounded-md border ${bgClass}`}
      title={tooltipText}
    >
      <span className={`text-sm font-medium ${textClass}`}>
        {node.providerName}
      </span>
      <StatusIcon status={node.status} size="sm" />
    </div>
  );
}

function ChainArrow({ status }: { status: ChainNode["status"] }) {
  // Arrow color based on the source node's status
  const color =
    status === "failed"
      ? "text-red-400 dark:text-red-500"
      : "text-green-400 dark:text-green-500";

  return (
    <div className={`flex items-center mx-1 ${color}`}>
      {/* Dashed line */}
      <div className="w-4 border-t-2 border-dashed border-current" />
      {/* Arrow head */}
      <svg className="w-3 h-3 -ml-0.5" viewBox="0 0 12 12" fill="currentColor">
        <path d="M4 2l6 4-6 4V2z" />
      </svg>
    </div>
  );
}

function StatusIcon({
  status,
  size = "md",
}: {
  status: ChainNode["status"];
  size?: "sm" | "md";
}) {
  const sizeClass = size === "sm" ? "w-3.5 h-3.5" : "w-4 h-4";

  if (status === "success") {
    return (
      <svg
        className={`${sizeClass} text-green-500`}
        viewBox="0 0 20 20"
        fill="currentColor"
        aria-label="Success"
        role="img"
      >
        <path
          fillRule="evenodd"
          d="M10 18a8 8 0 100-16 8 8 0 000 16zm3.857-9.809a.75.75 0 00-1.214-.882l-3.483 4.79-1.88-1.88a.75.75 0 10-1.06 1.061l2.5 2.5a.75.75 0 001.137-.089l4-5.5z"
          clipRule="evenodd"
        />
      </svg>
    );
  }

  return (
    <svg
      className={`${sizeClass} text-red-500`}
      viewBox="0 0 20 20"
      fill="currentColor"
      aria-label="Failed"
      role="img"
    >
      <path
        fillRule="evenodd"
        d="M10 18a8 8 0 100-16 8 8 0 000 16zM8.28 7.22a.75.75 0 00-1.06 1.06L8.94 10l-1.72 1.72a.75.75 0 101.06 1.06L10 11.06l1.72 1.72a.75.75 0 101.06-1.06L11.06 10l1.72-1.72a.75.75 0 00-1.06-1.06L10 8.94 8.28 7.22z"
        clipRule="evenodd"
      />
    </svg>
  );
}
