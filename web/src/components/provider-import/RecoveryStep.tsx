import { AlertTriangle } from "lucide-react";
import type { ProviderImportRecoveryReason } from "../../lib/providerImport";

const RECOVERY_COPY: Record<
  ProviderImportRecoveryReason,
  { title: string; message: string }
> = {
  stale: {
    title: "Preview is out of date",
    message:
      "Provider assignments changed after this preview was created, so it can no longer be committed.",
  },
  expired: {
    title: "Preview expired",
    message:
      "This short-lived preview has expired and can no longer be committed.",
  },
  unavailable: {
    title: "Preview is no longer available",
    message:
      "This preview may have expired, been cancelled, or been cleared by a server restart.",
  },
  committed_mismatch: {
    title: "A different selection was already committed",
    message:
      "Switch-A has a completed commit receipt for this preview, but it contains a different account selection.",
  },
};

export function RecoveryStep({
  reason,
}: {
  reason: ProviderImportRecoveryReason;
}) {
  const copy = RECOVERY_COPY[reason];
  return (
    <div
      role="alert"
      tabIndex={-1}
      data-step-error
      className="mx-auto max-w-xl rounded-xl border border-warning/30 bg-warning-light/30 p-6 text-center"
    >
      <div className="mx-auto flex h-12 w-12 items-center justify-center rounded-full bg-warning-light text-warning">
        <AlertTriangle className="h-6 w-6" aria-hidden="true" />
      </div>
      <h3 className="mt-4 text-lg font-semibold text-text-primary">
        {copy.title}
      </h3>
      <p className="mt-2 text-sm leading-relaxed text-text-secondary">
        {copy.message} Check Providers for any applied changes, then choose the
        export file again to build a fresh preview.
      </p>
    </div>
  );
}
