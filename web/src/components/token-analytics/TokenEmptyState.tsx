import { BarChart2 } from "lucide-react";

export function TokenEmptyState() {
  return (
    <div className="rounded-2xl border border-dashed border-slate-200 dark:border-slate-800 bg-white/50 dark:bg-slate-900/50 p-12 text-center space-y-3">
      <div className="mx-auto w-12 h-12 rounded-2xl bg-indigo-50 dark:bg-indigo-950/40 text-indigo-600 dark:text-indigo-400 flex items-center justify-center">
        <BarChart2 className="w-6 h-6" aria-hidden="true" />
      </div>
      <div className="space-y-1">
        <h4 className="text-sm font-semibold text-slate-800 dark:text-slate-200">
          No Token Telemetry Recorded
        </h4>
        <p className="text-xs text-slate-500 dark:text-slate-400 max-w-sm mx-auto">
          No token usage was observed for comparable requests within this time
          window. Try expanding the time range above.
        </p>
      </div>
    </div>
  );
}
