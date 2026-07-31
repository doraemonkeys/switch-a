import type {
  ProviderImportCandidate,
  ProviderImportCommitItem,
  ProviderImportCommitRequest,
  ProviderImportPreview,
} from "../api";
import { isValidId } from "./utils";

export const PROVIDER_IMPORT_MAX_FILE_BYTES = 5 * 1024 * 1024;
export const PROVIDER_IMPORT_MAX_PROVIDER_ID_LENGTH = 200;
export const PROVIDER_IMPORT_MAX_PROVIDER_NAME_LENGTH = 200;
export const PROVIDER_IMPORT_MAX_SCHEDULING_VALUE = 1_000_000;

export const SUB2API_FIELD_MAPPING_NOTES = [
  {
    field: "proxies",
    reason:
      "Switch-A provider credentials do not inherit sub2api proxy records.",
  },
  {
    field: "rate_multiplier",
    reason:
      "Billing multipliers are not equivalent to Switch-A routing weight.",
  },
  {
    field: "auto_pause_on_expired",
    reason: "Switch-A manages OAuth expiry and reauthentication itself.",
  },
  {
    field: "extra",
    reason:
      "Codex 5-hour and 7-day quota snapshots plus codex_usage_updated_at are imported; unrelated metadata is ignored.",
  },
] as const;

export type ProviderImportDecisionAction = "create" | "update" | "skip";

export interface ProviderImportCreateDraft {
  providerId: string;
  name: string;
  priority: number;
  concurrency: number;
}

export interface ProviderImportDecision {
  candidateId: string;
  action: ProviderImportDecisionAction;
  provider: ProviderImportCreateDraft | null;
}

export interface ProviderImportValidationError {
  field: keyof ProviderImportCreateDraft;
  message: string;
}

export interface ProviderImportReviewDraft {
  groupId: string | null;
  acknowledgedRefreshTokenOwnership: boolean;
  decisions: ProviderImportDecision[];
}

export type ProviderImportRecoveryReason =
  "stale" | "expired" | "unavailable" | "committed_mismatch";

export type ProviderImportFlowState =
  | {
      phase: "upload";
      file: File | null;
      error: string | null;
    }
  | {
      phase: "previewing";
      file: File;
    }
  | {
      phase: "review";
      preview: ProviderImportPreview;
      draft: ProviderImportReviewDraft;
      error: string | null;
    }
  | {
      phase: "committing";
      preview: ProviderImportPreview;
      draft: ProviderImportReviewDraft;
    }
  | {
      phase: "result";
      preview: ProviderImportPreview;
      result: import("../api").ProviderImportCommitResult;
    }
  | {
      phase: "recovery";
      importId: string;
      reason: ProviderImportRecoveryReason;
    };

export type ProviderImportFlowEvent =
  | { type: "preview_started"; file: File }
  | { type: "preview_failed"; file: File; error: string }
  | { type: "preview_succeeded"; preview: ProviderImportPreview }
  | {
      type: "set_action";
      candidateId: string;
      action: ProviderImportDecisionAction;
    }
  | {
      type: "edit_provider";
      candidateId: string;
      field: keyof ProviderImportCreateDraft;
      value: string | number;
    }
  | { type: "set_group"; groupId: string | null }
  | { type: "set_acknowledgement"; acknowledged: boolean }
  | { type: "select_all_ready" }
  | { type: "select_all_existing" }
  | { type: "clear_selection" }
  | { type: "commit_started" }
  | { type: "commit_failed"; error: string }
  | {
      type: "commit_recovery_required";
      reason: ProviderImportRecoveryReason;
    }
  | {
      type: "commit_succeeded";
      result: import("../api").ProviderImportCommitResult;
    }
  | { type: "reset" };

export const initialProviderImportState: ProviderImportFlowState = {
  phase: "upload",
  file: null,
  error: null,
};

function createDecision(
  candidate: ProviderImportCandidate,
): ProviderImportDecision {
  return {
    candidateId: candidate.candidate_id,
    action:
      candidate.status === "ready" && candidate.default_selected
        ? "create"
        : "skip",
    provider: {
      providerId: candidate.provider_id,
      name: candidate.name,
      priority: candidate.priority,
      concurrency: candidate.concurrency,
    },
  };
}

export function createProviderImportReviewDraft(
  preview: ProviderImportPreview,
): ProviderImportReviewDraft {
  return {
    groupId: null,
    acknowledgedRefreshTokenOwnership: false,
    decisions: preview.items.map(createDecision),
  };
}

function updateReviewState(
  state: Extract<ProviderImportFlowState, { phase: "review" }>,
  update: (draft: ProviderImportReviewDraft) => ProviderImportReviewDraft,
): ProviderImportFlowState {
  return { ...state, draft: update(state.draft), error: null };
}

function updateDecision(
  draft: ProviderImportReviewDraft,
  candidateId: string,
  update: (decision: ProviderImportDecision) => ProviderImportDecision,
): ProviderImportReviewDraft {
  return {
    ...draft,
    decisions: draft.decisions.map((decision) =>
      decision.candidateId === candidateId ? update(decision) : decision,
    ),
  };
}

type ReviewState = Extract<ProviderImportFlowState, { phase: "review" }>;
type SetActionEvent = Extract<ProviderImportFlowEvent, { type: "set_action" }>;

function reduceSetAction(
  state: ReviewState,
  event: SetActionEvent,
): ProviderImportFlowState {
  const candidate = state.preview.items.find(
    (item) => item.candidate_id === event.candidateId,
  );
  if (
    !candidate ||
    candidate.status === "duplicate" ||
    candidate.status === "invalid" ||
    candidate.status === "unsupported"
  ) {
    return state;
  }
  if (
    event.action === "update" &&
    (candidate.status !== "existing" || !candidate.existing_provider_id)
  ) {
    return state;
  }
  if (event.action === "create" && candidate.status !== "ready") {
    return state;
  }
  return updateReviewState(state, (draft) =>
    updateDecision(draft, event.candidateId, (decision) => ({
      ...decision,
      action: event.action,
    })),
  );
}

function reduceReviewEvent(
  state: ReviewState,
  event: ProviderImportFlowEvent,
): ProviderImportFlowState | null {
  switch (event.type) {
    case "set_action":
      return reduceSetAction(state, event);
    case "edit_provider":
      return updateReviewState(state, (draft) =>
        updateDecision(draft, event.candidateId, (decision) =>
          decision.provider
            ? {
                ...decision,
                provider: {
                  ...decision.provider,
                  [event.field]: event.value,
                },
              }
            : decision,
        ),
      );
    case "set_group":
      return updateReviewState(state, (draft) => ({
        ...draft,
        groupId: event.groupId,
      }));
    case "set_acknowledgement":
      return updateReviewState(state, (draft) => ({
        ...draft,
        acknowledgedRefreshTokenOwnership: event.acknowledged,
      }));
    case "select_all_ready":
      return updateReviewState(state, (draft) => ({
        ...draft,
        decisions: draft.decisions.map((decision) => {
          const candidate = state.preview.items.find(
            (item) => item.candidate_id === decision.candidateId,
          );
          return candidate?.status === "ready" && decision.provider
            ? { ...decision, action: "create" }
            : decision;
        }),
      }));
    case "select_all_existing":
      return updateReviewState(state, (draft) => ({
        ...draft,
        decisions: draft.decisions.map((decision) => {
          const candidate = state.preview.items.find(
            (item) => item.candidate_id === decision.candidateId,
          );
          return candidate?.status === "existing" &&
            candidate.existing_provider_id
            ? { ...decision, action: "update" }
            : decision;
        }),
      }));
    case "clear_selection":
      return updateReviewState(state, (draft) => ({
        ...draft,
        decisions: draft.decisions.map((decision) => ({
          ...decision,
          action: "skip",
        })),
      }));
    default:
      return null;
  }
}

export function providerImportFlowReducer(
  state: ProviderImportFlowState,
  event: ProviderImportFlowEvent,
): ProviderImportFlowState {
  if (state.phase === "review") {
    const reviewState = reduceReviewEvent(state, event);
    if (reviewState) return reviewState;
  }

  switch (event.type) {
    case "preview_started":
      return { phase: "previewing", file: event.file };
    case "preview_failed":
      return { phase: "upload", file: event.file, error: event.error };
    case "preview_succeeded":
      return {
        phase: "review",
        preview: event.preview,
        draft: createProviderImportReviewDraft(event.preview),
        error: null,
      };
    case "set_action":
    case "edit_provider":
    case "set_group":
    case "set_acknowledgement":
    case "select_all_ready":
    case "select_all_existing":
    case "clear_selection":
      return state;
    case "commit_started":
      return state.phase === "review"
        ? {
            phase: "committing",
            preview: state.preview,
            draft: state.draft,
          }
        : state;
    case "commit_failed":
      return state.phase === "committing"
        ? {
            phase: "review",
            preview: state.preview,
            draft: state.draft,
            error: event.error,
          }
        : state;
    case "commit_recovery_required":
      return state.phase === "committing"
        ? {
            phase: "recovery",
            importId: state.preview.import_id,
            reason: event.reason,
          }
        : state;
    case "commit_succeeded":
      return state.phase === "committing"
        ? { phase: "result", preview: state.preview, result: event.result }
        : state;
    case "reset":
      return initialProviderImportState;
  }
}

export function getProviderImportDecisionError(
  decision: ProviderImportDecision,
  existingProviderIds: ReadonlySet<string>,
  selectedProviderIds: readonly string[],
): ProviderImportValidationError | null {
  if (decision.action !== "create") return null;
  if (!decision.provider) {
    return { field: "providerId", message: "Provider settings are missing" };
  }

  const providerId = decision.provider.providerId.trim();
  if (!providerId) {
    return { field: "providerId", message: "Provider ID is required" };
  }
  if (!isValidId(providerId)) {
    return {
      field: "providerId",
      message:
        "Provider ID can only contain lowercase letters, numbers, and hyphens",
    };
  }
  if (providerId.length > PROVIDER_IMPORT_MAX_PROVIDER_ID_LENGTH) {
    return {
      field: "providerId",
      message: `Provider ID must be ${PROVIDER_IMPORT_MAX_PROVIDER_ID_LENGTH} characters or fewer`,
    };
  }
  if (existingProviderIds.has(providerId)) {
    return { field: "providerId", message: "Provider ID is already in use" };
  }
  if (selectedProviderIds.filter((id) => id === providerId).length > 1) {
    return {
      field: "providerId",
      message: "Provider ID is duplicated in this import",
    };
  }
  if (!decision.provider.name.trim()) {
    return { field: "name", message: "Provider name is required" };
  }
  if (
    decision.provider.name.trim().length >
    PROVIDER_IMPORT_MAX_PROVIDER_NAME_LENGTH
  ) {
    return {
      field: "name",
      message: `Provider name must be ${PROVIDER_IMPORT_MAX_PROVIDER_NAME_LENGTH} characters or fewer`,
    };
  }
  if (
    !Number.isSafeInteger(decision.provider.priority) ||
    decision.provider.priority < 0 ||
    decision.provider.priority > PROVIDER_IMPORT_MAX_SCHEDULING_VALUE
  ) {
    return {
      field: "priority",
      message: `Priority must be an integer from 0 to ${PROVIDER_IMPORT_MAX_SCHEDULING_VALUE.toLocaleString()}`,
    };
  }
  if (
    !Number.isSafeInteger(decision.provider.concurrency) ||
    decision.provider.concurrency < 0 ||
    decision.provider.concurrency > PROVIDER_IMPORT_MAX_SCHEDULING_VALUE
  ) {
    return {
      field: "concurrency",
      message: `Concurrency must be an integer from 0 to ${PROVIDER_IMPORT_MAX_SCHEDULING_VALUE.toLocaleString()}`,
    };
  }
  return null;
}

export function getSelectedProviderImportDecisions(
  draft: ProviderImportReviewDraft,
): ProviderImportDecision[] {
  return draft.decisions.filter((decision) => decision.action !== "skip");
}

export function getProviderImportValidationErrors(
  draft: ProviderImportReviewDraft,
  existingProviderIds: ReadonlySet<string>,
): Map<string, ProviderImportValidationError> {
  const selectedProviderIds = draft.decisions.flatMap((decision) =>
    decision.action === "create" && decision.provider
      ? [decision.provider.providerId.trim()]
      : [],
  );
  return new Map(
    draft.decisions.flatMap((decision) => {
      const error = getProviderImportDecisionError(
        decision,
        existingProviderIds,
        selectedProviderIds,
      );
      return error ? [[decision.candidateId, error] as const] : [];
    }),
  );
}

export function canCommitProviderImport(
  draft: ProviderImportReviewDraft,
  existingProviderIds: ReadonlySet<string>,
): boolean {
  return (
    draft.acknowledgedRefreshTokenOwnership &&
    getSelectedProviderImportDecisions(draft).length > 0 &&
    getProviderImportValidationErrors(draft, existingProviderIds).size === 0
  );
}

export function buildProviderImportCommitRequest(
  draft: ProviderImportReviewDraft,
): ProviderImportCommitRequest {
  const items: ProviderImportCommitItem[] = getSelectedProviderImportDecisions(
    draft,
  ).map((decision) => {
    if (decision.action === "update") {
      if (!decision.provider) {
        throw new Error(
          `update decision ${decision.candidateId} has no provider`,
        );
      }
      return {
        candidate_id: decision.candidateId,
        action: "update",
        provider_id: decision.provider.providerId.trim(),
      };
    }
    if (!decision.provider) {
      throw new Error(
        `create decision ${decision.candidateId} has no provider`,
      );
    }
    return {
      candidate_id: decision.candidateId,
      action: "create",
      provider_id: decision.provider.providerId.trim(),
      name: decision.provider.name.trim(),
      priority: decision.provider.priority,
      concurrency: decision.provider.concurrency,
    };
  });

  return { group_id: draft.groupId, items };
}

export function isSupportedProviderImportFile(file: File): boolean {
  const lowerName = file.name.toLowerCase();
  return (
    lowerName.endsWith(".json") ||
    lowerName.endsWith(".txt") ||
    file.type === "application/json" ||
    file.type === "text/plain"
  );
}

export function describeProviderImportError(error: unknown): string {
  return error instanceof Error ? error.message : "Provider import failed";
}
