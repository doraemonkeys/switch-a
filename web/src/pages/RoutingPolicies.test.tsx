import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { RoutingPolicy } from "../api";
import { RoutingPolicies } from "./RoutingPolicies";

const useGroupsMock = vi.fn();
const useRoutingPoliciesMock = vi.fn();
const toast = {
  success: vi.fn(),
  error: vi.fn(),
};

vi.mock("../hooks/useGroups", () => ({
  useGroups: () => useGroupsMock(),
}));

vi.mock("../hooks/useRoutingPolicies", () => ({
  useRoutingPolicies: () => useRoutingPoliciesMock(),
}));

vi.mock("../hooks/useToast", () => ({
  useToast: () => toast,
}));

function buildPolicy(overrides: Partial<RoutingPolicy> = {}): RoutingPolicy {
  return {
    id: "policy-1",
    api_type: "codex",
    model_match_type: "exact",
    model_match_value: "gpt-5.1-codex",
    allowed_group_ids: ["group-1"],
    allowed_vendors: ["openai"],
    created_at: "2026-03-22T12:00:00Z",
    updated_at: "2026-03-22T12:05:00Z",
    ...overrides,
  };
}

beforeEach(() => {
  useGroupsMock.mockReset();
  useRoutingPoliciesMock.mockReset();
  toast.success.mockReset();
  toast.error.mockReset();

  useGroupsMock.mockReturnValue({
    groups: [{ id: "group-1", name: "Core" }],
  });
});

describe("RoutingPolicies", () => {
  it("renders model-aware hard-routing rules", () => {
    useRoutingPoliciesMock.mockReturnValue({
      policies: [buildPolicy()],
      loading: false,
      error: null,
      available: true,
      refetch: vi.fn(),
      createPolicy: vi.fn(),
      updatePolicy: vi.fn(),
      deletePolicy: vi.fn(),
    });

    render(<RoutingPolicies />);

    expect(screen.getByText("Routing Policies")).toBeInTheDocument();
    expect(screen.getByText("exact: gpt-5.1-codex")).toBeInTheDocument();
    expect(screen.getByText("Core")).toBeInTheDocument();
    expect(screen.getByText("openai")).toBeInTheDocument();
  });

  it("submits api_type + model hard-routing constraints", async () => {
    const user = userEvent.setup();
    const createPolicy = vi.fn().mockResolvedValue(buildPolicy());

    useRoutingPoliciesMock.mockReturnValue({
      policies: [],
      loading: false,
      error: null,
      available: true,
      refetch: vi.fn(),
      createPolicy,
      updatePolicy: vi.fn(),
      deletePolicy: vi.fn(),
    });

    render(<RoutingPolicies />);

    await user.click(screen.getByRole("button", { name: /add policy/i }));
    await user.type(screen.getByPlaceholderText("codex"), "codex");
    await user.selectOptions(screen.getAllByRole("combobox")[1]!, "prefix");
    await user.type(screen.getByPlaceholderText("gpt-5"), "gpt-5");
    await user.click(screen.getByRole("checkbox"));
    await user.type(screen.getAllByRole("textbox").at(-1)!, "openai");
    await user.click(screen.getByRole("button", { name: /create policy/i }));

    await waitFor(() =>
      expect(createPolicy).toHaveBeenCalledWith({
        api_type: "codex",
        model_match_type: "prefix",
        model_match_value: "gpt-5",
        allowed_group_ids: ["group-1"],
        allowed_vendors: ["openai"],
      }),
    );
    expect(toast.success).toHaveBeenCalledWith("Routing policy created");
  });

  it("blocks duplicate rule keys before save", async () => {
    const user = userEvent.setup();
    const createPolicy = vi.fn();

    useRoutingPoliciesMock.mockReturnValue({
      policies: [buildPolicy()],
      loading: false,
      error: null,
      available: true,
      refetch: vi.fn(),
      createPolicy,
      updatePolicy: vi.fn(),
      deletePolicy: vi.fn(),
    });

    render(<RoutingPolicies />);

    await user.click(screen.getByRole("button", { name: /add policy/i }));
    await user.type(screen.getByPlaceholderText("codex"), "codex");
    await user.selectOptions(screen.getAllByRole("combobox")[1]!, "exact");
    await user.type(
      screen.getByPlaceholderText("gpt-5.1-codex"),
      "gpt-5.1-codex",
    );
    await user.click(screen.getByRole("checkbox"));
    await user.click(screen.getByRole("button", { name: /create policy/i }));

    expect(
      await screen.findByText(
        "A rule with the same api_type and model match already exists.",
      ),
    ).toBeInTheDocument();
    expect(createPolicy).not.toHaveBeenCalled();
  });
});
