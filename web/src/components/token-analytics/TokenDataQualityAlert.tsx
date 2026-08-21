import { AlertTriangle } from "lucide-react";
import type { TokenDataQualityDTO } from "../../api/types";

interface TokenDataQualityAlertProps {
  dataQuality: TokenDataQualityDTO;
}

export function TokenDataQualityAlert({
  dataQuality,
}: TokenDataQualityAlertProps) {
  // Only display alert when quality is under 100% or issues were observed
  const hasIssues =
    dataQuality.quality_rate < 1.0 ||
    dataQuality.partial_requests > 0 ||
    dataQuality.invalid_requests > 0 ||
    dataQuality.unknown_semantics_requests > 0;

  if (!hasIssues) {
    return null;
  }

  const qualityPct = (dataQuality.quality_rate * 100).toFixed(1);

  return (
    <div
      role="alert"
      className="rounded-xl border border-amber-200 dark:border-amber-900/50 bg-amber-50/80 dark:bg-amber-950/30 p-3.5 text-xs text-amber-800 dark:text-amber-300 flex items-start gap-2.5 shadow-2xs"
    >
      <AlertTriangle
        className="w-4 h-4 text-amber-600 dark:text-amber-400 shrink-0 mt-0.5"
        aria-hidden="true"
      />
      <div className="space-y-0.5">
        <p className="font-semibold">
          Observed Data Quality Notice ({qualityPct}% quality rate)
        </p>
        <p className="text-amber-700 dark:text-amber-400">
          Encountered {dataQuality.partial_requests.toLocaleString()} partial,{" "}
          {dataQuality.invalid_requests.toLocaleString()} invalid, and{" "}
          {dataQuality.unknown_semantics_requests.toLocaleString()} unknown
          semantics requests in this time window. Non-comparable requests are
          safely isolated from token breakdowns and average metrics to ensure
          Total = Input + Output conservation.
        </p>
      </div>
    </div>
  );
}
