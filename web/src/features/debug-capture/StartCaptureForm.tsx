import { useState, type FormEvent } from "react";
import { AlertTriangle, Play, RefreshCw } from "lucide-react";
import type { StartDebugCaptureRequest } from "@/api";
import { useProviders } from "@/hooks/useProviders";
import { formatBytes } from "./presentation";

const BYTES_PER_MIB = 1_024 * 1_024;
const DEFAULT_RECORDS_PER_PROVIDER = 10;
const DEFAULT_RETAINED_MIB = 256;
const MINIMUM_POSITIVE_VALUE = 1;

interface StartCaptureFormProps {
  processCeilingBytes: number;
  starting: boolean;
  onStart: (request: StartDebugCaptureRequest) => Promise<void>;
}

export function StartCaptureForm({
  processCeilingBytes,
  starting,
  onStart,
}: StartCaptureFormProps) {
  const { providers, loading, error, refetch } = useProviders();
  const [selectedProviderIds, setSelectedProviderIds] = useState<string[]>([]);
  const [recordsPerProvider, setRecordsPerProvider] = useState(
    DEFAULT_RECORDS_PER_PROVIDER,
  );
  const [retainedMiB, setRetainedMiB] = useState(DEFAULT_RETAINED_MIB);
  const [riskAcknowledged, setRiskAcknowledged] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);

  const toggleProvider = (providerId: string, checked: boolean) => {
    setSelectedProviderIds((current) =>
      checked
        ? [...current, providerId]
        : current.filter((id) => id !== providerId),
    );
  };

  const handleSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setSubmitError(null);

    if (selectedProviderIds.length === 0) {
      setSubmitError("Select at least one Provider.");
      return;
    }
    if (
      !Number.isInteger(recordsPerProvider) ||
      recordsPerProvider < MINIMUM_POSITIVE_VALUE
    ) {
      setSubmitError("Records per Provider must be a positive whole number.");
      return;
    }
    const retainedBytesLimit = retainedMiB * BYTES_PER_MIB;
    if (
      !Number.isInteger(retainedMiB) ||
      retainedMiB < MINIMUM_POSITIVE_VALUE ||
      retainedBytesLimit > processCeilingBytes
    ) {
      setSubmitError(
        `Retained memory must be a positive whole MiB no greater than ${formatBytes(processCeilingBytes)}.`,
      );
      return;
    }
    if (!riskAcknowledged) {
      setSubmitError("Acknowledge the raw payload risk before starting.");
      return;
    }

    try {
      await onStart({
        provider_ids: selectedProviderIds,
        completed_records_per_provider: recordsPerProvider,
        retained_bytes_limit: retainedBytesLimit,
        acknowledge_raw_payload_risk: true,
      });
    } catch (reason) {
      setSubmitError(
        reason instanceof Error ? reason.message : "Unable to start capture.",
      );
    }
  };

  return (
    <form className="space-y-6" onSubmit={handleSubmit}>
      <section className="card space-y-5" aria-labelledby="capture-scope-title">
        <div>
          <h3
            id="capture-scope-title"
            className="text-lg font-semibold text-text-primary"
          >
            Capture scope
          </h3>
          <p className="mt-1 text-sm text-text-secondary">
            Only selected Providers retain raw exchanges. Routing and proxy
            behavior are unchanged.
          </p>
        </div>

        {error && (
          <div
            role="alert"
            className="flex items-center justify-between gap-3 rounded-lg border border-danger/20 bg-danger-light p-3 text-sm text-danger"
          >
            <span>{error.message}</span>
            <button
              type="button"
              className="btn btn-secondary btn-sm"
              onClick={() => void refetch()}
            >
              <RefreshCw className="h-4 w-4" />
              Retry
            </button>
          </div>
        )}

        <fieldset>
          <legend className="mb-2 text-sm font-medium text-text-primary">
            Providers
          </legend>
          {loading && providers.length === 0 && (
            <p role="status" className="text-sm text-text-secondary">
              Loading Providers…
            </p>
          )}
          {!loading && providers.length === 0 && (
            <p className="rounded-lg bg-bg-secondary p-4 text-sm text-text-secondary">
              No Providers are configured.
            </p>
          )}
          {providers.length > 0 && (
            <div className="grid gap-2 sm:grid-cols-2">
              {providers.map((provider) => (
                <label
                  key={provider.id}
                  className="flex cursor-pointer items-start gap-3 rounded-lg border border-border p-3 hover:bg-bg-hover"
                >
                  <input
                    type="checkbox"
                    checked={selectedProviderIds.includes(provider.id)}
                    onChange={(event) =>
                      toggleProvider(provider.id, event.target.checked)
                    }
                  />
                  <span className="min-w-0">
                    <span className="block truncate text-sm font-medium text-text-primary">
                      {provider.name}
                    </span>
                    <span className="block truncate text-xs text-text-muted">
                      {provider.id}
                      {!provider.enabled ? " · disabled" : ""}
                    </span>
                  </span>
                </label>
              ))}
            </div>
          )}
        </fieldset>

        <div className="grid gap-4 sm:grid-cols-2">
          <label className="text-sm font-medium text-text-primary">
            Completed records per Provider
            <input
              className="input mt-2"
              type="number"
              min={MINIMUM_POSITIVE_VALUE}
              step={MINIMUM_POSITIVE_VALUE}
              value={recordsPerProvider}
              onChange={(event) =>
                setRecordsPerProvider(Number(event.target.value))
              }
            />
          </label>
          <label className="text-sm font-medium text-text-primary">
            Retained memory limit (MiB)
            <input
              className="input mt-2"
              type="number"
              min={MINIMUM_POSITIVE_VALUE}
              max={Math.max(
                MINIMUM_POSITIVE_VALUE,
                Math.floor(processCeilingBytes / BYTES_PER_MIB),
              )}
              step={MINIMUM_POSITIVE_VALUE}
              value={retainedMiB}
              onChange={(event) => setRetainedMiB(Number(event.target.value))}
            />
            <span className="mt-1 block text-xs font-normal text-text-muted">
              Process ceiling: {formatBytes(processCeilingBytes)}
            </span>
          </label>
        </div>
      </section>

      <section className="rounded-xl border border-warning/30 bg-warning-light p-5">
        <div className="flex items-start gap-3">
          <AlertTriangle
            className="mt-0.5 h-5 w-5 shrink-0 text-warning"
            aria-hidden="true"
          />
          <div>
            <h3 className="font-semibold text-text-primary">
              Raw payload warning
            </h3>
            <p className="mt-1 text-sm text-text-secondary">
              Captures contain raw prompts, model output, and upstream bodies.
              They may include secrets or personal data.
            </p>
            <label className="mt-4 flex cursor-pointer items-start gap-3 text-sm font-medium text-text-primary">
              <input
                type="checkbox"
                checked={riskAcknowledged}
                onChange={(event) => setRiskAcknowledged(event.target.checked)}
              />
              I understand and accept the raw payload risk.
            </label>
          </div>
        </div>
      </section>

      {submitError && (
        <p role="alert" className="text-sm font-medium text-danger">
          {submitError}
        </p>
      )}

      <button
        type="submit"
        className="btn btn-primary"
        disabled={starting || loading || providers.length === 0}
      >
        <Play className="h-4 w-4" aria-hidden="true" />
        {starting ? "Starting…" : "Start Debug Capture"}
      </button>
    </form>
  );
}
