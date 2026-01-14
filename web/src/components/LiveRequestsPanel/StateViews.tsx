// =============================================================================
// State Views for LiveRequestsPanel
// =============================================================================

interface ErrorStateProps {
  error: Error;
}

export function ErrorState({ error }: ErrorStateProps) {
  return (
    <div className="p-4 text-center text-red-500 dark:text-red-400">
      Failed to load active requests: {error.message}
    </div>
  );
}

export function LoadingState() {
  return (
    <div className="p-8 text-center">
      <div className="inline-block animate-spin rounded-full h-8 w-8 border-b-2 border-accent-primary" />
      <p className="mt-2 text-text-secondary">Loading active requests...</p>
    </div>
  );
}

export function EmptyState() {
  return (
    <div className="p-8 text-center text-text-secondary">
      <svg
        className="mx-auto h-12 w-12 text-text-muted mb-4"
        fill="none"
        stroke="currentColor"
        viewBox="0 0 24 24"
        aria-hidden="true"
      >
        <path
          strokeLinecap="round"
          strokeLinejoin="round"
          strokeWidth={1.5}
          d="M20 7l-8-4-8 4m16 0l-8 4m8-4v10l-8 4m0-10L4 7m8 4v10M4 7v10l8 4"
        />
      </svg>
      <p className="text-lg font-medium text-text-primary">
        No active requests
      </p>
      <p className="mt-1 text-sm">
        Requests will appear here when they are in progress
      </p>
    </div>
  );
}
