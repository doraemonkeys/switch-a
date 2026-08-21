import { useState } from "react";
import { Info, RefreshCw, X, Zap } from "lucide-react";
import type {
  StatsGranularity,
  StatsPeriod,
  TokenCoverageDTO,
  TokenDataQualityDTO,
} from "../../api/types";
import {
  GRANULARITY_LABELS,
  GRANULARITY_OPTIONS_BY_PERIOD,
  PERIOD_OPTIONS,
} from "./token-format";

interface TokenAnalyticsHeaderProps {
  period: StatsPeriod;
  granularity: StatsGranularity;
  onPeriodChange: (period: StatsPeriod) => void;
  onGranularityChange: (granularity: StatsGranularity) => void;
  onRefresh: () => void;
  loading: boolean;
  coverage?: TokenCoverageDTO;
  dataQuality?: TokenDataQualityDTO;
}

export function TokenAnalyticsHeader({
  period,
  granularity,
  onPeriodChange,
  onGranularityChange,
  onRefresh,
  loading,
  coverage,
  dataQuality,
}: TokenAnalyticsHeaderProps) {
  const [showInfoModal, setShowInfoModal] = useState(false);

  const coverageText = coverage
    ? `Coverage: ${(coverage.rate * 100).toFixed(1)}%`
    : null;
  const qualityText = dataQuality
    ? `Observed-data quality: ${(dataQuality.quality_rate * 100).toFixed(1)}%`
    : null;

  return (
    <div className="flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
      {/* Title & Subtitle */}
      <div className="space-y-1">
        <div className="flex items-center gap-2.5">
          <div className="p-1.5 rounded-xl bg-indigo-50 dark:bg-indigo-950/40 text-indigo-600 dark:text-indigo-400">
            <Zap className="w-5 h-5" aria-hidden="true" />
          </div>
          <h2 className="text-xl font-bold tracking-tight text-slate-900 dark:text-slate-100">
            Token Usage Analytics
          </h2>
          <span className="px-2 py-0.5 rounded-full text-[11px] font-semibold bg-indigo-50 dark:bg-indigo-950/40 text-indigo-700 dark:text-indigo-300">
            Global
          </span>
        </div>
        <p className="text-xs text-slate-500 dark:text-slate-400 flex flex-wrap items-center gap-x-2 gap-y-0.5">
          <span>Global aggregated volume & efficiency</span>
          {coverageText && (
            <>
              <span>•</span>
              <span>{coverageText}</span>
            </>
          )}
          {qualityText && (
            <>
              <span>•</span>
              <span>{qualityText}</span>
            </>
          )}
        </p>
      </div>

      {/* Action Controls */}
      <div className="flex flex-wrap items-center gap-2.5">
        {/* Period Selector */}
        <div className="flex items-center">
          <label htmlFor="token-analytics-period" className="sr-only">
            Time Range
          </label>
          <select
            id="token-analytics-period"
            value={period}
            onChange={(e) => {
              const nextPeriod = e.target.value as StatsPeriod;
              onPeriodChange(nextPeriod);
            }}
            className="input input-sm font-medium text-xs bg-white dark:bg-slate-900 border-slate-200 dark:border-slate-800 rounded-xl"
          >
            {PERIOD_OPTIONS.map((opt) => (
              <option key={opt.value} value={opt.value}>
                {opt.label}
              </option>
            ))}
          </select>
        </div>

        {/* Granularity Selector */}
        <div className="flex items-center">
          <label htmlFor="token-analytics-granularity" className="sr-only">
            Bucket Size
          </label>
          <select
            id="token-analytics-granularity"
            value={granularity}
            onChange={(e) =>
              onGranularityChange(e.target.value as StatsGranularity)
            }
            className="input input-sm font-medium text-xs bg-white dark:bg-slate-900 border-slate-200 dark:border-slate-800 rounded-xl"
          >
            {GRANULARITY_OPTIONS_BY_PERIOD[period].map((g) => (
              <option key={g} value={g}>
                {GRANULARITY_LABELS[g]}
              </option>
            ))}
          </select>
        </div>

        {/* Refresh Button */}
        <button
          type="button"
          onClick={onRefresh}
          disabled={loading}
          className="btn btn-secondary btn-sm rounded-xl text-xs flex items-center gap-1.5 cursor-pointer hover:bg-slate-50 dark:hover:bg-slate-800"
          aria-label="Refresh token analytics"
        >
          <RefreshCw
            className={`w-3.5 h-3.5 ${loading ? "animate-spin text-indigo-500" : ""}`}
            aria-hidden="true"
          />
          <span>Refresh</span>
        </button>

        {/* Info / Guide Button */}
        <button
          type="button"
          onClick={() => setShowInfoModal(true)}
          className="p-2 rounded-xl text-slate-400 hover:text-slate-600 dark:hover:text-slate-200 hover:bg-slate-100 dark:hover:bg-slate-800 transition-colors cursor-pointer"
          aria-label="Token usage analytics information"
        >
          <Info className="w-4 h-4" aria-hidden="true" />
        </button>
      </div>

      {/* Information Modal */}
      {showInfoModal && (
        <div
          className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/50 backdrop-blur-xs"
          role="dialog"
          aria-modal="true"
          aria-labelledby="token-info-title"
        >
          <div className="bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-2xl max-w-lg w-full p-6 space-y-4 shadow-2xl relative">
            <div className="flex items-center justify-between border-b border-slate-100 dark:border-slate-800 pb-3">
              <h3
                id="token-info-title"
                className="text-base font-bold text-slate-900 dark:text-slate-100 flex items-center gap-2"
              >
                <Info className="w-5 h-5 text-indigo-500" aria-hidden="true" />
                Token Analytics Guide
              </h3>
              <button
                type="button"
                onClick={() => setShowInfoModal(false)}
                className="p-1 rounded-lg text-slate-400 hover:text-slate-600 dark:hover:text-slate-200 cursor-pointer"
                aria-label="Close dialog"
              >
                <X className="w-5 h-5" aria-hidden="true" />
              </button>
            </div>

            <div className="space-y-3 text-xs text-slate-600 dark:text-slate-300 max-h-[60vh] overflow-y-auto pr-1">
              <section className="space-y-1">
                <h4 className="font-semibold text-slate-900 dark:text-slate-100">
                  Global Scope & Alignment
                </h4>
                <p>
                  Metrics in this panel represent system-wide aggregates for all
                  requests across the selected time range. Table filters on the
                  request logs do not scope these global totals.
                </p>
              </section>

              <section className="space-y-1">
                <h4 className="font-semibold text-slate-900 dark:text-slate-100">
                  Canonical Token Conservation
                </h4>
                <p>
                  To guarantee mathematical consistency (
                  <code>Total = Input + Output</code>), telemetry is projected
                  through protocol-level canonical definitions:
                </p>
                <ul className="list-disc pl-4 space-y-1 text-slate-500 dark:text-slate-400">
                  <li>
                    <strong>Fresh Input</strong>: Standard uncached prompt
                    tokens.
                  </li>
                  <li>
                    <strong>Cache Read</strong>: Cached prompt tokens (billed at
                    reduced rates).
                  </li>
                  <li>
                    <strong>Cache Creation</strong>: Tokens written to cache.
                  </li>
                  <li>
                    <strong>Reasoning (CoT)</strong>: Thinking tokens (nested
                    inside Output).
                  </li>
                  <li>
                    <strong>Unclassified</strong>: Tokens where cache or
                    reasoning details are unavailable.
                  </li>
                </ul>
              </section>

              <section className="space-y-1">
                <h4 className="font-semibold text-slate-900 dark:text-slate-100">
                  Coverage vs Quality
                </h4>
                <p>
                  <strong>Coverage</strong> indicates the fraction of total
                  traffic with full token telemetry. Non-generation or failed
                  requests naturally fall outside token coverage without
                  indicating telemetry faults.
                </p>
                <p>
                  <strong>Observed Quality</strong> measures whether recorded
                  token telemetry conforms strictly to canonical schema
                  constraints.
                </p>
              </section>
            </div>

            <div className="pt-2 flex justify-end">
              <button
                type="button"
                onClick={() => setShowInfoModal(false)}
                className="btn btn-primary btn-sm rounded-xl cursor-pointer"
              >
                Got it
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
