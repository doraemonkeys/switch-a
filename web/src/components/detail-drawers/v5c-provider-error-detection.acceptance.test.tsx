import { act } from "react";
import { render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { describe, expect, it, vi } from "vitest";
import {
  parseAPICatalog,
  type APICatalog,
  type ApiClient,
  type Provider,
} from "@/api";
import { APICatalogContext, ApiContext } from "@/api/context";
import type {
  InternalErrorRule,
  InternalErrorRuleListResponse,
  RevisionedInternalErrorResource,
} from "@/features/error-detection/contracts";
import { createMockApiClient } from "@/hooks/test-utils";
import { readErrorDetectionPrefill } from "@/pages/error-detection-prefill";
import apiCatalogFixture from "../../../../contracts/internal-error/v1/api-catalog.json";
import { ProviderDetailDrawer } from "./ProviderDetailDrawer";
import { ProviderErrorDetectionSummary } from "./ProviderErrorDetectionSummary";

const parsedCatalog = parseAPICatalog(apiCatalogFixture);
const catalog: APICatalog = {
  ...parsedCatalog,
  api_types: parsedCatalog.api_types.map((entry) =>
    entry.api_type === "gemini"
      ? {
          ...entry,
          semantic_error_supported: false,
          response_protocol_ids: [],
        }
      : entry,
  ),
};

const provider = {
  id: "provider & north",
  name: "North Provider",
  api_types: [
    {
      api_type: "codex",
      base_url: "https://example.test/codex",
      credential_session_id: "credential-api-key",
    },
    {
      api_type: "claude",
      base_url: "https://example.test/claude",
      credential_session_id: "credential-api-key",
    },
    {
      api_type: "gemini",
      base_url: "https://example.test/gemini",
      credential_session_id: "credential-api-key",
    },
    {
      api_type: "custom:private",
      base_url: "https://example.test/private",
      credential_session_id: "credential-api-key",
    },
    {
      api_type: "codex",
      base_url: "https://duplicate.example.test/codex",
      credential_session_id: "credential-api-key",
    },
  ],
  auth_mode: "auto",
  credential_sessions: [
    {
      id: "credential-api-key",
      name: "API key credential",
      kind: "api_key",
      version: 1,
      subject: { kind: "keyed_digest", value: "digest" },
      auth_state: { status: "active" },
    },
  ],
  group_id: null,
  weight: 1,
  priority: 0,
  concurrency: 0,
  max_retries: 0,
  vendor: "",
  failover_scope: "any",
  accept_failover: "any",
  enabled: true,
  created_at: "2026-08-04T00:00:00Z",
  updated_at: "2026-08-04T00:00:00Z",
} satisfies Provider;

function makeRule(
  id: string,
  name: string,
  position: number,
  overrides: Partial<InternalErrorRule> = {},
): InternalErrorRule {
  return {
    id,
    name,
    enabled: true,
    target: { kind: "global" },
    api_type: "codex",
    keywords: ["server_is_overloaded"],
    match_mode: "any",
    action: { type: "passthrough" },
    position,
    created_at: "2026-08-04T00:00:00Z",
    updated_at: "2026-08-04T00:00:00Z",
    ...overrides,
  };
}

function resource(
  rules: readonly InternalErrorRule[],
): RevisionedInternalErrorResource<InternalErrorRuleListResponse> {
  return {
    value: {
      schema_version: 1,
      rule_set_revision: "7",
      rules,
    },
    etag: '"internal-error-rules/7"',
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

function createDrawerApi(
  listRules: ApiClient["errorDetection"]["rules"]["list"],
): ApiClient {
  const api = createMockApiClient();
  api.errorDetection = {
    rules: {
      list: listRules,
      get: vi.fn(),
      create: vi.fn(),
      update: vi.fn(),
      delete: vi.fn(),
      reorder: vi.fn(),
    },
    stats: { get: vi.fn() },
    testMessage: vi.fn(),
  };
  vi.mocked(api.logs.list).mockResolvedValue({
    logs: [],
    total: 0,
    limit: 5,
    offset: 0,
    sort_by: "created_at",
    sort_order: "desc",
  });
  return api;
}

function renderSummary(api: ApiClient) {
  return render(
    <MemoryRouter>
      <ApiContext.Provider value={api}>
        <APICatalogContext.Provider
          value={{
            catalog,
            loading: false,
            error: null,
            refetch: vi.fn().mockResolvedValue(undefined),
          }}
        >
          <ProviderErrorDetectionSummary provider={provider} />
        </APICatalogContext.Provider>
      </ApiContext.Provider>
    </MemoryRouter>,
  );
}

describe("V5C provider error-detection acceptance", () => {
  it("keeps the drawer summary read-only and links each supported configured protocol exactly once", async () => {
    const rules = [
      makeRule("11111111-1111-4111-8111-111111111111", "Global Codex rule", 0),
      makeRule(
        "22222222-2222-4222-8222-222222222222",
        "Provider all-API rule",
        1,
        {
          target: { kind: "provider", provider_id: provider.id },
          api_type: null,
        },
      ),
      makeRule(
        "33333333-3333-4333-8333-333333333333",
        "Other provider rule",
        2,
        {
          target: { kind: "provider", provider_id: "deleted-provider" },
        },
      ),
      makeRule("44444444-4444-4444-8444-444444444444", "Disabled rule", 3, {
        enabled: false,
      }),
      makeRule(
        "55555555-5555-4555-8555-555555555555",
        "Unsupported Gemini rule",
        4,
        { api_type: "gemini" },
      ),
    ];
    const listRules = vi.fn().mockResolvedValue(resource(rules));
    renderSummary(createDrawerApi(listRules));

    const effectiveRules = await screen.findByRole("list", {
      name: "Effective internal error rules",
    });
    expect(within(effectiveRules).getAllByRole("listitem")).toHaveLength(2);
    expect(within(effectiveRules).getByText("Global Codex rule")).toBeVisible();
    expect(
      within(effectiveRules).getByText("Provider all-API rule"),
    ).toBeVisible();
    expect(
      within(effectiveRules).queryByText("Other provider rule"),
    ).not.toBeInTheDocument();
    expect(
      within(effectiveRules).queryByText("Disabled rule"),
    ).not.toBeInTheDocument();
    expect(
      within(effectiveRules).queryByText("Unsupported Gemini rule"),
    ).not.toBeInTheDocument();

    const section = screen.getByRole("heading", {
      name: "Internal Error Detection",
    }).parentElement?.parentElement;
    expect(section).not.toBeNull();
    expect(within(section as HTMLElement).queryByRole("button")).toBeNull();
    expect(
      within(section as HTMLElement).getByText(/Read-only summary/),
    ).toBeVisible();

    const links = within(section as HTMLElement).getAllByRole("link");
    expect(links).toHaveLength(2);
    const codexLink = within(section as HTMLElement).getByRole("link", {
      name: "Manage Codex error detection rules for North Provider",
    });
    expect(codexLink).toHaveAttribute(
      "href",
      "/error-detection?scope=provider&provider_id=provider+%26+north&api_type=codex",
    );
    const query = codexLink.getAttribute("href")?.split("?")[1];
    expect(readErrorDetectionPrefill(new URLSearchParams(query))).toEqual({
      target: { kind: "provider", provider_id: provider.id },
      api_type: "codex",
    });
    expect(
      within(section as HTMLElement).getByRole("link", {
        name: "Manage Claude error detection rules for North Provider",
      }),
    ).toHaveAttribute(
      "href",
      "/error-detection?scope=provider&provider_id=provider+%26+north&api_type=claude",
    );
    expect(
      within(section as HTMLElement).getByLabelText(
        "Gemini: structured error detection unavailable",
      ),
    ).toBeVisible();
    expect(
      within(section as HTMLElement).getByLabelText(
        "custom:private: structured error detection unavailable",
      ),
    ).toBeVisible();
    expect(listRules).toHaveBeenCalledTimes(1);
  });

  it("exposes loading and failure without removing management links or retaining a deleted drawer", async () => {
    const pending =
      deferred<
        RevisionedInternalErrorResource<InternalErrorRuleListResponse>
      >();
    const listRules = vi.fn().mockReturnValue(pending.promise);
    const api = createDrawerApi(listRules);
    const { unmount } = renderSummary(api);

    expect(await screen.findByRole("status", { name: "" })).toHaveTextContent(
      "Loading effective rules",
    );
    expect(
      screen.getByRole("link", {
        name: "Manage Codex error detection rules for North Provider",
      }),
    ).toBeVisible();

    await act(async () => {
      pending.reject(new Error("rules offline"));
      await pending.promise.catch(() => undefined);
    });
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Rule summary unavailable: rules offline",
    );
    expect(
      screen.getByRole("link", {
        name: "Manage Codex error detection rules for North Provider",
      }),
    ).toBeVisible();

    unmount();
    render(
      <MemoryRouter>
        <ApiContext.Provider value={api}>
          <APICatalogContext.Provider
            value={{
              catalog,
              loading: false,
              error: null,
              refetch: vi.fn().mockResolvedValue(undefined),
            }}
          >
            <ProviderDetailDrawer
              provider={null}
              onClose={vi.fn()}
              onEdit={vi.fn()}
              onDelete={vi.fn()}
              onToggle={vi.fn()}
              onReset={vi.fn()}
              getGroupName={() => "Ungrouped"}
            />
          </APICatalogContext.Provider>
        </ApiContext.Provider>
      </MemoryRouter>,
    );
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(listRules).toHaveBeenCalledTimes(1);
    await waitFor(() => expect(api.logs.list).not.toHaveBeenCalled());
  });
});
