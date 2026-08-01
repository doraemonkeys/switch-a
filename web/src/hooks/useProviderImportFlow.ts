import { useReducer, useRef } from "react";
import type {
  ProviderImportCommitRequest,
  ProviderImportCommitResult,
  ProviderImportPreview,
} from "../api";
import {
  PROVIDER_IMPORT_MAX_FILE_BYTES,
  buildProviderImportCommitRequest,
  canCommitProviderImport,
  describeProviderImportError,
  getProviderImportValidationErrors,
  getSelectedProviderImportDecisions,
  initialProviderImportState,
  isSupportedProviderImportFile,
  providerImportFlowReducer,
  type ProviderImportDecisionAction,
  type ProviderImportFlowState,
  type ProviderImportRecoveryReason,
} from "../lib/providerImport";
import type {
  ProviderImportCreateDraft,
  ProviderImportNewProviderDefaults,
  ProviderImportValidationError,
} from "../lib/providerImportSettings";

const BYTES_PER_MEBIBYTE = 1024 * 1024;

export interface ProviderImportGateway {
  preview: (sourceData: string) => Promise<ProviderImportPreview>;
  commit: (
    importId: string,
    request: ProviderImportCommitRequest,
  ) => Promise<ProviderImportCommitResult>;
  discard: (importId: string) => Promise<void>;
}

interface UseProviderImportFlowOptions {
  gateway: ProviderImportGateway;
  existingProviderIds: readonly string[];
  onCommitted?: (result: ProviderImportCommitResult) => void | Promise<void>;
}

function getImportIdForDiscard(state: ProviderImportFlowState): string | null {
  if (state.phase === "review" || state.phase === "committing") {
    return state.preview.import_id;
  }
  if (state.phase === "recovery") return state.importId;
  return null;
}

function getProviderImportRecoveryReason(
  error: unknown,
): ProviderImportRecoveryReason | null {
  if (!error || typeof error !== "object" || !("status" in error)) return null;
  const status = (error as { status: unknown }).status;
  if (status === 404) return "unavailable";
  if (status === 409) {
    const details = "details" in error ? error.details : null;
    if (
      details &&
      typeof details === "object" &&
      "kind" in details &&
      details.kind === "provider_import_commit_mismatch"
    ) {
      return "committed_mismatch";
    }
    return "stale";
  }
  if (status === 410) return "expired";
  return null;
}

function describeProviderImportCommitError(
  error: unknown,
  preview: ProviderImportPreview,
): string {
  if (!error || typeof error !== "object" || !("details" in error)) {
    return describeProviderImportError(error);
  }
  const details = error.details;
  if (!details || typeof details !== "object" || !("kind" in details)) {
    return describeProviderImportError(error);
  }
  if (details.kind === "provider_import_signing_keys_unavailable") {
    return "OpenAI signing keys are temporarily unavailable. Your review is unchanged; retry in a moment.";
  }
  if (details.kind !== "provider_import_token_verification_failed") {
    return describeProviderImportError(error);
  }

  const candidateId =
    "candidate_id" in details && typeof details.candidate_id === "string"
      ? details.candidate_id
      : null;
  const candidate = preview.items.find(
    (item) => item.candidate_id === candidateId,
  );
  const accountLabel =
    candidate?.name || candidate?.email || candidate?.provider_id;
  return accountLabel
    ? `Could not verify OAuth tokens for "${accountLabel}". Skip this account or upload a fresh trusted export.`
    : "Could not verify one account's OAuth tokens. Skip that account or upload a fresh trusted export.";
}

function validateProviderImportFile(file: File): string | null {
  if (!isSupportedProviderImportFile(file)) {
    return "Choose a sub2api JSON export with a .json or .txt extension.";
  }
  if (file.size > PROVIDER_IMPORT_MAX_FILE_BYTES) {
    return `The export is larger than ${PROVIDER_IMPORT_MAX_FILE_BYTES / BYTES_PER_MEBIBYTE} MB.`;
  }
  return null;
}

async function requestProviderImportPreview(
  gateway: ProviderImportGateway,
  file: File,
): Promise<ProviderImportPreview> {
  // Keep credential-bearing JSON in this short-lived stack frame so React state
  // receives only the server's sanitized preview.
  const sourceData = await file.text();
  return gateway.preview(sourceData);
}

async function discardProviderImportDraft(
  gateway: ProviderImportGateway,
  importId: string,
) {
  try {
    await gateway.discard(importId);
  } catch (error) {
    // Drafts expire server-side; cleanup failure must not trap the user in a modal.
    console.warn("provider import draft cleanup failed", { importId, error });
  }
}

export function useProviderImportFlow({
  gateway,
  existingProviderIds,
  onCommitted,
}: UseProviderImportFlowOptions) {
  const [state, dispatch] = useReducer(
    providerImportFlowReducer,
    initialProviderImportState,
  );
  const previewVersionRef = useRef(0);
  const commitInFlightRef = useRef(false);

  const existingIds = new Set(existingProviderIds);
  const reviewDraft =
    state.phase === "review" || state.phase === "committing"
      ? state.draft
      : null;
  const selectedCount = reviewDraft
    ? getSelectedProviderImportDecisions(reviewDraft).length
    : 0;
  const validationErrors = reviewDraft
    ? getProviderImportValidationErrors(reviewDraft, existingIds)
    : new Map<string, ProviderImportValidationError>();
  const canCommit =
    state.phase === "review" &&
    canCommitProviderImport(state.draft, existingIds);

  async function previewFile(file: File) {
    const requestVersion = previewVersionRef.current + 1;
    previewVersionRef.current = requestVersion;

    const fileError = validateProviderImportFile(file);
    if (fileError) {
      dispatch({ type: "preview_failed", file, error: fileError });
      return;
    }

    dispatch({ type: "preview_started", file });
    try {
      const preview = await requestProviderImportPreview(gateway, file);
      if (previewVersionRef.current === requestVersion) {
        dispatch({ type: "preview_succeeded", preview });
      } else {
        // Closing or replacing a preview invalidates its response, but the server
        // draft still needs explicit cleanup when the request completes.
        await discardProviderImportDraft(gateway, preview.import_id);
      }
    } catch (error) {
      if (previewVersionRef.current === requestVersion) {
        dispatch({
          type: "preview_failed",
          file,
          error: describeProviderImportError(error),
        });
      }
    }
  }

  function retryPreview() {
    if (state.phase === "upload" && state.file) {
      void previewFile(state.file);
    }
  }

  function setAction(
    candidateId: string,
    action: ProviderImportDecisionAction,
  ) {
    dispatch({ type: "set_action", candidateId, action });
  }

  function editProvider(
    candidateId: string,
    field: keyof ProviderImportCreateDraft,
    value: ProviderImportCreateDraft[keyof ProviderImportCreateDraft],
  ) {
    dispatch({ type: "edit_provider", candidateId, field, value });
  }

  function applyNewProviderDefaults(
    defaults: ProviderImportNewProviderDefaults,
  ) {
    dispatch({ type: "apply_new_provider_defaults", defaults });
  }

  function setGroup(groupId: string | null) {
    dispatch({ type: "set_group", groupId });
  }

  function setAcknowledgement(acknowledged: boolean) {
    dispatch({ type: "set_acknowledgement", acknowledged });
  }

  function selectAllReady() {
    dispatch({ type: "select_all_ready" });
  }

  function selectAllExisting() {
    dispatch({ type: "select_all_existing" });
  }

  function clearSelection() {
    dispatch({ type: "clear_selection" });
  }

  async function commit() {
    if (state.phase !== "review" || !canCommit || commitInFlightRef.current) {
      return;
    }

    commitInFlightRef.current = true;
    const request = buildProviderImportCommitRequest(state.draft);
    const importId = state.preview.import_id;
    dispatch({ type: "commit_started" });
    try {
      const result = await gateway.commit(importId, request);
      dispatch({ type: "commit_succeeded", result });
      if (onCommitted) {
        void Promise.resolve(onCommitted(result)).catch((error: unknown) => {
          console.error("provider import committed but list refresh failed", {
            importId,
            error,
          });
        });
      }
    } catch (error) {
      const recoveryReason = getProviderImportRecoveryReason(error);
      dispatch(
        recoveryReason
          ? { type: "commit_recovery_required", reason: recoveryReason }
          : {
              type: "commit_failed",
              error: describeProviderImportCommitError(error, state.preview),
            },
      );
    } finally {
      commitInFlightRef.current = false;
    }
  }

  async function abandonDraft() {
    previewVersionRef.current += 1;
    const importId = getImportIdForDiscard(state);
    dispatch({ type: "reset" });
    if (!importId) return;
    await discardProviderImportDraft(gateway, importId);
  }

  return {
    state,
    selectedCount,
    validationErrors,
    canCommit,
    previewFile,
    retryPreview,
    setAction,
    editProvider,
    applyNewProviderDefaults,
    setGroup,
    setAcknowledgement,
    selectAllReady,
    selectAllExisting,
    clearSelection,
    commit,
    abandonDraft,
  };
}
