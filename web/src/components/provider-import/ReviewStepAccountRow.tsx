import { useState } from "react";
import type { ProviderImportCandidate } from "../../api";
import {
  PROVIDER_IMPORT_MAX_PROVIDER_ID_LENGTH,
  PROVIDER_IMPORT_MAX_PROVIDER_NAME_LENGTH,
  PROVIDER_IMPORT_MAX_SCHEDULING_VALUE,
} from "../../lib/providerImport";
import type {
  ProviderImportCreateDraft,
  ProviderImportDecision,
  ProviderImportValidationError,
} from "../../lib/providerImport";

const ACCOUNT_ID_VISIBLE_CHARACTERS = 8;
const UNLIMITED_CONCURRENCY = 0;

const STATUS_PRESENTATION: Record<
  ProviderImportCandidate["status"],
  { label: string; className: string }
> = {
  ready: {
    label: "Ready",
    className: "bg-success-light text-success",
  },
  existing: {
    label: "Existing",
    className: "bg-warning-light text-warning",
  },
  duplicate: {
    label: "Duplicate",
    className: "bg-danger-light text-danger",
  },
  invalid: {
    label: "Invalid",
    className: "bg-danger-light text-danger",
  },
  unsupported: {
    label: "Unsupported",
    className: "bg-bg-tertiary text-text-secondary",
  },
};

interface ReviewStepAccountRowProps {
  candidate: ProviderImportCandidate;
  decision: ProviderImportDecision;
  validationError?: ProviderImportValidationError;
  disabled: boolean;
  onSetAction: (action: ProviderImportDecision["action"]) => void;
  onEditProvider: (
    field: keyof ProviderImportCreateDraft,
    value: string | number,
  ) => void;
}

function candidateLabel(candidate: ProviderImportCandidate): string {
  return (
    candidate.email || candidate.name || `Account ${candidate.source_index + 1}`
  );
}

function maskAccountId(accountId?: string): string | null {
  if (!accountId) return null;
  return accountId.length <= ACCOUNT_ID_VISIBLE_CHARACTERS
    ? accountId
    : `••••${accountId.slice(-ACCOUNT_ID_VISIBLE_CHARACTERS)}`;
}

function CandidateAction({
  candidate,
  decision,
  disabled,
  onSetAction,
}: Pick<
  ReviewStepAccountRowProps,
  "candidate" | "decision" | "disabled" | "onSetAction"
>) {
  const label = candidateLabel(candidate);

  if (candidate.status === "ready") {
    return (
      <label className="flex items-start gap-2 text-sm text-text-primary">
        <input
          type="checkbox"
          checked={decision.action === "create"}
          disabled={disabled}
          onChange={(event) =>
            onSetAction(event.target.checked ? "create" : "skip")
          }
          className="mt-0.5 h-4 w-4 rounded border-border text-primary focus:ring-primary"
        />
        <span>Create provider for {label}</span>
      </label>
    );
  }

  if (candidate.status === "existing") {
    return (
      <label className="flex items-start gap-2 text-sm text-text-primary">
        <input
          type="checkbox"
          checked={decision.action === "update"}
          disabled={disabled}
          onChange={(event) =>
            onSetAction(event.target.checked ? "update" : "skip")
          }
          className="mt-0.5 h-4 w-4 rounded border-border text-primary focus:ring-primary"
        />
        <span>
          Update credentials on{" "}
          {candidate.existing_provider_name ?? "existing provider"}
        </span>
      </label>
    );
  }

  return (
    <label className="flex items-start gap-2 text-sm text-text-muted">
      <input
        type="checkbox"
        checked={false}
        disabled
        className="mt-0.5 h-4 w-4"
      />
      <span>Cannot import {label}</span>
    </label>
  );
}

function CreateProviderFields({
  candidate,
  provider,
  validationError,
  disabled,
  selected,
  onEditProvider,
}: {
  candidate: ProviderImportCandidate;
  provider: ProviderImportCreateDraft;
  validationError?: ProviderImportValidationError;
  disabled: boolean;
  selected: boolean;
  onEditProvider: ReviewStepAccountRowProps["onEditProvider"];
}) {
  const label = candidateLabel(candidate);
  const fieldsDisabled = disabled || !selected;
  const validationId = `${candidate.candidate_id}-validation`;
  const isInvalid = (field: keyof ProviderImportCreateDraft) =>
    validationError?.field === field;

  return (
    <div className="mt-3 grid gap-3 border-t border-border pt-3 sm:grid-cols-2 lg:grid-cols-4">
      <label className="text-xs font-medium text-text-secondary sm:col-span-2">
        Provider name for {label}
        <input
          value={provider.name}
          maxLength={PROVIDER_IMPORT_MAX_PROVIDER_NAME_LENGTH}
          disabled={fieldsDisabled}
          onChange={(event) => onEditProvider("name", event.target.value)}
          className={`input mt-1 ${isInvalid("name") ? "border-danger" : ""}`}
          aria-invalid={isInvalid("name") || undefined}
          aria-describedby={isInvalid("name") ? validationId : undefined}
        />
      </label>
      <label className="text-xs font-medium text-text-secondary sm:col-span-2">
        Provider ID for {label}
        <input
          value={provider.providerId}
          maxLength={PROVIDER_IMPORT_MAX_PROVIDER_ID_LENGTH}
          disabled={fieldsDisabled}
          onChange={(event) => onEditProvider("providerId", event.target.value)}
          className={`input mt-1 ${isInvalid("providerId") ? "border-danger" : ""}`}
          aria-invalid={isInvalid("providerId") || undefined}
          aria-describedby={isInvalid("providerId") ? validationId : undefined}
        />
      </label>
      <label className="text-xs font-medium text-text-secondary">
        Priority for {label}
        <input
          type="number"
          min={0}
          max={PROVIDER_IMPORT_MAX_SCHEDULING_VALUE}
          value={provider.priority}
          disabled={fieldsDisabled}
          onChange={(event) =>
            onEditProvider("priority", Number(event.target.value))
          }
          className={`input mt-1 ${isInvalid("priority") ? "border-danger" : ""}`}
          aria-invalid={isInvalid("priority") || undefined}
          aria-describedby={isInvalid("priority") ? validationId : undefined}
        />
      </label>
      <label className="text-xs font-medium text-text-secondary">
        Concurrency for {label}
        <input
          type="number"
          min={0}
          max={PROVIDER_IMPORT_MAX_SCHEDULING_VALUE}
          value={provider.concurrency}
          disabled={fieldsDisabled}
          onChange={(event) =>
            onEditProvider("concurrency", Number(event.target.value))
          }
          className={`input mt-1 ${isInvalid("concurrency") ? "border-danger" : ""}`}
          aria-invalid={isInvalid("concurrency") || undefined}
          aria-describedby={isInvalid("concurrency") ? validationId : undefined}
        />
      </label>
      <p className="self-end text-xs text-text-muted sm:col-span-2">
        Source values are prefilled. Lower priority numbers route first;
        concurrency 0 is unlimited.
      </p>
      {validationError && (
        <p id={validationId} className="text-xs text-danger sm:col-span-4">
          {validationError.message}
        </p>
      )}
    </div>
  );
}

export function ReviewStepAccountRow({
  candidate,
  decision,
  validationError,
  disabled,
  onSetAction,
  onEditProvider,
}: ReviewStepAccountRowProps) {
  const [providerSettingsExpanded, setProviderSettingsExpanded] =
    useState(false);
  const label = candidateLabel(candidate);
  const accountId = maskAccountId(candidate.account_id);
  const presentation = STATUS_PRESENTATION[candidate.status];
  const provider = candidate.status === "ready" ? decision.provider : null;

  return (
    <li className="rounded-lg border border-border bg-white p-3 shadow-sm">
      <div className="flex flex-col gap-2 sm:flex-row sm:items-start sm:justify-between">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2">
            <h3 className="truncate text-sm font-medium text-text-primary">
              {label}
            </h3>
            <span
              className={`rounded-full px-2 py-0.5 text-xs font-semibold ${presentation.className}`}
            >
              {presentation.label}
            </span>
          </div>
          <p className="mt-0.5 text-xs text-text-muted">
            {[candidate.plan_type, accountId].filter(Boolean).join(" · ") ||
              "Identity read from OAuth token claims"}
          </p>
        </div>
        <CandidateAction
          candidate={candidate}
          decision={decision}
          disabled={disabled}
          onSetAction={onSetAction}
        />
      </div>

      {candidate.message && (
        <p className="mt-2 text-sm text-text-secondary">{candidate.message}</p>
      )}
      {candidate.warnings.length > 0 && (
        <ul className="mt-1 space-y-1 text-xs text-warning">
          {candidate.warnings.map((warning) => (
            <li
              key={`${candidate.candidate_id}:${warning.code}:${warning.message}`}
            >
              {warning.message}
            </li>
          ))}
        </ul>
      )}
      {candidate.status === "existing" && (
        <p className="mt-1 text-xs text-text-muted">
          Updating credentials preserves the existing provider’s routing, group,
          priority, and concurrency.
        </p>
      )}
      {provider && (
        <details
          className="mt-2 rounded-md bg-bg-secondary/70 px-3 py-2"
          open={providerSettingsExpanded || Boolean(validationError)}
          onToggle={(event) =>
            setProviderSettingsExpanded(event.currentTarget.open)
          }
        >
          <summary className="cursor-pointer text-xs font-medium text-text-primary">
            <span>Provider settings for {label}</span>
            <span className="ml-2 font-normal text-text-muted">
              Priority {provider.priority} · Concurrency{" "}
              {provider.concurrency === UNLIMITED_CONCURRENCY
                ? "unlimited"
                : provider.concurrency}
            </span>
            {validationError && (
              <span className="ml-2 text-danger">Needs attention</span>
            )}
          </summary>
          <CreateProviderFields
            candidate={candidate}
            provider={provider}
            validationError={validationError}
            disabled={disabled}
            selected={decision.action === "create"}
            onEditProvider={onEditProvider}
          />
        </details>
      )}
    </li>
  );
}
