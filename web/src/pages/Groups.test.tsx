import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { Group } from "../api/types";
import { Groups } from "./Groups";

const useGroupsMock = vi.fn();
const toast = {
  success: vi.fn(),
  error: vi.fn(),
};

vi.mock("../hooks/useGroups", () => ({
  useGroups: () => useGroupsMock(),
}));

vi.mock("../hooks/useToast", () => ({
  useToast: () => toast,
}));

vi.mock("../components", () => ({
  GroupModal: () => null,
  ConfirmModal: () => null,
  GroupDetailDrawer: () => null,
}));

function buildGroup(overrides: Partial<Group> = {}): Group {
  return {
    id: "group-1",
    name: "Primary",
    strategy: "priority",
    priority: 1,
    weight: 1,
    enabled: true,
    providers: [],
    created_at: "2026-03-29T12:00:00Z",
    updated_at: "2026-03-29T12:05:00Z",
    ...overrides,
  };
}

function mockPageState(overrides: Record<string, unknown> = {}) {
  useGroupsMock.mockReturnValue({
    groups: [buildGroup()],
    loading: false,
    error: null,
    refetch: vi.fn(),
    createGroup: vi.fn(),
    updateGroup: vi.fn(),
    deleteGroup: vi.fn(),
    enableGroup: vi.fn().mockResolvedValue(undefined),
    disableGroup: vi.fn().mockResolvedValue(undefined),
    ...overrides,
  });
}

beforeEach(() => {
  useGroupsMock.mockReset();
  toast.success.mockReset();
  toast.error.mockReset();
});

describe("Groups", () => {
  it("renders lifecycle quick actions for enabled and disabled groups", () => {
    mockPageState({
      groups: [
        buildGroup({ id: "group-enabled", name: "Primary", enabled: true }),
        buildGroup({ id: "group-disabled", name: "Fallback", enabled: false }),
      ],
    });

    render(<Groups />);

    expect(
      screen.getByRole("button", { name: "Disable Group Primary" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Enable Group Fallback" }),
    ).toBeInTheDocument();
  });

  it("disables a group from the card quick action", async () => {
    const user = userEvent.setup();
    const disableGroup = vi.fn().mockResolvedValue(undefined);
    mockPageState({
      groups: [buildGroup({ id: "group-1", name: "Primary", enabled: true })],
      disableGroup,
    });

    render(<Groups />);

    await user.click(
      screen.getByRole("button", { name: "Disable Group Primary" }),
    );

    await waitFor(() => expect(disableGroup).toHaveBeenCalledWith("group-1"));
    expect(toast.success).toHaveBeenCalledWith('Group "Primary" disabled');
  });

  it("enables a group from the card quick action", async () => {
    const user = userEvent.setup();
    const enableGroup = vi.fn().mockResolvedValue(undefined);
    mockPageState({
      groups: [buildGroup({ id: "group-2", name: "Fallback", enabled: false })],
      enableGroup,
    });

    render(<Groups />);

    await user.click(
      screen.getByRole("button", { name: "Enable Group Fallback" }),
    );

    await waitFor(() => expect(enableGroup).toHaveBeenCalledWith("group-2"));
    expect(toast.success).toHaveBeenCalledWith('Group "Fallback" enabled');
  });

  it("surfaces lifecycle errors without swallowing the backend message", async () => {
    const user = userEvent.setup();
    const disableGroup = vi.fn().mockRejectedValue(new Error("toggle failed"));
    mockPageState({
      groups: [buildGroup({ id: "group-1", name: "Primary", enabled: true })],
      disableGroup,
    });

    render(<Groups />);

    await user.click(
      screen.getByRole("button", { name: "Disable Group Primary" }),
    );

    await waitFor(() => expect(disableGroup).toHaveBeenCalledWith("group-1"));
    expect(toast.error).toHaveBeenCalledWith("toggle failed");
  });
});
