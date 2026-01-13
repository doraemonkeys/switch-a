import { REFRESH_INTERVALS } from "./refreshIntervalConstants";

interface RefreshIntervalSelectProps {
  value: number;
  onChange: (interval: number) => void;
  showLabel?: boolean;
  className?: string;
}

export function RefreshIntervalSelect({
  value,
  onChange,
  showLabel = false,
  className = "",
}: RefreshIntervalSelectProps) {
  return (
    <select
      value={value}
      onChange={(e) => onChange(Number(e.target.value))}
      className={`text-xs border-border rounded-md py-1 px-2 bg-bg-secondary focus:ring-primary focus:border-primary ${className}`}
    >
      {REFRESH_INTERVALS.map((interval) => (
        <option key={interval.value} value={interval.value}>
          {showLabel && interval.value === 0
            ? "Auto-refresh: Off"
            : interval.label}
        </option>
      ))}
    </select>
  );
}
