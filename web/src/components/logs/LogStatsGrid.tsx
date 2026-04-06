import type {
  OutcomeTimeSeriesPoint,
  ServiceOutcome,
  StatsGranularity,
  StatsParams,
  StatsPeriod,
  StatsResponse,
} from "../../api/types";
import { formatDuration } from "../../lib/utils";
import { getStatVariantClass, type StatVariantValue } from "./utils";

const DEFAULT_STATS_PERIOD: StatsPeriod = "24h";
const PERIOD_LABELS: Record<StatsPeriod, string> = {
  "24h": "Last 24 hours",
  "7d": "Last 7 days",
  "30d": "Last 30 days",
  all: "All time",
};
const PERIOD_OPTIONS: Array<{ value: StatsPeriod; label: string }> = [
  { value: "24h", label: PERIOD_LABELS["24h"] },
  { value: "7d", label: PERIOD_LABELS["7d"] },
  { value: "30d", label: PERIOD_LABELS["30d"] },
  { value: "all", label: PERIOD_LABELS.all },
];
const GRANULARITY_LABELS: Record<StatsGranularity, string> = {
  "5m": "5 minutes",
  "15m": "15 minutes",
  "1h": "1 hour",
  "6h": "6 hours",
  "1d": "1 day",
};
const GRANULARITY_OPTIONS_BY_PERIOD: Record<StatsPeriod, StatsGranularity[]> = {
  "24h": ["5m", "15m", "1h"],
  "7d": ["1h", "6h", "1d"],
  "30d": ["6h", "1d"],
  all: ["1d"],
};
const DEFAULT_GRANULARITY_BY_PERIOD: Record<StatsPeriod, StatsGranularity> = {
  "24h": "1h",
  "7d": "6h",
  "30d": "1d",
  all: "1d",
};
const OUTCOME_ORDER: ServiceOutcome[] = [
  "completed",
  "interrupted",
  "never_started",
  "abandoned_by_client",
  "unknown",
];
const OUTCOME_META: Record<
  ServiceOutcome,
  {
    label: string;
    icon: string;
    variant?: StatVariantValue;
    chartClass: string;
  }
> = {
  completed: {
    label: "Completed",
    icon: "OK",
    variant: "success",
    chartClass: "bg-success",
  },
  interrupted: {
    label: "Interrupted",
    icon: "!!",
    variant: "danger",
    chartClass: "bg-danger",
  },
  never_started: {
    label: "Never Started",
    icon: "NS",
    variant: "warning",
    chartClass: "bg-warning",
  },
  abandoned_by_client: {
    label: "Abandoned",
    icon: "AC",
    chartClass: "bg-info",
  },
  unknown: {
    label: "Unknown",
    icon: "??",
    variant: "warning",
    chartClass: "bg-bg-tertiary",
  },
};
const CHART_MIN_WIDTH_PX = 360;
const CHART_BUCKET_WIDTH_PX = 6;
const CHART_BUCKET_GAP_PX = 1;

interface LogStatsGridProps {
  stats: StatsResponse | null;
  statsLoading: boolean;
  params: StatsParams;
  onParamsChange: (params: StatsParams) => void;
  hasActiveFilters: boolean;
}

function getOutcomeCount(
  stats: StatsResponse | null,
  outcome: ServiceOutcome,
): string {
  return stats?.outcome_counts?.[outcome]?.toLocaleString() ?? "-";
}

function getStatsPeriodLabel(period: StatsPeriod): string {
  return PERIOD_LABELS[period];
}

function getGranularityLabel(granularity: StatsGranularity): string {
  return GRANULARITY_LABELS[granularity];
}

function isGranularityAllowed(
  period: StatsPeriod,
  granularity: StatsGranularity | undefined,
): granularity is StatsGranularity {
  if (!granularity) {
    return false;
  }

  return GRANULARITY_OPTIONS_BY_PERIOD[period].includes(granularity);
}

function getSelectedPeriod(params: StatsParams): StatsPeriod {
  return params.period ?? DEFAULT_STATS_PERIOD;
}

function getSelectedGranularity(
  period: StatsPeriod,
  params: StatsParams,
): StatsGranularity {
  if (isGranularityAllowed(period, params.granularity)) {
    return params.granularity;
  }

  return DEFAULT_GRANULARITY_BY_PERIOD[period];
}

function formatRangeLabel(
  timeRange: StatsResponse["time_range"] | undefined,
  period: StatsPeriod,
): string {
  if (!timeRange) {
    return "Loading time window";
  }

  const dateStyle: Intl.DateTimeFormatOptions =
    period === "24h"
      ? { month: "short", day: "numeric", hour: "numeric" }
      : { month: "short", day: "numeric" };
  const formatter = new Intl.DateTimeFormat(undefined, dateStyle);

  return `${formatter.format(new Date(timeRange.start))} to ${formatter.format(new Date(timeRange.end))}`;
}

function formatPointLabel(time: string, granularity: StatsGranularity): string {
  const date = new Date(time);
  const options: Intl.DateTimeFormatOptions =
    granularity === "1d"
      ? { month: "short", day: "numeric" }
      : { month: "short", day: "numeric", hour: "numeric" };

  return new Intl.DateTimeFormat(undefined, options).format(date);
}

function formatPointTooltip(
  point: OutcomeTimeSeriesPoint,
  granularity: StatsGranularity,
): string {
  const outcomeSummary = OUTCOME_ORDER.map((outcome) => {
    const count = point.outcome_counts?.[outcome] ?? 0;
    if (count === 0) {
      return null;
    }

    return `${OUTCOME_META[outcome].label}: ${count.toLocaleString()}`;
  })
    .filter((value): value is string => value !== null)
    .join(", ");

  return [
    formatPointLabel(point.time, granularity),
    `Requests: ${point.total_requests.toLocaleString()}`,
    outcomeSummary || "No classified outcomes",
  ].join("\n");
}

export function LogStatsGrid({
  stats,
  statsLoading,
  params,
  onParamsChange,
  hasActiveFilters,
}: LogStatsGridProps) {
  const period = getSelectedPeriod(params);
  const granularity = getSelectedGranularity(period, params);
  const avgLatencyValue =
    stats?.avg_latency_ms !== undefined
      ? formatDuration(stats.avg_latency_ms, { smallestUnit: "ms" })
      : "-";
  const providerHealthValue = stats
    ? `${stats.providers.healthy}/${stats.providers.total}`
    : "-";
  const providerHealthSubtitle = stats
    ? `${stats.providers.unhealthy.toLocaleString()} unhealthy, ${stats.providers.disabled.toLocaleString()} disabled`
    : undefined;

  return (
    <section className="card p-4 space-y-4" aria-labelledby="log-stats-heading">
      <div className="flex flex-col gap-3 xl:flex-row xl:items-start xl:justify-between">
        <div className="space-y-1">
          <div className="flex items-center gap-2">
            <h2
              id="log-stats-heading"
              className="text-lg font-semibold text-text-primary"
            >
              Normalized Outcome Stats
            </h2>
            <span className="badge badge-success">service_outcome</span>
          </div>
          <p className="text-sm text-text-muted">
            Global normalized aggregates for {getStatsPeriodLabel(period)}.
            Legacy rows are excluded from these metrics and the trend chart.
          </p>
          <p className="text-xs text-text-muted">
            Window: {formatRangeLabel(stats?.time_range, period)}
          </p>
        </div>

        <div className="flex flex-wrap gap-3">
          <StatsSelect
            id="stats-period"
            label="Stats Window"
            value={period}
            options={PERIOD_OPTIONS}
            onChange={(value) => {
              const nextPeriod = value as StatsPeriod;
              onParamsChange({
                period: nextPeriod,
                granularity: DEFAULT_GRANULARITY_BY_PERIOD[nextPeriod],
              });
            }}
          />
          <StatsSelect
            id="stats-granularity"
            label="Bucket Size"
            value={granularity}
            options={GRANULARITY_OPTIONS_BY_PERIOD[period].map((value) => ({
              value,
              label: getGranularityLabel(value),
            }))}
            onChange={(value) =>
              onParamsChange({
                period,
                granularity: value as StatsGranularity,
              })
            }
          />
        </div>
      </div>

      {hasActiveFilters && (
        <div className="rounded-lg border border-warning-light bg-warning-light/50 p-3 text-sm text-warning-dark">
          Active table filters do not scope this stats panel yet. Use the stats
          window controls above to change the global normalized trend.
        </div>
      )}

      <div className="grid grid-cols-1 sm:grid-cols-2 xl:grid-cols-4 gap-4">
        <LogStatCard
          label="Requests"
          value={stats?.total_requests.toLocaleString() ?? "-"}
          icon="RQ"
          loading={statsLoading}
        />
        <LogStatCard
          label="Avg Latency"
          value={avgLatencyValue}
          icon="LT"
          loading={statsLoading}
        />
        <LogStatCard
          label="Healthy Providers"
          value={providerHealthValue}
          subtitle={providerHealthSubtitle}
          icon="PV"
          loading={statsLoading}
        />
        {OUTCOME_ORDER.map((outcome) => (
          <LogStatCard
            key={outcome}
            label={OUTCOME_META[outcome].label}
            value={getOutcomeCount(stats, outcome)}
            icon={OUTCOME_META[outcome].icon}
            loading={statsLoading}
            variant={OUTCOME_META[outcome].variant}
          />
        ))}
      </div>

      <OutcomeTimeSeriesChart
        points={stats?.outcome_timeseries ?? []}
        loading={statsLoading}
        period={period}
        granularity={granularity}
      />
    </section>
  );
}

interface StatsSelectProps {
  id: string;
  label: string;
  value: string;
  options: Array<{ value: string; label: string }>;
  onChange: (value: string) => void;
}

function StatsSelect({
  id,
  label,
  value,
  options,
  onChange,
}: StatsSelectProps) {
  return (
    <div className="flex flex-col gap-1">
      <label htmlFor={id} className="text-xs text-text-muted font-medium">
        {label}
      </label>
      <select
        id={id}
        value={value}
        onChange={(event) => onChange(event.target.value)}
        className="input input-sm"
      >
        {options.map((option) => (
          <option key={option.value} value={option.value}>
            {option.label}
          </option>
        ))}
      </select>
    </div>
  );
}

interface OutcomeTimeSeriesChartProps {
  points: OutcomeTimeSeriesPoint[];
  loading: boolean;
  period: StatsPeriod;
  granularity: StatsGranularity;
}

function OutcomeTimeSeriesChart({
  points,
  loading,
  period,
  granularity,
}: OutcomeTimeSeriesChartProps) {
  if (loading) {
    return (
      <div className="rounded-lg border border-border-light bg-bg-secondary p-4 space-y-3">
        <div className="h-5 w-40 rounded bg-gray-200 animate-pulse" />
        <div className="h-44 rounded bg-gray-200 animate-pulse" />
      </div>
    );
  }

  if (points.length === 0) {
    return (
      <div className="rounded-lg border border-dashed border-border bg-bg-secondary p-4 text-sm text-text-muted">
        No outcome time series is available for this window.
      </div>
    );
  }

  const chartWidth = Math.max(
    CHART_MIN_WIDTH_PX,
    points.length * (CHART_BUCKET_WIDTH_PX + CHART_BUCKET_GAP_PX),
  );
  const maxRequests = Math.max(
    ...points.map((point) => point.total_requests),
    1,
  );
  const firstPoint = points[0];
  const lastPoint = points[points.length - 1];

  return (
    <section className="space-y-3" aria-labelledby="log-stats-trend-heading">
      <div className="flex flex-col gap-1 lg:flex-row lg:items-end lg:justify-between">
        <div>
          <h3
            id="log-stats-trend-heading"
            className="text-sm font-semibold text-text-primary"
          >
            Outcome Trend
          </h3>
          <p className="text-xs text-text-muted">
            Each column is a {getGranularityLabel(granularity).toLowerCase()}{" "}
            bucket inside {getStatsPeriodLabel(period).toLowerCase()}. Peak
            bucket volume: {maxRequests.toLocaleString()} requests.
          </p>
        </div>
        <p className="text-xs text-text-muted">
          {formatPointLabel(firstPoint.time, granularity)} to{" "}
          {formatPointLabel(lastPoint.time, granularity)}
        </p>
      </div>

      <div className="rounded-lg border border-border-light bg-bg-secondary p-4 space-y-3">
        <div className="flex flex-wrap items-center gap-3 text-xs text-text-muted">
          {OUTCOME_ORDER.map((outcome) => (
            <span key={outcome} className="inline-flex items-center gap-2">
              <span
                className={`inline-block h-2.5 w-2.5 rounded-full ${OUTCOME_META[outcome].chartClass}`}
              />
              {OUTCOME_META[outcome].label}
            </span>
          ))}
        </div>

        <div className="overflow-x-auto pb-2">
          <div
            className="flex h-44 items-end gap-px"
            role="img"
            aria-label={`Outcome trend for ${getStatsPeriodLabel(period)} at ${getGranularityLabel(granularity)} granularity`}
            style={{ width: `${chartWidth}px` }}
          >
            {points.map((point) => (
              <div
                key={point.time}
                className="flex h-full flex-col-reverse justify-start overflow-hidden rounded-[2px] bg-bg-tertiary/40"
                style={{ width: `${CHART_BUCKET_WIDTH_PX}px` }}
                title={formatPointTooltip(point, granularity)}
              >
                {OUTCOME_ORDER.map((outcome) => {
                  const count = point.outcome_counts?.[outcome] ?? 0;
                  if (count === 0) {
                    return null;
                  }

                  return (
                    <div
                      key={outcome}
                      className={OUTCOME_META[outcome].chartClass}
                      style={{ height: `${(count / maxRequests) * 100}%` }}
                    />
                  );
                })}
              </div>
            ))}
          </div>
        </div>

        <div className="flex items-center justify-between text-xs text-text-muted">
          <span>{formatPointLabel(firstPoint.time, granularity)}</span>
          <span>{formatPointLabel(lastPoint.time, granularity)}</span>
        </div>
      </div>
    </section>
  );
}

interface LogStatCardProps {
  label: string;
  value: string;
  icon: string;
  loading?: boolean;
  subtitle?: string;
  variant?: StatVariantValue;
}

function LogStatCard({
  label,
  value,
  icon,
  loading,
  subtitle,
  variant,
}: LogStatCardProps) {
  const variantClass = getStatVariantClass(variant);

  return (
    <div className="card py-4">
      <div className="flex items-center gap-3">
        <span className="inline-flex h-10 w-10 items-center justify-center rounded-full bg-bg-secondary text-xs font-semibold text-text-muted">
          {icon}
        </span>
        <div className="min-w-0">
          <p className="text-xs text-text-muted">{label}</p>
          {loading ? (
            <div className="h-7 w-16 bg-gray-200 rounded animate-pulse mt-1" />
          ) : (
            <>
              <p className={`text-lg font-bold ${variantClass}`}>{value}</p>
              {subtitle && (
                <p className="text-xs text-text-muted mt-0.5">{subtitle}</p>
              )}
            </>
          )}
        </div>
      </div>
    </div>
  );
}
