import { describe, expect, it } from "vitest";
import type { ProviderImportPreview } from "../api";
import {
  PROVIDER_IMPORT_MAX_PROVIDER_ID_LENGTH,
  PROVIDER_IMPORT_MAX_PROVIDER_NAME_LENGTH,
  PROVIDER_IMPORT_MAX_SCHEDULING_VALUE,
  buildProviderImportCommitRequest,
  canCommitProviderImport,
  createProviderImportReviewDraft,
  getProviderImportValidationErrors,
  initialProviderImportState,
  isSupportedProviderImportFile,
  providerImportFlowReducer,
} from "./providerImport";

function buildPreview(): ProviderImportPreview {
  return {
    import_id: "import-1",
    expires_at: "2026-07-30T22:00:00Z",
    summary: {
      total: 3,
      ready: 1,
      existing: 1,
      duplicate: 0,
      invalid: 1,
      unsupported: 0,
    },
    warnings: [],
    items: [
      {
        candidate_id: "ready-1",
        source_index: 0,
        status: "ready",
        name: "Ready Account",
        provider_id: "ready-account",
        email: "ready@example.com",
        priority: 1,
        concurrency: 10,
        default_selected: true,
        warnings: [],
      },
      {
        candidate_id: "existing-1",
        source_index: 1,
        status: "existing",
        name: "Existing Account",
        provider_id: "existing-provider",
        existing_provider_id: "existing-provider",
        existing_provider_name: "Existing Provider",
        priority: 2,
        concurrency: 4,
        default_selected: false,
        warnings: [],
      },
      {
        candidate_id: "invalid-1",
        source_index: 2,
        status: "invalid",
        name: "Invalid Account",
        provider_id: "invalid-account",
        priority: 0,
        concurrency: 0,
        default_selected: false,
        message: "Refresh token is missing",
        warnings: [],
      },
    ],
  };
}

describe("provider import review model", () => {
  it("selects ready creates while keeping existing and invalid accounts safe", () => {
    const draft = createProviderImportReviewDraft(buildPreview());

    expect(
      draft.decisions.map(({ candidateId, action }) => ({
        candidateId,
        action,
      })),
    ).toEqual([
      { candidateId: "ready-1", action: "create" },
      { candidateId: "existing-1", action: "skip" },
      { candidateId: "invalid-1", action: "skip" },
    ]);
    expect(draft.acknowledgedRefreshTokenOwnership).toBe(false);
  });

  it("requires acknowledgement and validates provider IDs before commit", () => {
    const preview = buildPreview();
    let state = providerImportFlowReducer(initialProviderImportState, {
      type: "preview_succeeded",
      preview,
    });
    expect(state.phase).toBe("review");
    if (state.phase !== "review") throw new Error("expected review state");

    expect(canCommitProviderImport(state.draft, new Set())).toBe(false);
    state = providerImportFlowReducer(state, {
      type: "set_acknowledgement",
      acknowledged: true,
    });
    if (state.phase !== "review") throw new Error("expected review state");
    expect(canCommitProviderImport(state.draft, new Set())).toBe(true);

    state = providerImportFlowReducer(state, {
      type: "edit_provider",
      candidateId: "ready-1",
      field: "providerId",
      value: "existing-provider",
    });
    if (state.phase !== "review") throw new Error("expected review state");
    expect(
      getProviderImportValidationErrors(
        state.draft,
        new Set(["existing-provider"]),
      ).get("ready-1"),
    ).toEqual({
      field: "providerId",
      message: "Provider ID is already in use",
    });
    expect(
      canCommitProviderImport(state.draft, new Set(["existing-provider"])),
    ).toBe(false);
  });

  it.each([
    ["priority", PROVIDER_IMPORT_MAX_SCHEDULING_VALUE + 1],
    ["concurrency", Number.MAX_SAFE_INTEGER + 1],
  ] as const)("rejects an out-of-range %s integer", (field, value) => {
    let state = providerImportFlowReducer(initialProviderImportState, {
      type: "preview_succeeded",
      preview: buildPreview(),
    });
    state = providerImportFlowReducer(state, {
      type: "edit_provider",
      candidateId: "ready-1",
      field,
      value,
    });
    if (state.phase !== "review") throw new Error("expected review state");

    expect(
      getProviderImportValidationErrors(state.draft, new Set()).get("ready-1"),
    ).toEqual({
      field,
      message: `${field === "priority" ? "Priority" : "Concurrency"} must be an integer from 0 to 1,000,000`,
    });
  });

  it.each([
    [
      "providerId",
      "a".repeat(PROVIDER_IMPORT_MAX_PROVIDER_ID_LENGTH + 1),
      `Provider ID must be ${PROVIDER_IMPORT_MAX_PROVIDER_ID_LENGTH} characters or fewer`,
    ],
    [
      "name",
      "A".repeat(PROVIDER_IMPORT_MAX_PROVIDER_NAME_LENGTH + 1),
      `Provider name must be ${PROVIDER_IMPORT_MAX_PROVIDER_NAME_LENGTH} characters or fewer`,
    ],
  ] as const)("rejects an overlong %s", (field, value, message) => {
    let state = providerImportFlowReducer(initialProviderImportState, {
      type: "preview_succeeded",
      preview: buildPreview(),
    });
    state = providerImportFlowReducer(state, {
      type: "edit_provider",
      candidateId: "ready-1",
      field,
      value,
    });
    if (state.phase !== "review") throw new Error("expected review state");

    expect(
      getProviderImportValidationErrors(state.draft, new Set()).get("ready-1"),
    ).toEqual({ field, message });
  });

  it("builds explicit create and credential-update decisions", () => {
    const preview = buildPreview();
    let state = providerImportFlowReducer(initialProviderImportState, {
      type: "preview_succeeded",
      preview,
    });
    state = providerImportFlowReducer(state, {
      type: "set_action",
      candidateId: "existing-1",
      action: "update",
    });
    if (state.phase !== "review") throw new Error("expected review state");

    expect(buildProviderImportCommitRequest(state.draft)).toEqual({
      group_id: null,
      items: [
        {
          candidate_id: "ready-1",
          action: "create",
          provider_id: "ready-account",
          name: "Ready Account",
          priority: 1,
          concurrency: 10,
        },
        {
          candidate_id: "existing-1",
          action: "update",
          provider_id: "existing-provider",
        },
      ],
    });
  });

  it("bulk-selects existing credential updates without changing blocked rows", () => {
    let state = providerImportFlowReducer(initialProviderImportState, {
      type: "preview_succeeded",
      preview: buildPreview(),
    });
    state = providerImportFlowReducer(state, { type: "select_all_existing" });
    if (state.phase !== "review") throw new Error("expected review state");

    expect(
      state.draft.decisions.map(({ candidateId, action }) => ({
        candidateId,
        action,
      })),
    ).toEqual([
      { candidateId: "ready-1", action: "create" },
      { candidateId: "existing-1", action: "update" },
      { candidateId: "invalid-1", action: "skip" },
    ]);
  });

  it("accepts both real sub2api export extensions", () => {
    expect(
      isSupportedProviderImportFile(
        new File(["{}"], "sub2api-account.txt", { type: "text/plain" }),
      ),
    ).toBe(true);
    expect(
      isSupportedProviderImportFile(
        new File(["{}"], "sub2api-account.json", {
          type: "application/json",
        }),
      ),
    ).toBe(true);
    expect(
      isSupportedProviderImportFile(
        new File(["{}"], "accounts.csv", { type: "text/csv" }),
      ),
    ).toBe(false);
  });
});
