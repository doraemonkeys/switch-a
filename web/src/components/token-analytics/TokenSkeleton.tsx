export function TokenSkeleton() {
  return (
    <div
      className="space-y-4 animate-pulse"
      aria-busy="true"
      aria-label="Loading token usage analytics"
    >
      {/* Hero cards skeleton */}
      <div className="grid grid-cols-1 sm:grid-cols-2 xl:grid-cols-4 gap-4">
        {[1, 2, 3, 4].map((i) => (
          <div
            key={i}
            className="bg-white dark:bg-slate-900 border border-slate-200/80 dark:border-slate-800 rounded-2xl p-5 space-y-4 shadow-xs"
          >
            <div className="flex items-center justify-between">
              <div className="h-4 w-24 bg-slate-200 dark:bg-slate-800 rounded" />
              <div className="h-8 w-8 bg-slate-200 dark:bg-slate-800 rounded-xl" />
            </div>
            <div className="space-y-1.5">
              <div className="h-8 w-32 bg-slate-200 dark:bg-slate-800 rounded" />
              <div className="h-3 w-28 bg-slate-200 dark:bg-slate-800 rounded" />
            </div>
            <div className="h-2 w-full bg-slate-200 dark:bg-slate-800 rounded-full" />
            <div className="border-t border-slate-100 dark:border-slate-800 pt-3 space-y-1.5">
              <div className="h-3 w-full bg-slate-200 dark:bg-slate-800 rounded" />
              <div className="h-3 w-3/4 bg-slate-200 dark:bg-slate-800 rounded" />
            </div>
          </div>
        ))}
      </div>

      {/* Trend chart skeleton */}
      <div className="bg-white dark:bg-slate-900 border border-slate-200/80 dark:border-slate-800 rounded-2xl p-5 space-y-4 shadow-xs">
        <div className="flex items-center justify-between">
          <div className="h-5 w-48 bg-slate-200 dark:bg-slate-800 rounded" />
          <div className="h-6 w-40 bg-slate-200 dark:bg-slate-800 rounded-lg" />
        </div>
        <div className="h-52 bg-slate-100 dark:bg-slate-800/50 rounded-xl" />
      </div>

      {/* Top breakdown skeleton */}
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
        {[1, 2].map((i) => (
          <div
            key={i}
            className="bg-white dark:bg-slate-900 border border-slate-200/80 dark:border-slate-800 rounded-2xl p-5 space-y-4 shadow-xs"
          >
            <div className="h-5 w-32 bg-slate-200 dark:bg-slate-800 rounded" />
            <div className="space-y-3">
              {[1, 2, 3].map((j) => (
                <div key={j} className="space-y-1.5">
                  <div className="flex justify-between">
                    <div className="h-3.5 w-28 bg-slate-200 dark:bg-slate-800 rounded" />
                    <div className="h-3.5 w-20 bg-slate-200 dark:bg-slate-800 rounded" />
                  </div>
                  <div className="h-2.5 w-full bg-slate-200 dark:bg-slate-800 rounded-full" />
                </div>
              ))}
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
