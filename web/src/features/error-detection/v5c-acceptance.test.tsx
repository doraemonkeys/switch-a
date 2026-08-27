import { act } from "react";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiError, type ApiClient, type Provider } from "@/api";
import { parseAPICatalog } from "@/api/api-catalog";
import { APICatalogContext, ApiContext } from "@/api/context";
import {
  parseInternalErrorRuleListResponse,
  parseInternalErrorRuleStatsResponse,
  parseTestMessageResponse,
} from "@/api/error-detection-decoders";
import { createMockApiClient } from "@/hooks/test-utils";
import apiCatalogFixture from "../../../../contracts/internal-error/v1/api-catalog.json";
import ruleListFixture from "../../../../contracts/internal-error/v1/rule-list.json";
import ruleStatsFixture from "../../../../contracts/internal-error/v1/rule-stats.json";
import testMessageFixture from "../../../../contracts/internal-error/v1/test-message.json";
import type {
  DeletedInternalErrorRule,
  InternalErrorRule,
  InternalErrorRuleETag,
  InternalErrorRuleResponse,
  RevisionedInternalErrorResource,
} from "./contracts";
import { ErrorDetectionFeature } from "./ErrorDetectionFeature";

const catalog = parseAPICatalog(apiCatalogFixture);
const initialList = parseInternalErrorRuleListResponse(ruleListFixture);
const initialStats = parseInternalErrorRuleStatsResponse(ruleStatsFixture);
const provider = {
  id: "provider-codex",
  name: "Codex primary",
  api_types: [{ api_type: "codex" }],
} as Provider;

function etag(revision: string): InternalErrorRuleETag {
  return `"internal-error-rules/${revision}"`;
}

function listResource(
  revision: string,
  rules: readonly InternalErrorRule[] = initialList.rules,
): RevisionedInternalErrorResource<typeof initialList> {
  return {
    value: {
      schema_version: 1,
      rule_set_revision: revision,
      rules,
    },
    etag: etag(revision),
  };
}

function ruleResource(
  revision: string,
  rule: InternalErrorRule,
): RevisionedInternalErrorResource<InternalErrorRuleResponse> {
  return {
    value: { schema_version: 1, rule_set_revision: revision, rule },
    etag: etag(revision),
  };
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, resolve, reject };
}

function createFeatureApi(): ApiClient {
  const api = createMockApiClient();
  api.errorDetection = {
    rules: {
      list: vi.fn().mockResolvedValue(listResource("7")),
      get: vi.fn(),
      create: vi.fn(),
      update: vi.fn(),
      delete: vi.fn(),
      reorder: vi.fn(),
    },
    stats: { get: vi.fn().mockResolvedValue(initialStats) },
    testMessage: vi.fn(),
  };
  vi.mocked(api.providers.list).mockResolvedValue([provider]);
  vi.mocked(api.config.get).mockResolvedValue({
    defaults: { global_max_attempts: "3" },
    values: {},
  });
  return api;
}

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

function renderFeature(api: ApiClient) {
  return render(
    <ApiContext.Provider value={api}>
      <APICatalogContext.Provider
        value={{
          catalog,
          loading: false,
          error: null,
          refetch: vi.fn().mockResolvedValue(undefined),
        }}
      >
        <ErrorDetectionFeature />
      </APICatalogContext.Provider>
    </ApiContext.Provider>,
  );
}

describe("V5C Error Detection accessibility acceptance", () => {
  it("completes CRUD and keyboard reorder while chaining authoritative revisions", async () => {
    const user = userEvent.setup();
    const api = createFeatureApi();
    const createdRule: InternalErrorRule = {
      ...initialList.rules[0],
      id: "33333333-3333-4333-8333-333333333333",
      name: "Capacity copy",
      target: { kind: "global" },
      position: 2,
      created_at: "2026-08-04T00:00:00Z",
      updated_at: "2026-08-04T00:00:00Z",
    };
    const updatedRule: InternalErrorRule = {
      ...createdRule,
      name: "Capacity copy edited",
      updated_at: "2026-08-04T00:01:00Z",
    };
    const reorderedRules: readonly InternalErrorRule[] = [
      { ...initialList.rules[0], position: 0 },
      { ...updatedRule, position: 1 },
      { ...initialList.rules[1], position: 2 },
    ];
    // Explicit completion boundaries preserve one end-to-end revision chain
    // without making Vitest worker load part of the acceptance contract.
    const createResult =
      deferred<RevisionedInternalErrorResource<InternalErrorRuleResponse>>();
    const updateResult =
      deferred<RevisionedInternalErrorResource<InternalErrorRuleResponse>>();
    const reorderResult =
      deferred<RevisionedInternalErrorResource<typeof initialList>>();
    const deleteResult = deferred<DeletedInternalErrorRule>();

    vi.mocked(api.errorDetection.rules.create).mockReturnValue(
      createResult.promise,
    );
    vi.mocked(api.errorDetection.rules.update).mockReturnValue(
      updateResult.promise,
    );
    vi.mocked(api.errorDetection.rules.reorder).mockReturnValue(
      reorderResult.promise,
    );
    vi.mocked(api.errorDetection.rules.delete).mockReturnValue(
      deleteResult.promise,
    );
    renderFeature(api);

    fireEvent.click(await screen.findByRole("button", { name: "Add rule" }));
    const editor = screen
      .getByRole("heading", { name: "Create detection rule" })
      .closest("section");
    expect(editor).not.toBeNull();
    const editorQueries = within(editor as HTMLElement);
    fireEvent.change(editorQueries.getByLabelText("Rule name"), {
      target: { value: "Capacity copy" },
    });

    const presetButton = editorQueries.getByRole("button", {
      name: /Codex capacity/i,
    });
    await user.click(presetButton);
    expect(
      screen.getByRole("alertdialog", {
        name: "Replace unsaved draft fields?",
      }),
    ).toBeInTheDocument();

    await user.keyboard("{Escape}");
    expect(presetButton).toHaveFocus();
    fireEvent.click(presetButton);
    fireEvent.click(screen.getByRole("button", { name: "Replace draft" }));

    const waitEstimate = editorQueries.getByRole("region", {
      name: "Current provider wait estimate",
    });
    expect(waitEstimate).toHaveTextContent("Effective same-provider retries1");
    expect(waitEstimate).toHaveTextContent(
      "One finite global attempt is reserved for a provider switch",
    );
    expect(waitEstimate).toHaveTextContent(
      "excludes connection time, upstream time to first byte",
    );

    fireEvent.click(editorQueries.getByRole("button", { name: "Create rule" }));
    const expectedCreateSpec = {
      name: "Capacity copy",
      enabled: true,
      target: { kind: "global" },
      api_type: "codex",
      keywords: [
        "server_is_overloaded",
        "our servers are currently overloaded at capacity",
      ],
      match_mode: "any",
      action: {
        type: "retry_then_switch",
        max_retries: 2,
        backoff: {
          initial_delay: "250ms",
          max_delay: "2s",
          multiplier: 2,
          jitter: true,
        },
      },
    } as const;
    expect(api.errorDetection.rules.create).toHaveBeenCalledWith(
      expectedCreateSpec,
      etag("7"),
    );
    await act(async () => {
      createResult.resolve(ruleResource("8", createdRule));
    });

    fireEvent.click(
      await screen.findByRole("button", { name: "Edit Capacity copy" }),
    );
    const nameInput = screen.getByLabelText("Rule name");
    fireEvent.change(nameInput, {
      target: { value: "Capacity copy edited" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Save rule" }));
    expect(api.errorDetection.rules.update).toHaveBeenCalledWith(
      createdRule.id,
      { ...expectedCreateSpec, name: "Capacity copy edited" },
      etag("8"),
    );
    await act(async () => {
      updateResult.resolve(ruleResource("9", updatedRule));
    });

    const moveButton = await screen.findByRole("button", {
      name: "Move Capacity copy edited up",
    });
    moveButton.focus();
    await user.keyboard("{Enter}");
    expect(api.errorDetection.rules.reorder).toHaveBeenCalledWith(
      [initialList.rules[0].id, updatedRule.id, initialList.rules[1].id],
      etag("9"),
    );
    await act(async () => {
      reorderResult.resolve(listResource("10", reorderedRules));
    });
    expect(
      screen.getByRole("button", {
        name: "Move Capacity copy edited up",
      }),
    ).toHaveFocus();

    fireEvent.click(
      screen.getByRole("button", { name: "Delete Capacity copy edited" }),
    );
    fireEvent.click(screen.getByRole("button", { name: "Delete rule" }));
    const deleteDialog = screen.getByRole("alertdialog", {
      name: "Delete detection rule?",
    });
    expect(deleteDialog).toHaveAttribute("aria-busy", "true");
    expect(screen.getByRole("button", { name: "Working…" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Refresh" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Add rule" })).toBeDisabled();
    expect(
      screen.getByRole("button", { name: "Analyze message" }),
    ).toBeDisabled();
    expect(api.errorDetection.rules.delete).toHaveBeenCalledWith(
      updatedRule.id,
      etag("10"),
    );

    await act(async () => {
      deleteResult.resolve({
        rule_set_revision: "11",
        etag: etag("11"),
      });
    });
    expect(await screen.findByText("Detection rule deleted.")).toBeVisible();
    expect(
      screen.queryByRole("button", { name: "Edit Capacity copy edited" }),
    ).not.toBeInTheDocument();
  });

  it("keeps the Anthropic preset exact and rejects an enabled custom API draft", async () => {
    const user = userEvent.setup();
    const api = createFeatureApi();
    const customRule: InternalErrorRule = {
      ...initialList.rules[0],
      id: "66666666-6666-4666-8666-666666666666",
      name: "Private overload observer",
      enabled: false,
      target: { kind: "global" },
      api_type: "custom:private",
      keywords: ["overloaded_error"],
      position: 2,
      created_at: "2026-08-04T00:02:00Z",
      updated_at: "2026-08-04T00:02:00Z",
    };
    vi.mocked(api.errorDetection.rules.create).mockResolvedValue(
      ruleResource("8", customRule),
    );
    renderFeature(api);

    await user.click(await screen.findByRole("button", { name: "Add rule" }));
    const editor = screen
      .getByRole("heading", { name: "Create detection rule" })
      .closest("section");
    expect(editor).not.toBeNull();
    const editorQueries = within(editor as HTMLElement);
    await user.click(
      editorQueries.getByRole("button", { name: /Anthropic overload/i }),
    );
    expect(editorQueries.getByLabelText("API type")).toHaveValue("claude");
    expect(editorQueries.getByLabelText(/Keywords/)).toHaveValue(
      "overloaded_error",
    );
    expect(editorQueries.getByLabelText("Action")).toHaveValue(
      "retry_then_switch",
    );
    expect(editorQueries.getByLabelText("Same-provider retries")).toHaveValue(
      2,
    );

    await user.type(
      editorQueries.getByLabelText("Rule name"),
      "Private overload observer",
    );
    const apiType = editorQueries.getByLabelText("API type");
    await user.clear(apiType);
    await user.type(apiType, "custom:private");
    expect(
      editorQueries.getByText(
        /Custom API types do not support structured error detection/,
      ),
    ).toBeVisible();

    await user.click(
      editorQueries.getByRole("button", { name: "Create rule" }),
    );
    expect(apiType).toHaveAttribute("aria-invalid", "true");
    expect(apiType).toHaveAccessibleDescription(
      "Custom API types do not support structured error detection. Disable the rule or choose a supported built-in API type.",
    );
    expect(api.errorDetection.rules.create).not.toHaveBeenCalled();

    await user.click(editorQueries.getByRole("checkbox", { name: /Enabled/ }));
    await user.click(
      editorQueries.getByRole("button", { name: "Create rule" }),
    );
    await waitFor(() =>
      expect(api.errorDetection.rules.create).toHaveBeenCalledWith(
        {
          name: "Private overload observer",
          enabled: false,
          target: { kind: "global" },
          api_type: "custom:private",
          keywords: ["overloaded_error"],
          match_mode: "any",
          action: {
            type: "retry_then_switch",
            max_retries: 2,
            backoff: {
              initial_delay: "250ms",
              max_delay: "2s",
              multiplier: 2,
              jitter: true,
            },
          },
        },
        etag("7"),
      ),
    );
  });
});

describe("V5C Error Detection recovery and Test Message acceptance", () => {
  it("associates validation errors and preserves a stale draft through explicit conflict recovery", async () => {
    const user = userEvent.setup();
    const api = createFeatureApi();
    const serverRules = initialList.rules.map((rule) =>
      rule.id === initialList.rules[0].id
        ? { ...rule, name: "Server-side name" }
        : rule,
    );
    const retainedRule = {
      ...serverRules[0],
      name: "Retained operator draft",
    };
    const reapplied =
      deferred<RevisionedInternalErrorResource<InternalErrorRuleResponse>>();
    vi.mocked(api.errorDetection.rules.list)
      .mockReset()
      .mockResolvedValueOnce(listResource("7"))
      .mockResolvedValue(listResource("8", serverRules));
    vi.mocked(api.errorDetection.rules.update)
      .mockRejectedValueOnce(
        new ApiError("REVISION_MISMATCH", "Rule revision changed", 412, {
          current_revision: "8",
        }),
      )
      .mockReturnValueOnce(reapplied.promise);
    renderFeature(api);

    await user.click(
      await screen.findByRole("button", { name: "Edit Codex capacity" }),
    );
    const nameInput = screen.getByLabelText("Rule name");
    await user.clear(nameInput);
    await user.click(screen.getByRole("button", { name: "Save rule" }));

    expect(nameInput).toHaveAttribute("aria-invalid", "true");
    expect(nameInput).toHaveAccessibleDescription("Name is required.");
    expect(
      screen.getByText("Review the highlighted fields before saving."),
    ).toHaveAttribute("role", "alert");
    expect(api.errorDetection.rules.update).not.toHaveBeenCalled();

    await user.type(nameInput, "Retained operator draft");
    await user.click(screen.getByRole("button", { name: "Save rule" }));
    expect(
      await screen.findByRole("alert", {
        name: "Rule revision changed on the server",
      }),
    ).toHaveTextContent("revision 8");
    expect(nameInput).toHaveValue("Retained operator draft");

    await user.click(
      screen.getByRole("button", { name: "Refresh server rules" }),
    );
    expect(
      await screen.findByText(
        "Latest rule revision loaded. Review the draft, then save again.",
      ),
    ).toBeVisible();
    expect(screen.getByLabelText("Rule name")).toHaveValue(
      "Retained operator draft",
    );

    await user.click(screen.getByRole("button", { name: "Save rule" }));
    const editorForm = screen.getByLabelText("Rule name").closest("form");
    await waitFor(() => {
      expect(editorForm).toHaveAttribute("aria-busy", "true");
      expect(screen.getByRole("button", { name: "Saving…" })).toBeDisabled();
      expect(screen.getByRole("button", { name: "Add rule" })).toBeDisabled();
      expect(
        screen.getByRole("button", { name: "Analyze message" }),
      ).toBeDisabled();
    });
    const updateCalls = vi.mocked(api.errorDetection.rules.update).mock.calls;
    expect(updateCalls[0]?.[2]).toBe(etag("7"));
    expect(updateCalls[1]?.[2]).toBe(etag("8"));
    expect(updateCalls[1]?.[1]).toMatchObject({
      name: "Retained operator draft",
    });

    await act(async () => {
      reapplied.resolve(ruleResource("9", retainedRule));
    });
    expect(await screen.findByText("Detection rule updated.")).toBeVisible();
  });

  it("uses one backend Test Message result as the only matching authority", async () => {
    const user = userEvent.setup();
    const api = createFeatureApi();
    const analysis = deferred<ReturnType<typeof parseTestMessageResponse>>();
    vi.mocked(api.errorDetection.testMessage).mockReturnValue(analysis.promise);
    renderFeature(api);

    const analyzeButton = await screen.findByRole("button", {
      name: "Analyze message",
    });
    const apiType = screen.getByLabelText("API type");
    const ruleScope = screen.getByLabelText("Rule scope");
    await user.selectOptions(apiType, "codex");
    await waitFor(() =>
      expect(
        within(ruleScope).getByRole("option", {
          name: /Codex primary.*provider and global rules/,
        }),
      ).toBeInTheDocument(),
    );
    await user.selectOptions(ruleScope, provider.id);
    fireEvent.change(screen.getByLabelText("Response body"), {
      target: { value: "ordinary output with no local semantic marker" },
    });
    await user.click(analyzeButton);

    const form = analyzeButton.closest("form");
    await waitFor(() => {
      expect(form).toHaveAttribute("aria-busy", "true");
      expect(screen.getByRole("button", { name: "Analyzing…" })).toBeDisabled();
      expect(apiType).toBeDisabled();
      expect(ruleScope).toBeDisabled();
      expect(screen.getByLabelText("Content-Type")).toBeDisabled();
      expect(screen.getByLabelText("Content-Encoding")).toBeDisabled();
      expect(screen.getByLabelText("Body encoding")).toBeDisabled();
      expect(screen.getByLabelText("Response body")).toBeDisabled();
    });
    expect(api.errorDetection.testMessage).toHaveBeenCalledTimes(1);
    expect(api.errorDetection.testMessage).toHaveBeenCalledWith(
      {
        api_type: "codex",
        provider_id: provider.id,
        content_type: "application/json",
        content_encoding: "identity",
        body: {
          encoding: "utf8",
          value: "ordinary output with no local semantic marker",
        },
      },
      etag("7"),
    );

    await act(async () => {
      analysis.resolve(
        parseTestMessageResponse(testMessageFixture.complete.response),
      );
    });
    const result = await screen.findByRole("region", {
      name: "Test Message result",
    });
    expect(result).toHaveTextContent("openai.responses.sse.v1");
    expect(result).toHaveTextContent("server_is_overloaded");
    expect(result).toHaveTextContent("Winning rule");
    expect(api.errorDetection.testMessage).toHaveBeenCalledTimes(1);
  });
});
