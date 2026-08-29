import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  render as testingLibraryRender,
  screen,
  waitFor,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactElement } from "react";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import type { Provider, RoutingPolicy } from "../api";
import { parseAPICatalog } from "../api/api-catalog";
import { APICatalogContext } from "../api/context";
import { RoutingPolicies } from "./RoutingPolicies";

const testAPICatalog = parseAPICatalog(
  JSON.parse(
    readFileSync(
      resolve(process.cwd(), "../contracts/internal-error/v1/api-catalog.json"),
      "utf8",
    ),
  ) as unknown,
);

function render(element: ReactElement) {
  return testingLibraryRender(
    <APICatalogContext.Provider
      value={{
        catalog: testAPICatalog,
        loading: false,
        error: null,
        refetch: () => Promise.resolve(),
      }}
    >
      {element}
    </APICatalogContext.Provider>,
  );
}

const useGroupsMock = vi.fn();
const useProvidersMock = vi.fn();
const useRoutingPoliciesMock = vi.fn();
const toast = {
  success: vi.fn(),
  error: vi.fn(),
};

vi.mock("../hooks/useGroups", () => ({
  useGroups: () => useGroupsMock(),
}));

vi.mock("../hooks/useProviders", () => ({
  useProviders: () => useProvidersMock(),
}));

vi.mock("../hooks/useRoutingPolicies", () => ({
  useRoutingPolicies: () => useRoutingPoliciesMock(),
}));

vi.mock("../hooks/useToast", () => ({
  useToast: () => toast,
}));

function buildProvider(overrides: Partial<Provider> = {}): Provider {
  return {
    id: "provider-1",
    name: "Primary OpenAI",
    api_types: [
      {
        api_type: "codex",
        base_url: "https://provider.example.com",
        credential_session_id: "credential-1",
      },
    ],
    auth_mode: "auto",
    credential_sessions: [
      {
        id: "credential-1",
        name: "Provider credential",
        kind: "api_key",
        version: 1,
        subject: { kind: "keyed_digest", value: "digest" },
        auth_state: { status: "active" },
      },
    ],
    usage_limit_policy: "switch_provider",
    usage_limit_policy_explicit: true,
    group_id: "group-1",
    weight: 1,
    priority: 1,
    concurrency: 5,
    max_retries: 2,
    vendor: "openai",
    failover_scope: "any",
    accept_failover: "any",
    enabled: true,
    created_at: "2026-03-22T12:00:00Z",
    updated_at: "2026-03-22T12:05:00Z",
    ...overrides,
  };
}

function buildPolicy(overrides: Partial<RoutingPolicy> = {}): RoutingPolicy {
  return {
    id: "policy-1",
    api_type: "codex",
    enabled: true,
    model_match_type: "exact",
    model_match_value: "gpt-5.1-codex",
    target_provider_id: null,
    allowed_group_ids: ["group-1"],
    allowed_vendors: ["openai"],
    created_at: "2026-03-22T12:00:00Z",
    updated_at: "2026-03-22T12:05:00Z",
    ...overrides,
  };
}

function mockPageState({
  policies = [],
  createPolicy = vi.fn(),
  updatePolicy = vi.fn(),
  deletePolicy = vi.fn(),
}: {
  policies?: RoutingPolicy[];
  createPolicy?: ReturnType<typeof vi.fn>;
  updatePolicy?: ReturnType<typeof vi.fn>;
  deletePolicy?: ReturnType<typeof vi.fn>;
} = {}) {
  useProvidersMock.mockReturnValue({
    providers: [buildProvider()],
    loading: false,
    error: null,
    refetch: vi.fn(),
  });
  useRoutingPoliciesMock.mockReturnValue({
    policies,
    loading: false,
    error: null,
    available: true,
    refetch: vi.fn(),
    createPolicy,
    updatePolicy,
    deletePolicy,
  });
}

beforeEach(() => {
  useGroupsMock.mockReset();
  useProvidersMock.mockReset();
  useRoutingPoliciesMock.mockReset();
  toast.success.mockReset();
  toast.error.mockReset();

  useGroupsMock.mockReturnValue({
    groups: [{ id: "group-1", name: "Core" }],
    refetch: vi.fn(),
  });
});

describe("RoutingPolicies", () => {
  it("renders exact-provider rules with lifecycle state", () => {
    mockPageState({
      policies: [
        buildPolicy({
          enabled: false,
          target_provider_id: "provider-1",
          allowed_group_ids: [],
          allowed_vendors: [],
        }),
      ],
    });

    render(<RoutingPolicies />);

    expect(screen.getByText("Routing Policies")).toBeInTheDocument();
    expect(screen.getByText("Disabled")).toBeInTheDocument();
    expect(screen.getAllByText("Exact provider")[0]).toBeInTheDocument();
    expect(screen.getByText("Primary OpenAI")).toBeInTheDocument();
  });

  it("submits exact-provider routing constraints", async () => {
    const user = userEvent.setup();
    const createPolicy = vi.fn().mockResolvedValue(
      buildPolicy({
        target_provider_id: "provider-1",
        allowed_group_ids: [],
        allowed_vendors: [],
      }),
    );
    mockPageState({ createPolicy });

    render(<RoutingPolicies />);

    await user.click(screen.getByRole("button", { name: /add policy/i }));
    await user.type(screen.getByPlaceholderText("codex"), "codex");
    await user.click(screen.getByRole("radio", { name: /Exact provider/i }));
    await user.selectOptions(
      screen.getByRole("combobox", { name: /Exact Provider/i }),
      "provider-1",
    );
    await user.click(screen.getByRole("button", { name: /create policy/i }));

    await waitFor(() =>
      expect(createPolicy).toHaveBeenCalledWith({
        api_type: "codex",
        enabled: true,
        model_match_type: null,
        model_match_value: null,
        target_provider_id: "provider-1",
        allowed_group_ids: [],
        allowed_vendors: [],
      }),
    );
    expect(toast.success).toHaveBeenCalledWith("Routing policy created");
  });

  it("preserves stale vendors on lifecycle-only edits", async () => {
    const user = userEvent.setup();
    const policy = buildPolicy({
      id: "policy-stale",
      enabled: false,
      allowed_vendors: ["legacy-vendor"],
    });
    const updatePolicy = vi.fn().mockResolvedValue({
      ...policy,
      enabled: true,
      updated_at: "2026-03-23T00:00:00Z",
    });
    mockPageState({ policies: [policy], updatePolicy });

    render(<RoutingPolicies />);

    await user.click(
      screen.getByRole("button", { name: /edit routing policy for codex/i }),
    );
    expect(
      screen.getByRole("checkbox", { name: /legacy-vendor \(stale\)/i }),
    ).toBeChecked();
    await user.click(screen.getByRole("checkbox", { name: /Enabled/i }));
    await user.click(screen.getByRole("button", { name: /update policy/i }));

    await waitFor(() =>
      expect(updatePolicy).toHaveBeenCalledWith("policy-stale", {
        api_type: "codex",
        enabled: true,
        model_match_type: "exact",
        model_match_value: "gpt-5.1-codex",
        target_provider_id: null,
        allowed_group_ids: ["group-1"],
        allowed_vendors: ["legacy-vendor"],
      }),
    );
  });

  it("blocks vendor-set changes while stale vendors remain", async () => {
    const user = userEvent.setup();
    const updatePolicy = vi.fn();
    mockPageState({
      policies: [
        buildPolicy({
          id: "policy-stale",
          allowed_vendors: ["legacy-vendor"],
        }),
      ],
      updatePolicy,
    });

    render(<RoutingPolicies />);

    await user.click(
      screen.getByRole("button", { name: /edit routing policy for codex/i }),
    );
    await user.click(screen.getByLabelText("openai"));
    await user.click(screen.getByRole("button", { name: /update policy/i }));

    const staleVendorMessages = await screen.findAllByText(
      "Remove stale vendors before changing vendor filters. Only the unchanged stored vendor set can survive catalog drift.",
    );
    expect(staleVendorMessages.length).toBeGreaterThan(0);
    expect(updatePolicy).not.toHaveBeenCalled();
  });

  it("toggles policy enabled state through the update flow", async () => {
    const user = userEvent.setup();
    const updatePolicy = vi
      .fn()
      .mockResolvedValue(
        buildPolicy({ enabled: false, updated_at: "2026-03-24T00:00:00Z" }),
      );
    mockPageState({ policies: [buildPolicy()], updatePolicy });

    render(<RoutingPolicies />);

    await user.click(
      screen.getByRole("button", { name: /disable routing policy for codex/i }),
    );

    await waitFor(() =>
      expect(updatePolicy).toHaveBeenCalledWith("policy-1", {
        api_type: "codex",
        enabled: false,
        model_match_type: "exact",
        model_match_value: "gpt-5.1-codex",
        target_provider_id: null,
        allowed_group_ids: ["group-1"],
        allowed_vendors: ["openai"],
      }),
    );
    expect(toast.success).toHaveBeenCalledWith("Routing policy disabled");
  });

  it("blocks duplicate rule keys before save", async () => {
    const user = userEvent.setup();
    const createPolicy = vi.fn();
    mockPageState({ policies: [buildPolicy()], createPolicy });

    render(<RoutingPolicies />);

    await user.click(screen.getByRole("button", { name: /add policy/i }));
    await user.type(screen.getByPlaceholderText("codex"), "codex");
    await user.selectOptions(
      screen.getByRole("combobox", { name: /Model Match/i }),
      "exact",
    );
    await user.type(
      screen.getByPlaceholderText("gpt-5.1-codex"),
      "gpt-5.1-codex",
    );
    await user.click(screen.getByLabelText("openai"));
    await user.click(screen.getByRole("button", { name: /create policy/i }));

    expect(
      await screen.findByText(
        "A rule with the same api_type and model match already exists.",
      ),
    ).toBeInTheDocument();
    expect(createPolicy).not.toHaveBeenCalled();
  });
});
