import { KeyRound, Plus, RefreshCw, SearchX } from "lucide-react";

interface CredentialEmptyStateProps {
  isFiltered: boolean;
  onResetFilters?: () => void;
  onCreateCredential?: () => void;
}

export function CredentialEmptyState({
  isFiltered,
  onResetFilters,
  onCreateCredential,
}: CredentialEmptyStateProps) {
  if (isFiltered) {
    return (
      <div className="flex flex-col items-center justify-center rounded-3xl border border-dashed border-border bg-white/60 p-12 text-center shadow-xs">
        <div className="flex h-16 w-16 items-center justify-center rounded-2xl bg-bg-secondary text-text-muted">
          <SearchX className="h-8 w-8" />
        </div>
        <h3 className="mt-4 text-base font-semibold text-text-primary">
          No matching credentials found
        </h3>
        <p className="mt-1.5 max-w-sm text-sm text-text-secondary">
          We couldn't find any credentials matching your search or active filter
          criteria.
        </p>
        {onResetFilters && (
          <button
            type="button"
            onClick={onResetFilters}
            className="btn btn-secondary mt-5 h-9 px-4 text-xs"
          >
            <RefreshCw className="h-3.5 w-3.5" />
            Reset all filters
          </button>
        )}
      </div>
    );
  }

  return (
    <div className="flex flex-col items-center justify-center rounded-3xl border border-dashed border-border bg-white p-16 text-center shadow-xs">
      {/* Decorative Vector Graphic */}
      <div className="relative mb-3 flex h-20 w-20 items-center justify-center rounded-3xl bg-linear-to-tr from-primary/10 via-primary-light to-indigo-100 text-primary">
        <KeyRound className="h-10 w-10" />
        <div className="absolute -bottom-1 -right-1 flex h-6 w-6 items-center justify-center rounded-full bg-emerald-500 text-white shadow-xs">
          <Plus className="h-3.5 w-3.5" />
        </div>
      </div>
      <h3 className="mt-3 text-lg font-bold tracking-tight text-text-primary">
        No Credentials Configured
      </h3>
      <p className="mt-1.5 max-w-md text-sm text-text-secondary leading-relaxed">
        Credentials store shared API keys and authenticated ChatGPT sessions
        that routes link to. Create an API key or attach one to a provider to
        begin routing requests.
      </p>
      {onCreateCredential && (
        <button
          type="button"
          onClick={onCreateCredential}
          className="btn btn-primary mt-6 h-10 px-5 text-sm"
        >
          <Plus className="h-4 w-4" />
          Add Your First Credential
        </button>
      )}
    </div>
  );
}
