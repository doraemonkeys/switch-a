import { render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { describe, expect, it, vi } from "vitest";
import type { APICatalog, ApiClient, Provider } from "../../api";
import { APICatalogContext, ApiContext } from "../../api/context";
import type { InternalErrorRule } from "../../features/error-detection/contracts";
import { ProviderDetailDrawer } from "./ProviderDetailDrawer";

const catalog: APICatalog = {
  schema_version: 1,
  custom_api_type_prefix: "custom:",
  api_types: [
    {
      api_type: "codex",
      label: "Codex",
      description: "OpenAI Responses API",
      display_order: 0,
      semantic_error_supported: true,
      response_protocol_ids: ["openai.responses.json.v1"],
    },
    {
      api_type: "gemini",
      label: "Gemini",
      description: "Gemini API",
      display_order: 1,
      semantic_error_supported: false,
      response_protocol_ids: [],
    },
  ],
};

function buildProvider(overrides: Partial<Provider> = {}): Provider {
  const credentialSession = {
    id: "credential-gpt",
    name: "GPT credential",
    kind: "chatgpt" as const,
    version: 1,
    subject: { kind: "account" as const, value: "acct_test" },
    auth_state: {
      status: "active" as const,
      email: "user@example.com",
      account_id: "acct_test",
      plan_type: "team",
      usage_snapshot: {
        fetched_at: "2026-03-22T12:05:00Z",
        plan_type: "team",
        five_hour: {
          used_percent: 22,
          window_seconds: 18_000,
          reset_at: "2026-03-22T17:00:00Z",
        },
        one_week: {
          used_percent: 58,
          window_seconds: 604_800,
          reset_at: "2026-03-29T00:00:00Z",
        },
      },
    },
  };
  return {
    id: "provider-gpt",
    name: "GPT Provider",
    api_types: [
      {
        api_type: "codex",
        base_url: "https://chatgpt.com/backend-api/codex",
        credential_session_id: credentialSession.id,
      },
    ],
    auth_mode: "auto",
    credential_sessions: [credentialSession],
    usage_limit_policy: "suspend",
    group_id: null,
    weight: 1,
    priority: 1,
    concurrency: 1,
    max_retries: 0,
    vendor: "",
    failover_scope: "any",
    accept_failover: "any",
    enabled: true,
    created_at: "2026-03-22T12:00:00Z",
    updated_at: "2026-03-22T12:00:00Z",
    ...overrides,
  };
}

function buildRule(
  overrides: Partial<InternalErrorRule> = {},
): InternalErrorRule {
  return {
    id: "11111111-1111-4111-8111-111111111111",
    name: "Global retry",
    enabled: true,
    target: { kind: "global" },
    api_type: "codex",
    keywords: ["internal error"],
    match_mode: "any",
    action: {
      type: "retry_only",
      max_retries: 2,
      backoff: {
        initial_delay: "250ms",
        max_delay: "2s",
        multiplier: 2,
        jitter: true,
      },
    },
    position: 0,
    created_at: "2026-03-22T12:00:00Z",
    updated_at: "2026-03-22T12:00:00Z",
    ...overrides,
  };
}

function renderDrawer({
  provider = buildProvider(),
  rules = [],
  ruleError,
  catalogValue = catalog,
}: {
  provider?: Provider | null;
  rules?: readonly InternalErrorRule[];
  ruleError?: Error;
  catalogValue?: APICatalog | null;
} = {}) {
  const listLogs = vi.fn().mockResolvedValue({ logs: [] });
  const listRules = ruleError
    ? vi.fn().mockRejectedValue(ruleError)
    : vi.fn().mockResolvedValue({
        value: {
          schema_version: 1,
          rule_set_revision: "7",
          rules,
        },
        etag: '"internal-error-rules/7"',
      });
  const mockApi = {
    logs: { list: listLogs },
    errorDetection: {
      rules: { list: listRules },
    },
  } as unknown as ApiClient;

  render(
    <MemoryRouter>
      <ApiContext.Provider value={mockApi}>
        <APICatalogContext.Provider
          value={{
            catalog: catalogValue,
            loading: catalogValue === null,
            error: null,
            refetch: vi.fn().mockResolvedValue(undefined),
          }}
        >
          <ProviderDetailDrawer
            provider={provider}
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

  return { listLogs, listRules };
}

describe("ProviderDetailDrawer", () => {
  it("renders GPT plan and quota windows for chatgpt providers", async () => {
    const { listLogs } = renderDrawer();

    await waitFor(() => expect(listLogs).toHaveBeenCalled());
    await screen.findByText("No recent requests");

    expect(screen.getByText("Authentication")).toBeInTheDocument();
    expect(screen.getByText("active")).toBeInTheDocument();
    expect(screen.getByText("Team")).toBeInTheDocument();
    expect(screen.getByText("5 Hours")).toBeInTheDocument();
    expect(screen.getByText("1 Week")).toBeInTheDocument();
    expect(screen.getByText(/22% used/)).toBeInTheDocument();
    expect(screen.getByText(/58% used/)).toBeInTheDocument();
    expect(screen.getByText("Usage Updated")).toBeInTheDocument();
    expect(screen.getByText("Suspend Until Reset")).toBeInTheDocument();
  });

  it("renders reconnect-required auth diagnostics from the explicit auth snapshot", async () => {
    const provider = buildProvider({
      credential_sessions: [
        {
          id: "credential-gpt",
          name: "GPT credential",
          kind: "chatgpt",
          version: 1,
          subject: { kind: "account", value: "acct_test" },
          auth_state: {
            status: "reauth_required",
            status_reason: "invalid_grant",
            last_error: "refresh_token_reused",
            email: "user@example.com",
          },
        },
      ],
    });

    renderDrawer({ provider });
    await screen.findByText("No recent requests");

    expect(screen.getByText("reauth_required")).toBeInTheDocument();
    expect(screen.getByText("invalid_grant")).toBeInTheDocument();
    expect(screen.getByText("refresh_token_reused")).toBeInTheDocument();
    expect(
      screen.getByText(
        /Reconnect this provider from Edit before manual sync can resume/i,
      ),
    ).toBeInTheDocument();
  });

  it("clarifies that accept failover does not block pre-visible replacement", async () => {
    renderDrawer({
      provider: buildProvider({
        vendor: "openai",
        failover_scope: "vendor",
        accept_failover: "none",
      }),
    });
    await screen.findByText("No recent requests");

    expect(
      screen.getByText(
        /Pre-visible provider replacement is not blocked by these settings/i,
      ),
    ).toBeInTheDocument();
    expect(screen.getByText("Accept Failover From")).toBeInTheDocument();
  });

  it("shows only enabled global and matching-provider rules in its read-only summary", async () => {
    const providerRule = buildRule({
      id: "22222222-2222-4222-8222-222222222222",
      name: "Provider pass through",
      target: { kind: "provider", provider_id: "provider-gpt" },
      api_type: null,
      action: { type: "passthrough" },
      position: 1,
    });
    const deletedProviderRule = buildRule({
      id: "33333333-3333-4333-8333-333333333333",
      name: "Deleted provider rule",
      target: { kind: "provider", provider_id: "provider-deleted" },
      position: 2,
    });
    const disabledRule = buildRule({
      id: "44444444-4444-4444-8444-444444444444",
      name: "Disabled provider rule",
      enabled: false,
      target: { kind: "provider", provider_id: "provider-gpt" },
      position: 3,
    });

    renderDrawer({
      rules: [buildRule(), providerRule, deletedProviderRule, disabledRule],
    });

    const ruleList = await screen.findByRole("list", {
      name: "Effective internal error rules",
    });
    expect(within(ruleList).getByText("Global retry")).toBeInTheDocument();
    expect(
      within(ruleList).getByText("Provider pass through"),
    ).toBeInTheDocument();
    expect(
      within(ruleList).queryByText("Deleted provider rule"),
    ).not.toBeInTheDocument();
    expect(
      within(ruleList).queryByText("Disabled provider rule"),
    ).not.toBeInTheDocument();

    const section = screen.getByText("Internal Error Detection").parentElement
      ?.parentElement;
    expect(section).not.toBeNull();
    expect(within(section as HTMLElement).queryByRole("button")).toBeNull();
    expect(
      within(section as HTMLElement).getByText(/Read-only summary/),
    ).toBeInTheDocument();
  });

  it("builds encoded provider/API prefill links for every supported configured API type", async () => {
    const provider = buildProvider({
      id: "provider & west",
      name: "West Provider",
      api_types: [
        {
          api_type: "codex",
          base_url: "https://example.test/codex",
          credential_session_id: "credential-gpt",
        },
      ],
    });

    renderDrawer({ provider });

    const link = await screen.findByRole("link", {
      name: "Manage Codex error detection rules for West Provider",
    });
    expect(link).toHaveAttribute(
      "href",
      "/error-detection?scope=provider&provider_id=provider+%26+west&api_type=codex",
    );
  });

  it("renders custom and catalog-unsupported API types as accessible non-actions", async () => {
    const provider = buildProvider({
      api_types: [
        {
          api_type: "custom:private",
          base_url: "https://example.test/custom",
          credential_session_id: "credential-gpt",
        },
        {
          api_type: "gemini",
          base_url: "https://example.test/gemini",
          credential_session_id: "credential-gpt",
        },
      ],
    });

    renderDrawer({ provider });

    expect(
      await screen.findByLabelText(
        "custom:private: structured error detection unavailable",
      ),
    ).toBeInTheDocument();
    expect(
      screen.getByLabelText("Gemini: structured error detection unavailable"),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("link", { name: /custom:private|Gemini/ }),
    ).not.toBeInTheDocument();
    expect(
      screen.getByText("No enabled global or provider rules currently apply."),
    ).toBeInTheDocument();
  });

  it("keeps management links available when the read-only rule request fails", async () => {
    renderDrawer({ ruleError: new Error("rules offline") });

    expect(
      await screen.findByRole("alert", {
        name: "",
      }),
    ).toHaveTextContent("Rule summary unavailable: rules offline");
    expect(
      screen.getByRole("link", {
        name: "Manage Codex error detection rules for GPT Provider",
      }),
    ).toBeInTheDocument();
  });

  it("renders no stale drawer or rule query after the selected provider is deleted", () => {
    const { listRules } = renderDrawer({ provider: null });

    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(listRules).not.toHaveBeenCalled();
  });
});
