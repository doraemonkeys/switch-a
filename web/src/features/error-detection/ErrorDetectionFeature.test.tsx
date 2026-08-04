import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { ApiError, type ApiClient, type Provider } from "@/api";
import { parseAPICatalog } from "@/api/api-catalog";
import { APICatalogContext, ApiContext } from "@/api/context";
import {
  parseInternalErrorRuleListResponse,
  parseInternalErrorRuleStatsResponse,
} from "@/api/error-detection-decoders";
import { createMockApiClient } from "@/hooks/test-utils";
import apiCatalogFixture from "../../../../contracts/internal-error/v1/api-catalog.json";
import ruleListFixture from "../../../../contracts/internal-error/v1/rule-list.json";
import ruleStatsFixture from "../../../../contracts/internal-error/v1/rule-stats.json";
import type {
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

function createFeatureApi(): ApiClient {
  const api = createMockApiClient();
  api.errorDetection = {
    rules: {
      list: vi.fn(),
      get: vi.fn(),
      create: vi.fn(),
      update: vi.fn(),
      delete: vi.fn(),
      reorder: vi.fn(),
    },
    stats: { get: vi.fn() },
    testMessage: vi.fn(),
  };
  vi.mocked(api.providers.list).mockResolvedValue([provider]);
  vi.mocked(api.config.get).mockResolvedValue({
    defaults: { global_max_attempts: "3" },
    values: {},
  });
  vi.mocked(api.errorDetection.rules.list).mockResolvedValue(listResource("7"));
  vi.mocked(api.errorDetection.stats.get).mockResolvedValue(initialStats);
  return api;
}

function renderFeature(api: ApiClient) {
  const catalogRefetch = vi.fn().mockResolvedValue(undefined);
  return render(
    <ApiContext.Provider value={api}>
      <APICatalogContext.Provider
        value={{
          catalog,
          loading: false,
          error: null,
          refetch: catalogRefetch,
        }}
      >
        <ErrorDetectionFeature />
      </APICatalogContext.Provider>
    </ApiContext.Provider>,
  );
}

describe("ErrorDetectionFeature", () => {
  it("creates a normalized preset draft with the last received ETag", async () => {
    const user = userEvent.setup();
    const api = createFeatureApi();
    const createdRule: InternalErrorRule = {
      ...initialList.rules[0],
      id: "33333333-3333-4333-8333-333333333333",
      name: "Capacity response",
      target: { kind: "global" },
      position: 2,
    };
    vi.mocked(api.errorDetection.rules.create).mockResolvedValue(
      ruleResource("8", createdRule),
    );
    renderFeature(api);

    await user.click(await screen.findByRole("button", { name: "Add rule" }));
    const editor = screen
      .getByRole("heading", { name: "Create detection rule" })
      .closest("section")!;
    await user.click(
      within(editor).getByRole("button", { name: /Codex capacity/i }),
    );
    await user.type(
      within(editor).getByLabelText("Rule name"),
      "Capacity response",
    );
    await user.click(
      within(editor).getByRole("button", { name: "Create rule" }),
    );

    expect(api.errorDetection.rules.create).toHaveBeenCalledWith(
      {
        name: "Capacity response",
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
      },
      etag("7"),
    );
    expect(await screen.findByText("Detection rule created.")).toBeVisible();
    expect(
      screen.queryByRole("button", { name: "Create rule" }),
    ).not.toBeInTheDocument();
  });

  it("preserves a stale edit until refresh and explicit reapply", async () => {
    const user = userEvent.setup();
    const api = createFeatureApi();
    const serverRules = initialList.rules.map((rule) =>
      rule.id === initialList.rules[0].id
        ? { ...rule, name: "Server changed name" }
        : rule,
    );
    vi.mocked(api.errorDetection.rules.list)
      .mockResolvedValueOnce(listResource("7"))
      .mockResolvedValue(listResource("8", serverRules));
    const conflict = new ApiError(
      "REVISION_MISMATCH",
      "Rule revision changed",
      412,
      { current_revision: "8" },
    );
    const reappliedRule = { ...serverRules[0], name: "My retained draft" };
    vi.mocked(api.errorDetection.rules.update)
      .mockRejectedValueOnce(conflict)
      .mockResolvedValueOnce(ruleResource("9", reappliedRule));
    renderFeature(api);

    await user.click(
      await screen.findByRole("button", { name: "Edit Codex capacity" }),
    );
    const nameInput = screen.getByLabelText("Rule name");
    await user.clear(nameInput);
    await user.type(nameInput, "My retained draft");
    await user.click(screen.getByRole("button", { name: "Save rule" }));

    expect(
      await screen.findByRole("alert", {
        name: /Rule revision changed on the server/i,
      }),
    ).toHaveTextContent("revision 8");
    expect(nameInput).toHaveValue("My retained draft");
    await user.click(
      screen.getByRole("button", { name: "Refresh server rules" }),
    );
    expect(
      await screen.findByText(/Latest rule revision loaded/),
    ).toBeVisible();
    expect(screen.getByLabelText("Rule name")).toHaveValue("My retained draft");

    await user.click(screen.getByRole("button", { name: "Save rule" }));
    await waitFor(() =>
      expect(api.errorDetection.rules.update).toHaveBeenCalledTimes(2),
    );
    const updateCalls = vi.mocked(api.errorDetection.rules.update).mock.calls;
    expect(updateCalls[0]?.[2]).toBe(etag("7"));
    expect(updateCalls[1]?.[2]).toBe(etag("8"));
    expect(updateCalls[1]?.[1]).toMatchObject({
      name: "My retained draft",
    });
    expect(await screen.findByText("Detection rule updated.")).toBeVisible();
  });

  it("chains toggle and delete mutations through returned revisions", async () => {
    const user = userEvent.setup();
    const api = createFeatureApi();
    const reorderedRules = [
      { ...initialList.rules[1], position: 0 },
      { ...initialList.rules[0], position: 1 },
    ];
    const toggledRule = { ...reorderedRules[1], enabled: false };
    vi.mocked(api.errorDetection.rules.reorder).mockResolvedValue(
      listResource("8", reorderedRules),
    );
    vi.mocked(api.errorDetection.rules.update).mockResolvedValue(
      ruleResource("9", toggledRule),
    );
    vi.mocked(api.errorDetection.rules.delete).mockResolvedValue({
      rule_set_revision: "10",
      etag: etag("10"),
    });
    renderFeature(api);

    await user.click(
      await screen.findByRole("button", { name: "Move Codex capacity down" }),
    );
    await waitFor(() =>
      expect(api.errorDetection.rules.reorder).toHaveBeenCalled(),
    );
    expect(api.errorDetection.rules.reorder).toHaveBeenCalledWith(
      [initialList.rules[1].id, initialList.rules[0].id],
      etag("7"),
    );
    await user.click(
      screen.getByRole("button", { name: "Disable Codex capacity" }),
    );
    expect(api.errorDetection.rules.update).toHaveBeenCalledWith(
      initialList.rules[0].id,
      expect.objectContaining({ enabled: false }),
      etag("8"),
    );
    await user.click(
      screen.getByRole("button", {
        name: "Delete Observe upstream maintenance",
      }),
    );
    await user.click(screen.getByRole("button", { name: "Delete rule" }));

    await waitFor(() =>
      expect(api.errorDetection.rules.delete).toHaveBeenCalled(),
    );
    expect(api.errorDetection.rules.delete).toHaveBeenCalledWith(
      initialList.rules[1].id,
      etag("9"),
    );
    expect(await screen.findByText("Detection rule deleted.")).toBeVisible();
  });
});
