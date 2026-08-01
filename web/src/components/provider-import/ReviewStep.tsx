import { useId, useState } from "react";
import type { Group, ProviderImportCandidate } from "../../api";
import {
  SUB2API_FIELD_MAPPING_NOTES,
  type ProviderImportDecision,
  type ProviderImportReviewDraft,
} from "../../lib/providerImport";
import type {
  ProviderImportCreateDraft,
  ProviderImportNewProviderDefaults,
  ProviderImportValidationError,
} from "../../lib/providerImportSettings";
import { ReviewStepAccountRow } from "./ReviewStepAccountRow";
import { NewProviderDefaultsPanel } from "./NewProviderDefaultsPanel";

const ACCOUNTS_PER_PAGE = 25;

type ReviewFilter = "all" | "selected" | "ready" | "existing" | "blocked";
type FilterCounts = Record<ReviewFilter, number>;

interface ReviewRow {
  candidate: ProviderImportCandidate;
  decision: ProviderImportDecision;
}

interface ReviewStepProps {
  preview: import("../../api").ProviderImportPreview;
  draft: ProviderImportReviewDraft;
  groups: Group[];
  validationErrors: ReadonlyMap<string, ProviderImportValidationError>;
  error: string | null;
  disabled?: boolean;
  onSetAction: (
    candidateId: string,
    action: ProviderImportDecision["action"],
  ) => void;
  onEditProvider: (
    candidateId: string,
    field: keyof ProviderImportCreateDraft,
    value: ProviderImportCreateDraft[keyof ProviderImportCreateDraft],
  ) => void;
  onApplyNewProviderDefaults: (
    defaults: ProviderImportNewProviderDefaults,
  ) => void;
  onSetGroup: (groupId: string | null) => void;
  onSetAcknowledgement: (acknowledged: boolean) => void;
  onSelectAllReady: () => void;
  onSelectAllExisting: () => void;
  onClearSelection: () => void;
}

const FILTER_OPTIONS: ReadonlyArray<{
  value: ReviewFilter;
  label: string;
}> = [
  { value: "all", label: "All" },
  { value: "selected", label: "Selected" },
  { value: "ready", label: "Ready" },
  { value: "existing", label: "Existing" },
  { value: "blocked", label: "Blocked" },
];

function isBlocked(candidate: ProviderImportCandidate): boolean {
  return candidate.status !== "ready" && candidate.status !== "existing";
}

function matchesFilter(row: ReviewRow, filter: ReviewFilter): boolean {
  switch (filter) {
    case "selected":
      return row.decision.action !== "skip";
    case "ready":
      return row.candidate.status === "ready";
    case "existing":
      return row.candidate.status === "existing";
    case "blocked":
      return isBlocked(row.candidate);
    case "all":
      return true;
  }
}

function formatPreviewExpiry(expiresAt: string): string {
  const expiry = new Date(expiresAt);
  if (Number.isNaN(expiry.getTime())) {
    return expiresAt.trim() || "Expiration time unavailable";
  }
  return new Intl.DateTimeFormat(undefined, {
    dateStyle: "medium",
    timeStyle: "short",
  }).format(expiry);
}

function formatAccountCount(count: number): string {
  return `${count} ${count === 1 ? "account" : "accounts"}`;
}

function ImportSummary({
  accountCount,
  filterCounts,
}: {
  accountCount: number;
  filterCounts: FilterCounts;
}) {
  return (
    <dl
      className="grid grid-cols-2 gap-3 sm:grid-cols-4"
      aria-label="Import summary"
    >
      {[
        ["Accounts", accountCount],
        ["Ready", filterCounts.ready],
        ["Existing", filterCounts.existing],
        ["Blocked", filterCounts.blocked],
      ].map(([label, count]) => (
        <div
          key={label}
          className="flex flex-col rounded-lg border border-border bg-bg-secondary p-3"
        >
          <dt className="order-2 text-xs text-text-muted">{label}</dt>
          <dd className="order-1 text-xl font-semibold text-text-primary">
            {count}
          </dd>
        </div>
      ))}
    </dl>
  );
}

function Pagination({
  page,
  totalPages,
  totalItems,
  onPageChange,
}: {
  page: number;
  totalPages: number;
  totalItems: number;
  onPageChange: (page: number) => void;
}) {
  if (totalItems <= ACCOUNTS_PER_PAGE) return null;

  const firstItem = (page - 1) * ACCOUNTS_PER_PAGE + 1;
  const lastItem = Math.min(page * ACCOUNTS_PER_PAGE, totalItems);

  return (
    <nav
      className="mt-3 flex flex-wrap items-center justify-between gap-3"
      aria-label="Account result pages"
    >
      <p className="text-xs text-text-muted" aria-live="polite">
        Showing {firstItem}–{lastItem} of {totalItems} accounts
      </p>
      <div className="flex items-center gap-2">
        <button
          type="button"
          className="btn btn-secondary"
          disabled={page === 1}
          onClick={() => onPageChange(page - 1)}
        >
          Previous
        </button>
        <span className="text-sm text-text-secondary">
          Page {page} of {totalPages}
        </span>
        <button
          type="button"
          className="btn btn-secondary"
          disabled={page === totalPages}
          onClick={() => onPageChange(page + 1)}
        >
          Next
        </button>
      </div>
    </nav>
  );
}

function StepError({ error }: { error: string | null }) {
  if (!error) return null;
  return (
    <div
      role="alert"
      tabIndex={-1}
      data-step-error
      className="rounded-lg border border-danger/20 bg-danger/5 p-3 text-sm text-danger"
    >
      {error}
    </div>
  );
}

export function ReviewStep({
  preview,
  draft,
  groups,
  validationErrors,
  error,
  disabled = false,
  onSetAction,
  onEditProvider,
  onApplyNewProviderDefaults,
  onSetGroup,
  onSetAcknowledgement,
  onSelectAllReady,
  onSelectAllExisting,
  onClearSelection,
}: ReviewStepProps) {
  const accountsHeadingId = useId();
  const [filter, setFilter] = useState<ReviewFilter>("all");
  const [requestedPage, setRequestedPage] = useState(1);
  const decisionsById = new Map(
    draft.decisions.map((decision) => [decision.candidateId, decision]),
  );
  const rows = preview.items.flatMap((candidate) => {
    const decision = decisionsById.get(candidate.candidate_id);
    return decision ? [{ candidate, decision }] : [];
  });
  const filterCounts: FilterCounts = {
    all: rows.length,
    selected: rows.filter((row) => matchesFilter(row, "selected")).length,
    ready: rows.filter((row) => matchesFilter(row, "ready")).length,
    existing: rows.filter((row) => matchesFilter(row, "existing")).length,
    blocked: rows.filter((row) => matchesFilter(row, "blocked")).length,
  };
  const filteredRows = rows.filter((row) => matchesFilter(row, filter));
  const totalPages = Math.max(
    1,
    Math.ceil(filteredRows.length / ACCOUNTS_PER_PAGE),
  );
  const page = Math.min(requestedPage, totalPages);
  const firstRowIndex = (page - 1) * ACCOUNTS_PER_PAGE;
  const visibleRows = filteredRows.slice(
    firstRowIndex,
    firstRowIndex + ACCOUNTS_PER_PAGE,
  );
  const activeFilter = FILTER_OPTIONS.find((option) => option.value === filter);
  return (
    <div className="space-y-5">
      <StepError error={error} />

      <ImportSummary
        accountCount={preview.items.length}
        filterCounts={filterCounts}
      />

      {preview.warnings.length > 0 && (
        <div className="rounded-lg border border-warning/20 bg-warning-light/40 p-4">
          <h3 className="text-sm font-semibold text-text-primary">
            Import notes
          </h3>
          <ul className="mt-2 space-y-1 text-sm text-text-secondary">
            {preview.warnings.map((warning) => (
              <li key={`${warning.code}:${warning.message}`}>
                • {warning.message}
              </li>
            ))}
          </ul>
        </div>
      )}

      <details className="rounded-lg border border-border bg-bg-secondary/60 p-4">
        <summary className="cursor-pointer text-sm font-medium text-text-primary">
          sub2api field mapping details
        </summary>
        <p className="mt-3 text-sm text-text-secondary">
          Account name, OAuth credentials, priority, and concurrency are mapped
          to each new GPT provider.
        </p>
        <ul className="mt-2 space-y-1 text-xs text-text-muted">
          {SUB2API_FIELD_MAPPING_NOTES.map((item) => (
            <li key={item.field}>
              <code>{item.field}</code>: {item.reason}
            </li>
          ))}
        </ul>
      </details>

      {filterCounts.ready > 0 && (
        <NewProviderDefaultsPanel
          defaults={draft.newProviderDefaults}
          groupId={draft.groupId}
          groups={groups}
          newProviderCount={filterCounts.ready}
          disabled={disabled}
          onSetGroup={onSetGroup}
          onApply={onApplyNewProviderDefaults}
        />
      )}

      <div className="flex flex-col gap-3 rounded-lg border border-border p-4 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <p className="text-sm font-medium text-text-primary">
            Account selection
          </p>
          <p className="mt-1 text-xs text-text-muted">
            Existing accounts only rotate credentials; their provider settings
            stay unchanged.
          </p>
        </div>
        <div
          className="flex flex-wrap gap-2"
          role="group"
          aria-label="Bulk selection"
        >
          <button
            type="button"
            onClick={() => {
              setRequestedPage(1);
              onSelectAllReady();
            }}
            disabled={disabled || filterCounts.ready === 0}
            className="btn btn-secondary"
          >
            Select ready
          </button>
          <button
            type="button"
            onClick={() => {
              setRequestedPage(1);
              onSelectAllExisting();
            }}
            disabled={disabled || filterCounts.existing === 0}
            className="btn btn-secondary"
          >
            Select existing updates
          </button>
          <button
            type="button"
            onClick={() => {
              setRequestedPage(1);
              onClearSelection();
            }}
            disabled={disabled || filterCounts.selected === 0}
            className="btn btn-secondary"
          >
            Clear
          </button>
        </div>
      </div>

      <section className="space-y-3" aria-labelledby={accountsHeadingId}>
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div>
            <h3
              id={accountsHeadingId}
              className="font-semibold text-text-primary"
            >
              Accounts
            </h3>
            <p className="mt-2 rounded-md border border-border bg-bg-secondary px-3 py-2 text-sm text-text-secondary">
              Preview expires{" "}
              <time
                dateTime={preview.expires_at}
                className="font-medium text-text-primary"
              >
                {formatPreviewExpiry(preview.expires_at)}
              </time>
            </p>
          </div>
          <p
            className="rounded-full bg-bg-secondary px-3 py-1 text-xs text-text-secondary"
            aria-live="polite"
            aria-atomic="true"
          >
            {filterCounts.selected} selected · {validationErrors.size}{" "}
            validation issues
          </p>
        </div>

        <label className="flex items-start gap-3 rounded-lg border border-warning/30 bg-warning-light/30 p-4">
          <input
            type="checkbox"
            checked={draft.acknowledgedRefreshTokenOwnership}
            disabled={disabled}
            onChange={(event) => onSetAcknowledgement(event.target.checked)}
            className="mt-0.5 h-4 w-4 rounded border-border text-primary focus:ring-primary"
          />
          <span className="text-sm text-text-secondary">
            <strong className="block text-text-primary">
              I understand the OAuth token ownership risk.
            </strong>
            Switch-A verifies selected tokens with OpenAI signing keys before
            saving. Stop sub2api from using these same accounts before
            importing; running both systems can rotate refresh tokens and sign
            one of them out.
          </span>
        </label>

        <div
          className="flex flex-wrap gap-2"
          role="group"
          aria-label="Filter accounts"
        >
          {FILTER_OPTIONS.map((option) => (
            <button
              key={option.value}
              type="button"
              className={`rounded-full border px-3 py-1.5 text-sm ${
                filter === option.value
                  ? "border-primary bg-primary-light text-primary"
                  : "border-border bg-white text-text-secondary"
              }`}
              aria-pressed={filter === option.value}
              aria-label={`${option.label}: ${formatAccountCount(filterCounts[option.value])}`}
              onClick={() => {
                setFilter(option.value);
                setRequestedPage(1);
              }}
            >
              {option.label}
              <span className="ml-1 text-xs">{filterCounts[option.value]}</span>
            </button>
          ))}
        </div>

        {visibleRows.length > 0 ? (
          <ul className="space-y-2" aria-label="Accounts to import">
            {visibleRows.map(({ candidate, decision }) => (
              <ReviewStepAccountRow
                key={candidate.candidate_id}
                candidate={candidate}
                decision={decision}
                validationError={validationErrors.get(candidate.candidate_id)}
                disabled={disabled}
                onSetAction={(action) =>
                  onSetAction(candidate.candidate_id, action)
                }
                onEditProvider={(field, value) =>
                  onEditProvider(candidate.candidate_id, field, value)
                }
              />
            ))}
          </ul>
        ) : (
          <p className="rounded-lg border border-dashed border-border p-6 text-center text-sm text-text-muted">
            No accounts match the {activeFilter?.label.toLowerCase()} filter.
          </p>
        )}

        <Pagination
          page={page}
          totalPages={totalPages}
          totalItems={filteredRows.length}
          onPageChange={setRequestedPage}
        />
      </section>
    </div>
  );
}
