import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { CredentialSession } from "../../api";
import { CredentialSessions } from "./CredentialSessions";

const mocks = vi.hoisted(() => ({
  rename: vi.fn(),
  update: vi.fn(),
  remove: vi.fn(),
  refetch: vi.fn(),
  success: vi.fn(),
  error: vi.fn(),
  sessions: [] as CredentialSession[],
}));

vi.mock("../../hooks/useCredentialSessions", () => ({
  useCredentialSessions: () => ({
    credentialSessions: mocks.sessions,
    loading: false,
    error: null,
    refetch: mocks.refetch,
    renameCredentialSession: mocks.rename,
    updateCredentialSession: mocks.update,
    deleteCredentialSession: mocks.remove,
  }),
}));

vi.mock("../../hooks/useToast", () => ({
  useToast: () => ({ success: mocks.success, error: mocks.error }),
}));

function credential(
  id: string,
  routeReferences: CredentialSession["route_references"],
): CredentialSession {
  return {
    id,
    name: id === "shared" ? "Shared Claude key" : "Unused old key",
    kind: "api_key",
    secret_data: `secret-${id}`,
    version: 3,
    subject: { kind: "keyed_digest", value: `digest-${id}` },
    auth_state: { status: "active" },
    referenced_route_target_ids: [
      ...new Set(routeReferences.map((reference) => reference.provider_id)),
    ],
    route_references: routeReferences,
    created_at: "2026-08-29T00:00:00Z",
    updated_at: "2026-08-29T01:00:00Z",
  };
}

describe("CredentialSessions", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.rename.mockResolvedValue(undefined);
    mocks.update.mockResolvedValue(undefined);
    mocks.remove.mockResolvedValue(undefined);
    mocks.sessions = [
      credential("shared", [
        {
          provider_id: "provider-a",
          provider_name: "Claude Production",
          api_type: "claude",
        },
        {
          provider_id: "provider-a",
          provider_name: "Claude Production",
          api_type: "codex",
        },
      ]),
      credential("unused", []),
    ];
  });

  it("shows operator names and exact route references", () => {
    render(<CredentialSessions />);

    expect(screen.getByText("Shared Claude key")).toBeInTheDocument();
    expect(screen.getByText("Claude Production · claude")).toBeInTheDocument();
    expect(screen.getByText("Claude Production · codex")).toBeInTheDocument();
    const deleteButtons = screen.getAllByRole("button", { name: /delete/i });
    expect(deleteButtons[0]).toBeDisabled();
    expect(deleteButtons[1]).toBeEnabled();
  });

  it("renames and rotates a shared credential with its version guard", async () => {
    const user = userEvent.setup();
    render(<CredentialSessions />);

    await user.click(screen.getAllByRole("button", { name: /rename/i })[0]);
    const nameInput = screen.getByLabelText("Name");
    await user.clear(nameInput);
    await user.type(nameInput, "Claude Team Key");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(mocks.rename).toHaveBeenCalledWith("shared", {
      expected_version: 3,
      name: "Claude Team Key",
    });

    await user.click(screen.getAllByRole("button", { name: /rotate/i })[0]);
    expect(
      screen.getByText(/changes every one of the 2 referenced routes/),
    ).toBeInTheDocument();
    await user.type(screen.getByLabelText("New API Key"), "rotated-secret");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(mocks.update).toHaveBeenCalledWith("shared", {
      expected_version: 3,
      secret_data: "rotated-secret",
    });
  });

  it("deletes only an unreferenced credential after confirmation", async () => {
    const user = userEvent.setup();
    render(<CredentialSessions />);

    await user.click(screen.getAllByRole("button", { name: /delete/i })[1]);
    expect(
      screen.getByText(/Only credentials with no route references/),
    ).toBeInTheDocument();
    const deleteButtons = screen.getAllByRole("button", { name: "Delete" });
    await user.click(deleteButtons[deleteButtons.length - 1]);
    expect(mocks.remove).toHaveBeenCalledWith("unused");
  });
});
