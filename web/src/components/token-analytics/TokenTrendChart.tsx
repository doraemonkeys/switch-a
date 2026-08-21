import { useId, useState } from "react";
import type {
  StatsGranularity,
  StatsPeriod,
  TokenBucketDTO,
} from "../../api/types";
import {
  calculateTokenPercent,
  formatTokenCompact,
  formatTokenLocale,
  parseTokenBigInt,
  TOKEN_SEMANTICS,
} from "./token-format";

interface TokenTrendChartProps {
  timeseries: TokenBucketDTO[];
  period: StatsPeriod;
  granularity: StatsGranularity;
}

type SeriesKey =
  | "fresh"
  | "cacheRead"
  | "cacheCreation"
  | "unclassifiedInput"
  | "standardOutput"
  | "reasoning"
  | "unclassifiedOutput";

interface SeriesDef {
  key: SeriesKey;
  label: string;
  field: keyof TokenBucketDTO;
  semanticKey: string;
}

const SERIES_CONFIG: SeriesDef[] = [
  {
    key: "fresh",
    label: "Fresh Input",
    field: "fresh_input_tokens",
    semanticKey: "fresh",
  },
  {
    key: "cacheRead",
    label: "Cache Read",
    field: "cache_read_input_tokens",
    semanticKey: "cacheRead",
  },
  {
    key: "cacheCreation",
    label: "Cache Creation",
    field: "cache_creation_input_tokens",
    semanticKey: "cacheCreation",
  },
  {
    key: "unclassifiedInput",
    label: "Unclassified In",
    field: "unclassified_input_tokens",
    semanticKey: "unclassifiedInput",
  },
  {
    key: "standardOutput",
    label: "Standard Output",
    field: "standard_output_tokens",
    semanticKey: "standardOutput",
  },
  {
    key: "reasoning",
    label: "Reasoning CoT",
    field: "reasoning_tokens",
    semanticKey: "reasoning",
  },
  {
    key: "unclassifiedOutput",
    label: "Unclassified Out",
    field: "unclassified_output_tokens",
    semanticKey: "unclassifiedOutput",
  },
];

function formatTimeAxisLabel(
  isoString: string,
  granularity: StatsGranularity,
): string {
  try {
    const date = new Date(isoString);
    if (granularity === "1d") {
      return new Intl.DateTimeFormat(undefined, {
        month: "short",
        day: "numeric",
      }).format(date);
    }
    return new Intl.DateTimeFormat(undefined, {
      month: "short",
      day: "numeric",
      hour: "2-digit",
      minute: "2-digit",
      hour12: false,
    }).format(date);
  } catch {
    return isoString;
  }
}

function formatTooltipTimeRange(startStr: string, endStr: string): string {
  try {
    const s = new Date(startStr);
    const e = new Date(endStr);
    const timeFormat = new Intl.DateTimeFormat(undefined, {
      month: "short",
      day: "numeric",
      hour: "2-digit",
      minute: "2-digit",
      hour12: false,
    });
    return `${timeFormat.format(s)} � ${timeFormat.format(e)}`;
  } catch {
    return `${startStr} � ${endStr}`;
  }
}

interface TrendTooltipProps {
  bucket: TokenBucketDTO;
  id: string;
}

function TrendTooltip({ bucket, id }: TrendTooltipProps) {
  const unclassifiedIn = parseTokenBigInt(bucket.unclassified_input_tokens);
  const unclassifiedOut = parseTokenBigInt(bucket.unclassified_output_tokens);

  return (
    <div
      className="rounded-xl border border-slate-200 dark:border-slate-700/80 bg-white/95 dark:bg-slate-900/95 backdrop-blur-md p-4 shadow-xl text-xs space-y-3 transition-all"
      id={id}
      role="tooltip"
    >
      <div className="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-1 border-b border-slate-100 dark:border-slate-800 pb-2">
        <div className="font-semibold text-slate-900 dark:text-slate-100 flex items-center gap-1.5">
          <span>??</span>
          <span>{formatTooltipTimeRange(bucket.start, bucket.end)}</span>
        </div>
        <span className="text-slate-500 dark:text-slate-400 font-mono text-[11px]">
          {bucket.observed_requests.toLocaleString()} observed /{" "}
          {bucket.total_requests.toLocaleString()} total
        </span>
      </div>

      <div className="flex items-center justify-between font-mono font-bold text-sm text-slate-900 dark:text-slate-100">
        <span>Total Tokens</span>
        <span className="text-indigo-600 dark:text-indigo-400">
          {formatTokenLocale(bucket.total_tokens)}
        </span>
      </div>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-3 pt-1 border-t border-slate-100 dark:border-slate-800 text-[11px]">
        {/* Input Breakdown */}
        <div className="space-y-1">
          <div className="font-semibold text-sky-700 dark:text-sky-300 flex items-center justify-between">
            <span>?? Input Tokens</span>
            <span>
              {formatTokenLocale(bucket.input_tokens)} (
              {calculateTokenPercent(
                bucket.input_tokens,
                bucket.total_tokens,
              ).toFixed(1)}
              %)
            </span>
          </div>
          <div className="pl-2 space-y-0.5 text-slate-600 dark:text-slate-400">
            <div className="flex items-center justify-between">
              <span className="flex items-center gap-1">
                <span
                  className={`inline-block w-1.5 h-1.5 rounded-full ${TOKEN_SEMANTICS.cacheRead.bgClass}`}
                />
                Cache Read (Hit)
              </span>
              <span>
                {formatTokenLocale(bucket.cache_read_input_tokens)} (
                {calculateTokenPercent(
                  bucket.cache_read_input_tokens,
                  bucket.input_tokens,
                ).toFixed(1)}
                %)
              </span>
            </div>
            <div className="flex items-center justify-between">
              <span className="flex items-center gap-1">
                <span
                  className={`inline-block w-1.5 h-1.5 rounded-full ${TOKEN_SEMANTICS.cacheCreation.bgClass}`}
                />
                Cache Creation
              </span>
              <span>
                {formatTokenLocale(bucket.cache_creation_input_tokens)} (
                {calculateTokenPercent(
                  bucket.cache_creation_input_tokens,
                  bucket.input_tokens,
                ).toFixed(1)}
                %)
              </span>
            </div>
            <div className="flex items-center justify-between">
              <span className="flex items-center gap-1">
                <span
                  className={`inline-block w-1.5 h-1.5 rounded-full ${TOKEN_SEMANTICS.fresh.bgClass}`}
                />
                Fresh Prompt
              </span>
              <span>
                {formatTokenLocale(bucket.fresh_input_tokens)} (
                {calculateTokenPercent(
                  bucket.fresh_input_tokens,
                  bucket.input_tokens,
                ).toFixed(1)}
                %)
              </span>
            </div>
            {unclassifiedIn > 0n && (
              <div className="flex items-center justify-between">
                <span className="flex items-center gap-1">
                  <span
                    className={`inline-block w-1.5 h-1.5 rounded-full ${TOKEN_SEMANTICS.unclassifiedInput.bgClass}`}
                  />
                  Unclassified In
                </span>
                <span>
                  {formatTokenLocale(bucket.unclassified_input_tokens)} (
                  {calculateTokenPercent(
                    bucket.unclassified_input_tokens,
                    bucket.input_tokens,
                  ).toFixed(1)}
                  %)
                </span>
              </div>
            )}
          </div>
        </div>

        {/* Output Breakdown */}
        <div className="space-y-1">
          <div className="font-semibold text-violet-700 dark:text-violet-300 flex items-center justify-between">
            <span>?? Output Tokens</span>
            <span>
              {formatTokenLocale(bucket.output_tokens)} (
              {calculateTokenPercent(
                bucket.output_tokens,
                bucket.total_tokens,
              ).toFixed(1)}
              %)
            </span>
          </div>
          <div className="pl-2 space-y-0.5 text-slate-600 dark:text-slate-400">
            <div className="flex items-center justify-between">
              <span className="flex items-center gap-1">
                <span
                  className={`inline-block w-1.5 h-1.5 rounded-full ${TOKEN_SEMANTICS.reasoning.bgClass}`}
                />
                Reasoning (CoT)
              </span>
              <span>
                {formatTokenLocale(bucket.reasoning_tokens)} (
                {calculateTokenPercent(
                  bucket.reasoning_tokens,
                  bucket.output_tokens,
                ).toFixed(1)}
                %)
              </span>
            </div>
            <div className="flex items-center justify-between">
              <span className="flex items-center gap-1">
                <span
                  className={`inline-block w-1.5 h-1.5 rounded-full ${TOKEN_SEMANTICS.standardOutput.bgClass}`}
                />
                Standard Output
              </span>
              <span>
                {formatTokenLocale(bucket.standard_output_tokens)} (
                {calculateTokenPercent(
                  bucket.standard_output_tokens,
                  bucket.output_tokens,
                ).toFixed(1)}
                %)
              </span>
            </div>
            {unclassifiedOut > 0n && (
              <div className="flex items-center justify-between">
                <span className="flex items-center gap-1">
                  <span
                    className={`inline-block w-1.5 h-1.5 rounded-full ${TOKEN_SEMANTICS.unclassifiedOutput.bgClass}`}
                  />
                  Unclassified Out
                </span>
                <span>
                  {formatTokenLocale(bucket.unclassified_output_tokens)} (
                  {calculateTokenPercent(
                    bucket.unclassified_output_tokens,
                    bucket.output_tokens,
                  ).toFixed(1)}
                  %)
                </span>
              </div>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}

export function TokenTrendChart({
  timeseries,
  granularity,
}: TokenTrendChartProps) {
  const [selectedSeries, setSelectedSeries] = useState<
    Record<SeriesKey, boolean>
  >({
    fresh: true,
    cacheRead: true,
    cacheCreation: true,
    unclassifiedInput: true,
    standardOutput: true,
    reasoning: true,
    unclassifiedOutput: true,
  });

  const tooltipId = useId();
  const [activeBucketIndex, setActiveBucketIndex] = useState<number | null>(
    null,
  );

  const toggleSeries = (key: SeriesKey) => {
    setSelectedSeries((prev) => {
      const next = { ...prev, [key]: !prev[key] };
      const anyActive = Object.values(next).some(Boolean);
      return anyActive ? next : prev;
    });
  };

  if (timeseries.length === 0) {
    return (
      <div className="rounded-2xl border border-dashed border-slate-200 dark:border-slate-800 p-8 text-center text-sm text-slate-500 dark:text-slate-400">
        No token time series recorded for this time window.
      </div>
    );
  }

  const maxBucketVolume = timeseries.reduce((max, bucket) => {
    let bucketVisibleVolume = 0n;
    for (const s of SERIES_CONFIG) {
      if (selectedSeries[s.key]) {
        bucketVisibleVolume += parseTokenBigInt(bucket[s.field] as string);
      }
    }
    return bucketVisibleVolume > max ? bucketVisibleVolume : max;
  }, 0n);

  const scaleMax = maxBucketVolume > 0n ? maxBucketVolume : 1n;
  const activeBucket =
    activeBucketIndex !== null ? timeseries[activeBucketIndex] : null;

  const firstBucket = timeseries[0];
  const lastBucket = timeseries[timeseries.length - 1];
  const midBucket =
    timeseries.length > 2
      ? timeseries[Math.floor(timeseries.length / 2)]
      : null;

  return (
    <section
      className="bg-white dark:bg-slate-900 border border-slate-200/80 dark:border-slate-800 rounded-2xl p-5 shadow-xs space-y-4 relative"
      aria-labelledby="token-trend-heading"
    >
      <div className="flex flex-col gap-3 lg:flex-row lg:items-center lg:justify-between">
        <div>
          <h3
            id="token-trend-heading"
            className="text-base font-semibold text-slate-900 dark:text-slate-100"
          >
            Token Consumption Trend Over Time
          </h3>
          <p className="text-xs text-slate-500 dark:text-slate-400 mt-0.5">
            Stacked canonical token breakdown per {granularity} bucket.
          </p>
        </div>

        <div className="flex flex-wrap items-center gap-2">
          {SERIES_CONFIG.map((s) => {
            const isEnabled = selectedSeries[s.key];
            const semantic = TOKEN_SEMANTICS[s.semanticKey];
            return (
              <button
                type="button"
                key={s.key}
                onClick={() => toggleSeries(s.key)}
                className={`inline-flex items-center gap-1.5 px-2.5 py-1 rounded-lg text-xs font-medium transition-all cursor-pointer border ${
                  isEnabled
                    ? "bg-slate-50 dark:bg-slate-800 text-slate-700 dark:text-slate-200 border-slate-200 dark:border-slate-700 shadow-2xs"
                    : "bg-transparent text-slate-400 dark:text-slate-500 border-transparent opacity-60 hover:opacity-100"
                }`}
                aria-pressed={isEnabled}
              >
                <span
                  className={`w-2 h-2 rounded-full ${semantic.bgClass} ${!isEnabled ? "opacity-30" : ""}`}
                />
                <span>{s.label}</span>
              </button>
            );
          })}
        </div>
      </div>

      <div className="relative pt-6 pb-2">
        <div className="absolute inset-x-0 inset-y-6 flex flex-col justify-between pointer-events-none">
          {[1, 0.75, 0.5, 0.25, 0].map((ratio) => {
            const tickBigInt =
              maxBucketVolume > 0n
                ? (maxBucketVolume * BigInt(Math.round(ratio * 100))) / 100n
                : 0n;
            return (
              <div
                key={ratio}
                className="w-full flex items-center border-b border-dashed border-slate-100 dark:border-slate-800/80 relative"
              >
                <span className="absolute -top-3 right-0 text-[10px] font-mono text-slate-400 dark:text-slate-500 bg-white/80 dark:bg-slate-900/80 px-1 rounded">
                  {formatTokenCompact(tickBigInt)}
                </span>
              </div>
            );
          })}
        </div>

        <div
          className="relative z-10 h-52 flex items-end gap-1 sm:gap-1.5 overflow-x-auto pt-2 pb-1"
          onMouseLeave={(event) => {
            if (!event.currentTarget.contains(document.activeElement)) {
              setActiveBucketIndex(null);
            }
          }}
        >
          {timeseries.map((bucket, index) => {
            let visibleTotal = 0n;
            for (const s of SERIES_CONFIG) {
              if (selectedSeries[s.key]) {
                visibleTotal += parseTokenBigInt(bucket[s.field] as string);
              }
            }

            const heightPercent =
              maxBucketVolume > 0n
                ? Number((visibleTotal * 10000n) / scaleMax) / 100
                : 0;

            const isActive = activeBucketIndex === index;

            return (
              <button
                type="button"
                key={bucket.start}
                className="group relative flex h-full min-w-[12px] max-w-[48px] flex-1 cursor-pointer appearance-none flex-col justify-end border-0 bg-transparent p-0 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-indigo-500 focus-visible:ring-offset-2 dark:focus-visible:ring-indigo-400 dark:focus-visible:ring-offset-slate-900"
                onMouseEnter={() => setActiveBucketIndex(index)}
                onClick={() => setActiveBucketIndex(index)}
                onFocus={() => setActiveBucketIndex(index)}
                onBlur={() =>
                  setActiveBucketIndex((current) =>
                    current === index ? null : current,
                  )
                }
                aria-describedby={isActive ? tooltipId : undefined}
                aria-expanded={isActive}
                aria-label={`Bucket ${bucket.start}: ${formatTokenLocale(bucket.total_tokens)} tokens`}
              >
                <div
                  className={`w-full flex flex-col-reverse rounded-t-[3px] overflow-hidden transition-all duration-150 ${
                    isActive
                      ? "ring-2 ring-indigo-500/70 dark:ring-indigo-400/80 shadow-md brightness-110"
                      : "opacity-90 hover:opacity-100"
                  }`}
                  style={{
                    height: `${Math.max(heightPercent > 0 ? heightPercent : 0, visibleTotal > 0n ? 3 : 0)}%`,
                  }}
                >
                  {SERIES_CONFIG.map((s) => {
                    if (!selectedSeries[s.key]) return null;
                    const val = parseTokenBigInt(bucket[s.field] as string);
                    if (val === 0n || visibleTotal === 0n) return null;

                    const segmentHeightPct =
                      Number((val * 10000n) / visibleTotal) / 100;
                    const semantic = TOKEN_SEMANTICS[s.semanticKey];

                    return (
                      <div
                        key={s.key}
                        className={`${semantic.bgClass} w-full transition-all`}
                        style={{ height: `${segmentHeightPct}%` }}
                      />
                    );
                  })}
                </div>
              </button>
            );
          })}
        </div>

        <div className="flex items-center justify-between text-[11px] text-slate-400 dark:text-slate-500 font-mono pt-2 border-t border-slate-200/60 dark:border-slate-800">
          <span>{formatTimeAxisLabel(firstBucket.start, granularity)}</span>
          {midBucket && (
            <span>{formatTimeAxisLabel(midBucket.start, granularity)}</span>
          )}
          <span>{formatTimeAxisLabel(lastBucket.end, granularity)}</span>
        </div>
      </div>

      {activeBucket && <TrendTooltip id={tooltipId} bucket={activeBucket} />}
    </section>
  );
}
