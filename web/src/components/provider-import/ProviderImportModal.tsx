import { useEffect, useId, useRef } from "react";
import { X } from "lucide-react";
import type { Group, ProviderImportCommitResult } from "../../api";
import {
  useProviderImportFlow,
  type ProviderImportGateway,
} from "../../hooks/useProviderImportFlow";
import { UploadStep } from "./UploadStep";
import { ReviewStep } from "./ReviewStep";
import { ResultStep } from "./ResultStep";
import { RecoveryStep } from "./RecoveryStep";

const FOCUSABLE_SELECTOR = [
  "button:not([disabled])",
  "[href]",
  "input:not([disabled])",
  "select:not([disabled])",
  "textarea:not([disabled])",
  '[tabindex]:not([tabindex="-1"])',
].join(",");

export interface ProviderImportModalProps {
  gateway: ProviderImportGateway;
  existingProviderIds: readonly string[];
  groups: Group[];
  onClose: () => void;
  onCheckProviders: () => void;
  onCommitted: (result: ProviderImportCommitResult) => void | Promise<void>;
}

function phaseSubtitle(
  phase: ReturnType<typeof useProviderImportFlow>["state"]["phase"],
) {
  switch (phase) {
    case "upload":
      return "Choose a sub2api account export";
    case "previewing":
      return "Building a credential-safe preview";
    case "review":
      return "Choose accounts and resolve existing providers";
    case "committing":
      return "Applying the selected changes atomically";
    case "result":
      return "Import complete";
    case "recovery":
      return "A fresh preview is required";
  }
}

function buildCommitGuidance(
  selectedCount: number,
  validationErrorCount: number,
  acknowledgedRefreshTokenOwnership: boolean,
): string {
  const issues: string[] = [];
  if (selectedCount === 0) issues.push("Select at least one account.");
  if (validationErrorCount > 0) {
    const accountLabel = validationErrorCount === 1 ? "account" : "accounts";
    issues.push(
      `Fix settings for ${validationErrorCount} selected ${accountLabel}.`,
    );
  }
  if (!acknowledgedRefreshTokenOwnership) {
    issues.push("Confirm the OAuth token ownership risk above.");
  }
  if (issues.length > 0) return issues.join(" ");

  const subject = selectedCount === 1 ? "account is" : "accounts are";
  return `${selectedCount} ${subject} ready to import.`;
}

export function ProviderImportModal({
  gateway,
  existingProviderIds,
  groups,
  onClose,
  onCheckProviders,
  onCommitted,
}: ProviderImportModalProps) {
  const titleId = useId();
  const descriptionId = useId();
  const commitGuidanceId = useId();
  const dialogRef = useRef<HTMLDivElement>(null);
  const titleRef = useRef<HTMLHeadingElement>(null);
  const previouslyFocusedRef = useRef<HTMLElement | null>(null);
  const previousPhaseRef =
    useRef<ReturnType<typeof useProviderImportFlow>["state"]["phase"]>(
      "upload",
    );
  const flow = useProviderImportFlow({
    gateway,
    existingProviderIds,
    onCommitted,
  });
  const { state } = flow;
  const closeLocked = state.phase === "committing";
  const abandonDraft = flow.abandonDraft;
  const closeDestination =
    state.phase === "recovery" ? onCheckProviders : onClose;
  const commitGuidance =
    state.phase === "review"
      ? buildCommitGuidance(
          flow.selectedCount,
          flow.validationErrors.size,
          state.draft.acknowledgedRefreshTokenOwnership,
        )
      : null;

  function requestClose() {
    if (closeLocked) return;
    void flow.abandonDraft();
    closeDestination();
  }

  useEffect(() => {
    previouslyFocusedRef.current =
      document.activeElement instanceof HTMLElement
        ? document.activeElement
        : null;
    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    const autofocusTarget =
      dialogRef.current?.querySelector<HTMLElement>("[data-autofocus]");
    (autofocusTarget ?? dialogRef.current)?.focus();

    return () => {
      document.body.style.overflow = previousOverflow;
      previouslyFocusedRef.current?.focus();
    };
  }, []);

  useEffect(() => {
    if (previousPhaseRef.current === state.phase) return;
    previousPhaseRef.current = state.phase;
    const target =
      dialogRef.current?.querySelector<HTMLElement>("[data-step-error]") ??
      titleRef.current;
    target?.focus();
  }, [state.phase]);

  useEffect(() => {
    function handleKeyDown(event: KeyboardEvent) {
      if (event.key === "Escape") {
        if (!closeLocked) {
          void abandonDraft();
          closeDestination();
        }
        return;
      }
      if (event.key !== "Tab" || !dialogRef.current) return;

      const focusable = Array.from(
        dialogRef.current.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTOR),
      );
      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      if (!first || !last) return;
      const activeIndex = focusable.indexOf(
        document.activeElement as HTMLElement,
      );
      if (event.shiftKey && activeIndex <= 0) {
        event.preventDefault();
        last.focus();
      } else if (
        !event.shiftKey &&
        (activeIndex === -1 || activeIndex === focusable.length - 1)
      ) {
        event.preventDefault();
        first.focus();
      }
    }

    document.addEventListener("keydown", handleKeyDown);
    return () => document.removeEventListener("keydown", handleKeyDown);
  }, [abandonDraft, closeDestination, closeLocked]);

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4 backdrop-blur-sm"
      onMouseDown={(event) => {
        if (event.target === event.currentTarget) requestClose();
      }}
    >
      <div
        ref={dialogRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        aria-describedby={descriptionId}
        aria-busy={state.phase === "previewing" || state.phase === "committing"}
        tabIndex={-1}
        className="flex max-h-[92vh] w-full max-w-5xl flex-col overflow-hidden rounded-xl border border-border bg-bg-primary shadow-2xl"
      >
        <header className="flex shrink-0 items-start justify-between gap-4 border-b border-border p-5 sm:p-6">
          <div>
            <h2
              ref={titleRef}
              id={titleId}
              tabIndex={-1}
              className="text-xl font-semibold text-text-primary"
            >
              Import GPT accounts
            </h2>
            <p id={descriptionId} className="mt-1 text-sm text-text-secondary">
              {phaseSubtitle(state.phase)}
            </p>
          </div>
          <button
            type="button"
            onClick={requestClose}
            disabled={closeLocked}
            aria-label="Close account import"
            className="rounded-lg p-2 text-text-muted transition-colors hover:bg-bg-secondary hover:text-text-primary disabled:cursor-not-allowed disabled:opacity-50"
          >
            <X className="h-5 w-5" aria-hidden="true" />
          </button>
        </header>

        <main className="grow overflow-y-auto p-5 sm:p-6">
          {(state.phase === "upload" || state.phase === "previewing") && (
            <UploadStep
              state={state}
              onFile={(file) => void flow.previewFile(file)}
              onRetry={flow.retryPreview}
            />
          )}
          {(state.phase === "review" || state.phase === "committing") && (
            <ReviewStep
              preview={state.preview}
              draft={state.draft}
              groups={groups}
              validationErrors={flow.validationErrors}
              error={state.phase === "review" ? state.error : null}
              disabled={state.phase === "committing"}
              onSetAction={flow.setAction}
              onEditProvider={flow.editProvider}
              onSetGroup={flow.setGroup}
              onSetAcknowledgement={flow.setAcknowledgement}
              onSelectAllReady={flow.selectAllReady}
              onSelectAllExisting={flow.selectAllExisting}
              onClearSelection={flow.clearSelection}
            />
          )}
          {state.phase === "result" && <ResultStep result={state.result} />}
          {state.phase === "recovery" && <RecoveryStep reason={state.reason} />}
          <div className="sr-only" aria-live="polite">
            {state.phase === "previewing" && "Previewing sub2api accounts"}
            {state.phase === "committing" && "Importing selected accounts"}
          </div>
        </main>

        <footer className="flex shrink-0 flex-wrap items-center justify-end gap-3 border-t border-border p-5 sm:p-6">
          {state.phase === "review" && (
            <button
              type="button"
              onClick={() => void flow.abandonDraft()}
              className="btn btn-secondary mr-auto"
            >
              Choose another file
            </button>
          )}
          {commitGuidance && (
            <p
              id={commitGuidanceId}
              role="status"
              className={`max-w-sm text-right text-xs ${flow.canCommit ? "text-success" : "text-text-secondary"}`}
            >
              {commitGuidance}
            </p>
          )}
          {state.phase !== "result" && (
            <button
              type="button"
              onClick={requestClose}
              disabled={closeLocked}
              className="btn btn-secondary"
            >
              {state.phase === "recovery"
                ? "Close and check providers"
                : "Cancel"}
            </button>
          )}
          {state.phase === "review" && (
            <button
              type="button"
              onClick={() => void flow.commit()}
              disabled={!flow.canCommit}
              aria-describedby={commitGuidanceId}
              className="btn btn-primary disabled:cursor-not-allowed disabled:opacity-50"
            >
              Import {flow.selectedCount}{" "}
              {flow.selectedCount === 1 ? "account" : "accounts"}
            </button>
          )}
          {state.phase === "committing" && (
            <button
              type="button"
              disabled
              className="btn btn-primary opacity-70"
            >
              Importing {flow.selectedCount} accounts…
            </button>
          )}
          {state.phase === "result" && (
            <button
              type="button"
              onClick={requestClose}
              className="btn btn-primary"
            >
              View providers
            </button>
          )}
          {state.phase === "recovery" && (
            <button
              type="button"
              onClick={() => void flow.abandonDraft()}
              className="btn btn-primary"
            >
              Choose file and preview again
            </button>
          )}
        </footer>
      </div>
    </div>
  );
}
