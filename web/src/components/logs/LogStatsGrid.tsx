import type { StatsResponse } from "../../api/types";
import { formatDuration } from "../../lib/utils";
import {
  getSuccessRateVariant,
  getErrorCountVariant,
  getStatVariantClass,
  type StatVariantValue,
} from "./utils";

interface LogStatsGridProps {
  total: number;
  stats: StatsResponse | null;
  loading: boolean;
  statsLoading: boolean;
}

export function LogStatsGrid({
  total,
  stats,
  loading,
  statsLoading,
}: LogStatsGridProps) {
  const successRateValue =
    stats?.success_rate !== undefined
      ? `${(stats.success_rate * 100).toFixed(1)}%`
      : "-";

  const avgLatencyValue =
    stats?.avg_latency_ms !== undefined
      ? formatDuration(stats.avg_latency_ms, { smallestUnit: "ms" })
      : "-";

  const errorCountValue = stats?.fail_count?.toLocaleString() ?? "-";

  return (
    <div className="grid grid-cols-1 sm:grid-cols-2 md:grid-cols-4 gap-4">
      <LogStatCard
        label="Total Logs"
        value={total.toLocaleString()}
        icon="📊"
        loading={loading && total === 0}
      />
      <LogStatCard
        label="Success Rate (24h)"
        value={successRateValue}
        icon="✅"
        loading={statsLoading}
        variant={getSuccessRateVariant(stats?.success_rate)}
      />
      <LogStatCard
        label="Avg Latency (24h)"
        value={avgLatencyValue}
        icon="⚡"
        loading={statsLoading}
      />
      <LogStatCard
        label="Errors (24h)"
        value={errorCountValue}
        icon="⚠️"
        loading={statsLoading}
        variant={getErrorCountVariant(stats?.fail_count)}
      />
    </div>
  );
}

interface LogStatCardProps {
  label: string;
  value: string;
  icon: string;
  loading?: boolean;
  variant?: StatVariantValue;
}

function LogStatCard({
  label,
  value,
  icon,
  loading,
  variant,
}: LogStatCardProps) {
  const variantClass = getStatVariantClass(variant);

  return (
    <div className="card py-4">
      <div className="flex items-center gap-3">
        <span className="text-xl">{icon}</span>
        <div>
          <p className="text-xs text-text-muted">{label}</p>
          {loading ? (
            <div className="h-7 w-16 bg-gray-200 rounded animate-pulse mt-1" />
          ) : (
            <p className={`text-lg font-bold ${variantClass}`}>{value}</p>
          )}
        </div>
      </div>
    </div>
  );
}
