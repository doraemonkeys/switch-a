import { AlertCircle, RefreshCw } from "lucide-react";
import type { TokenUsageResponse } from "../../api/types";
import type {
  AnalyticsWindow,
  AnalyticsWindowIntent,
} from "../../features/analytics-window/analytics-window";
import { TokenAnalyticsHeader } from "./TokenAnalyticsHeader";
import { TokenDataQualityAlert } from "./TokenDataQualityAlert";
import { TokenEmptyState } from "./TokenEmptyState";
import { TokenHeroCards } from "./TokenHeroCards";
import { TokenSkeleton } from "./TokenSkeleton";
import { TokenTopBreakdown } from "./TokenTopBreakdown";
import { TokenTrendChart } from "./TokenTrendChart";

interface TokenUsageAnalyticsPanelProps {
  data: TokenUsageResponse | null;
  loading: boolean;
  error: Error | null;
  window: AnalyticsWindow;
  onWindowIntent: (intent: AnalyticsWindowIntent) => void;
  hasActiveFilters?: boolean;
}

export function TokenUsageAnalyticsPanel({
  data,
  loading,
  error,
  window,
  onWindowIntent,
  hasActiveFilters = false,
}: TokenUsageAnalyticsPanelProps) {
  const isEmpty =
    data &&
    data.coverage.observed_requests === 0 &&
    data.summary.total_tokens === "0";

  return (
    <section
      className="card p-5 space-y-6 bg-white dark:bg-slate-900 border border-slate-200/80 dark:border-slate-800 rounded-2xl shadow-xs transition-all"
      aria-label="Token Usage Analytics Section"
    >
      {/* Header controls & summary */}
      <TokenAnalyticsHeader
        period={window.period}
        granularity={window.granularity}
        onPeriodChange={(period) =>
          onWindowIntent({ type: "period-selected", period })
        }
        onGranularityChange={(granularity) =>
          onWindowIntent({ type: "granularity-selected", granularity })
        }
        onRefresh={() => onWindowIntent({ type: "refresh-requested" })}
        loading={loading}
        coverage={data?.coverage}
        dataQuality={data?.data_quality}
      />

      {/* Table filter notice */}
      {hasActiveFilters && (
        <div
          role="note"
          className="rounded-xl border border-warning-light bg-warning-light/40 dark:bg-amber-950/20 p-3.5 text-xs text-warning-dark dark:text-amber-300"
        >
          Active table filters do not scope this token analytics panel. Use the
          analytics window controls above to change the global time range.
        </div>
      )}

      {/* Error state */}
      {error && !data && (
        <div
          role="alert"
          className="rounded-2xl border border-danger-light bg-danger-light/50 dark:bg-red-950/30 p-6 text-center space-y-3"
        >
          <div className="mx-auto w-10 h-10 rounded-full bg-red-100 dark:bg-red-900/40 text-danger flex items-center justify-center">
            <AlertCircle className="w-5 h-5" aria-hidden="true" />
          </div>
          <div className="space-y-1">
            <p className="text-sm font-semibold text-danger">
              Failed to load token usage analytics
            </p>
            <p className="text-xs text-text-muted">
              {error.message ||
                "An unexpected error occurred while fetching analytics."}
            </p>
          </div>
          <button
            type="button"
            onClick={() => onWindowIntent({ type: "refresh-requested" })}
            className="btn btn-secondary btn-sm rounded-xl text-xs cursor-pointer inline-flex items-center gap-1.5"
          >
            <RefreshCw className="w-3.5 h-3.5" aria-hidden="true" />
            Retry
          </button>
        </div>
      )}

      {error && data && (
        <div
          role="alert"
          className="flex flex-col gap-3 rounded-xl border border-amber-300/80 bg-amber-50/80 p-3.5 text-xs text-amber-950 dark:border-amber-800 dark:bg-amber-950/30 dark:text-amber-200 sm:flex-row sm:items-center sm:justify-between"
        >
          <div className="flex items-start gap-2">
            <AlertCircle
              className="mt-0.5 h-4 w-4 shrink-0"
              aria-hidden="true"
            />
            <div>
              <p className="font-semibold">
                Refresh failed ? showing the last successful snapshot
              </p>
              <p className="mt-0.5">
                Snapshot ended{" "}
                <time dateTime={data.time_range.end}>
                  {new Intl.DateTimeFormat(undefined, {
                    dateStyle: "medium",
                    timeStyle: "medium",
                  }).format(new Date(data.time_range.end))}
                </time>
                . {error.message}
              </p>
            </div>
          </div>
          <button
            type="button"
            onClick={() => onWindowIntent({ type: "refresh-requested" })}
            className="btn btn-secondary btn-sm shrink-0 rounded-xl text-xs"
          >
            <RefreshCw className="h-3.5 w-3.5" aria-hidden="true" />
            Retry
          </button>
        </div>
      )}

      {/* Loading state (initial) */}
      {loading && !data && <TokenSkeleton />}

      {/* Data views */}
      {data && (
        <div className="space-y-6">
          {/* Data Quality Notice */}
          <TokenDataQualityAlert dataQuality={data.data_quality} />

          {/* Empty state */}
          {isEmpty ? (
            <TokenEmptyState />
          ) : (
            <>
              {/* 4 Hero KPI Cards */}
              <TokenHeroCards
                summary={data.summary}
                coverage={data.coverage}
                dataQuality={data.data_quality}
                timeseries={data.timeseries}
              />

              {/* Time Series Trend Chart */}
              <TokenTrendChart
                timeseries={data.timeseries}
                period={window.period}
                granularity={window.granularity}
              />

              {/* Top Breakdown Matrix */}
              <TokenTopBreakdown
                byProvider={data.by_provider}
                byModel={data.by_model}
              />
            </>
          )}
        </div>
      )}
    </section>
  );
}
