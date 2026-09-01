import { KeyRound, Plus, RefreshCw } from "lucide-react";

interface CredentialHeroHeaderProps {
  loading: boolean;
  onRefresh: () => void;
  onAddCredential: () => void;
}

export function CredentialHeroHeader({
  loading,
  onRefresh,
  onAddCredential,
}: CredentialHeroHeaderProps) {
  return (
    <header className="relative overflow-hidden rounded-3xl border border-border bg-white p-6 shadow-xs sm:flex sm:items-center sm:justify-between sm:p-8">
      <div className="relative z-10 max-w-xl">
        <div className="flex items-center gap-2">
          <span className="inline-flex items-center gap-1 rounded-full bg-primary-light px-2.5 py-0.5 text-xs font-semibold text-primary">
            <KeyRound className="h-3.5 w-3.5" />
            Security Vault
          </span>
        </div>
        <h1 className="mt-2 text-2xl font-extrabold tracking-tight text-text-primary sm:text-3xl">
          Credentials Management
        </h1>
        <p className="mt-2 text-sm text-text-secondary leading-relaxed">
          Central repository for reusable upstream API keys and ChatGPT OAuth
          sessions. Rotate keys in place, inspect token health, and
          re-authenticate sessions across all bound routes.
        </p>
      </div>

      <div className="relative z-10 mt-5 flex flex-wrap items-center gap-3 sm:mt-0">
        <button
          type="button"
          className="btn btn-secondary h-10 px-4 text-sm"
          disabled={loading}
          onClick={onRefresh}
          aria-label="Refresh credentials"
        >
          <RefreshCw className={`h-4 w-4 ${loading ? "animate-spin" : ""}`} />
          Refresh
        </button>
        <button
          type="button"
          className="btn btn-primary h-10 px-4 text-sm shadow-xs"
          onClick={onAddCredential}
        >
          <Plus className="h-4 w-4" />
          Add Credential
        </button>
      </div>
    </header>
  );
}
