import { formatTokenLocale, parseTokenBigInt } from "./token-format";

export interface StackedBarSegment {
  key: string;
  label: string;
  value: string | number | bigint;
  bgClass: string;
  textClass?: string;
}

interface TokenMicroStackedBarProps {
  segments: StackedBarSegment[];
  totalValue?: string | number | bigint;
  heightClass?: string;
  className?: string;
}

export function TokenMicroStackedBar({
  segments,
  totalValue,
  heightClass = "h-2",
  className = "",
}: TokenMicroStackedBarProps) {
  const segmentBigInts = segments.map((seg) => ({
    ...seg,
    bigIntValue: parseTokenBigInt(seg.value),
  }));

  const calculatedTotal =
    totalValue !== undefined
      ? parseTokenBigInt(totalValue)
      : segmentBigInts.reduce((acc, seg) => acc + seg.bigIntValue, 0n);

  const activeSegments = segmentBigInts.filter((seg) => seg.bigIntValue > 0n);

  if (calculatedTotal === 0n || activeSegments.length === 0) {
    return (
      <div
        className={`w-full ${heightClass} bg-slate-100 dark:bg-slate-800 rounded-full overflow-hidden ${className}`}
        aria-hidden="true"
      />
    );
  }

  return (
    <div
      className={`w-full ${heightClass} bg-slate-100 dark:bg-slate-800 rounded-full overflow-hidden flex ${className}`}
      role="progressbar"
      aria-valuenow={100}
      aria-valuemin={0}
      aria-valuemax={100}
    >
      {activeSegments.map((seg) => {
        // Calculate percentage with precision
        const basisPoints = (seg.bigIntValue * 10000n) / calculatedTotal;
        const percent = Number(basisPoints) / 100;
        const formattedLocale = formatTokenLocale(seg.bigIntValue);
        const title = `${seg.label}: ${formattedLocale} (${percent.toFixed(1)}%)`;

        return (
          <div
            key={seg.key}
            className={`${seg.bgClass} transition-all duration-300 relative group cursor-pointer hover:opacity-90`}
            style={{ width: `${percent}%` }}
            title={title}
            aria-label={title}
          />
        );
      })}
    </div>
  );
}
