import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { CredentialSession } from "../../api";
import { CredentialSessions } from "./CredentialSessions";

const mocks = vi.hoisted(() => ({
  create: vi.fn(),
  rename: vi.fn(),
  update: vi.fn(),
  remove: vi.fn(),
  refetch: vi.fn(),
  success: vi.fn(),
  error: vi.fn(),
  startLogin: vi.fn(),
  importLogin: vi.fn(),
  credentialLoginHook: vi.fn(),
  sessions: [] as CredentialSession[],
  loading: false,
  apiError: null as Error | null,
}));

vi.mock("../../hooks/useCredentialSessions", () => ({
  useCredentialSessions: () => ({
    credentialSessions: mocks.sessions,
    loading: mocks.loading,
    error: mocks.apiError,
    refetch: mocks.refetch,
    createCredentialSession: mocks.create,
    renameCredentialSession: mocks.rename,
    updateCredentialSession: mocks.update,
    deleteCredentialSession: mocks.remove,
  }),
}));

vi.mock("../../hooks/useToast", () => ({
  useToast: () => ({ success: mocks.success, error: mocks.error }),
}));

vi.mock("../../hooks/useChatGPTCredentialLogin", () => ({
  useChatGPTCredentialLogin: (args: unknown) => {
    mocks.credentialLoginHook(args);
    return {
      chatGPTStatus: null,
      chatGPTLoginError: null,
      startingChatGPTLogin: false,
      applyingChatGPTLogin: false,
      chatGPTLoginAuthURL: null,
      lastReauthenticatedSession: null,
      handleStartChatGPTLogin: mocks.startLogin,
      handleOpenChatGPTLoginPage: vi.fn(),
      handleImportChatGPTLogin: mocks.importLogin,
    };
  },
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

function chatGPTCredential(): CredentialSession {
  return {
    ...credential("gpt-session", [
      {
        provider_id: "provider-gpt",
        provider_name: "GPT Production",
        api_type: "codex",
      },
    ]),
    name: "GPT Team Login",
    kind: "chatgpt",
    secret_data: undefined,
    version: 7,
    subject: { kind: "account", value: "account-gpt" },
    auth_state: {
      status: "reauth_required",
      email: "team@example.com",
      account_id: "account-gpt",
      plan_type: "team",
      usage_snapshot: {
        five_hour: { used_percent: 45, window_seconds: 18000 },
      },
    },
  };
}

describe("CredentialSessions", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.loading = false;
    mocks.apiError = null;
    mocks.create.mockResolvedValue(undefined);
    mocks.rename.mockResolvedValue(undefined);
    mocks.update.mockResolvedValue(undefined);
    mocks.remove.mockResolvedValue(undefined);
    mocks.startLogin.mockResolvedValue(undefined);
    mocks.importLogin.mockResolvedValue(true);
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

  it("opens an in-place reconnect flow for GPT credentials", async () => {
    const user = userEvent.setup();
    mocks.sessions = [chatGPTCredential()];
    render(<CredentialSessions />);

    await user.click(screen.getByRole("button", { name: "Reconnect" }));

    expect(
      screen.getByRole("heading", { name: "Reconnect GPT Credential" }),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/Every referenced route recovers together/),
    ).toBeInTheDocument();
    expect(mocks.credentialLoginHook).toHaveBeenCalledWith(
      expect.objectContaining({
        initialCredentialSession: {
          sessionID: "gpt-session",
          expectedVersion: 7,
        },
      }),
    );
    await user.click(
      screen.getByRole("button", { name: "Reconnect with GPT Sign-In" }),
    );
    expect(mocks.startLogin).toHaveBeenCalledTimes(1);
  });

  it("filters credentials dynamically by search query and kind filter tabs", async () => {
    const user = userEvent.setup();
    mocks.sessions = [
      credential("shared", [
        {
          provider_id: "provider-a",
          provider_name: "Claude Production",
          api_type: "claude",
        },
      ]),
      chatGPTCredential(),
    ];
    render(<CredentialSessions />);

    expect(screen.getByText("Shared Claude key")).toBeInTheDocument();
    expect(screen.getByText("GPT Team Login")).toBeInTheDocument();

    // Filter by kind: API Keys only
    await user.click(screen.getByRole("button", { name: /API Keys/i }));
    expect(screen.getByText("Shared Claude key")).toBeInTheDocument();
    expect(screen.queryByText("GPT Team Login")).not.toBeInTheDocument();

    // Filter by kind: ChatGPT Sessions only
    await user.click(screen.getByRole("button", { name: /ChatGPT Sessions/i }));
    expect(screen.queryByText("Shared Claude key")).not.toBeInTheDocument();
    expect(screen.getByText("GPT Team Login")).toBeInTheDocument();

    // Reset to All Types
    await user.click(screen.getByRole("button", { name: /All Types/i }));
    expect(screen.getByText("Shared Claude key")).toBeInTheDocument();
    expect(screen.getByText("GPT Team Login")).toBeInTheDocument();

    // Search query
    const searchInput = screen.getByPlaceholderText(
      /Search by credential name/i,
    );
    await user.type(searchInput, "team@example.com");
    expect(screen.queryByText("Shared Claude key")).not.toBeInTheDocument();
    expect(screen.getByText("GPT Team Login")).toBeInTheDocument();
  });

  it("switches seamlessly between Card Grid View and Table View", async () => {
    const user = userEvent.setup();
    render(<CredentialSessions />);

    // Default is Grid View
    expect(screen.getByLabelText("Table View")).toBeInTheDocument();

    // Click Table View
    await user.click(screen.getByLabelText("Table View"));
    expect(screen.getByRole("table")).toBeInTheDocument();
    expect(screen.getByText("Secret / Account")).toBeInTheDocument();

    // Click Grid View
    await user.click(screen.getByLabelText("Grid View"));
    expect(screen.queryByRole("table")).not.toBeInTheDocument();
  });

  it("allows creating a new API key credential directly from the page", async () => {
    const user = userEvent.setup();
    render(<CredentialSessions />);

    await user.click(screen.getByRole("button", { name: /Add Credential/i }));

    expect(
      screen.getByRole("heading", { name: "Add New Credential" }),
    ).toBeInTheDocument();
    await user.type(screen.getByLabelText("Credential Name"), "New Gemini Key");
    await user.type(
      screen.getByLabelText("API Secret Key"),
      "gemini-secret-12345",
    );
    await user.click(screen.getByRole("button", { name: "Create Credential" }));

    expect(mocks.create).toHaveBeenCalledWith({
      name: "New Gemini Key",
      kind: "api_key",
      secret_data: "gemini-secret-12345",
    });
  });

  it("toggles password visibility with eye toggle button", async () => {
    const user = userEvent.setup();
    render(<CredentialSessions />);

    const secretInput = screen.getByLabelText("API key for Shared Claude key");
    expect(secretInput).toHaveAttribute("type", "password");

    const showButton = screen.getAllByLabelText("Show secret")[0];
    await user.click(showButton);
    expect(secretInput).toHaveAttribute("type", "text");

    const hideButton = screen.getByLabelText("Hide secret");
    await user.click(hideButton);
    expect(secretInput).toHaveAttribute("type", "password");
  });

  it("renders empty states when there are no credentials or search results", async () => {
    const user = userEvent.setup();
    mocks.sessions = [];
    const { rerender } = render(<CredentialSessions />);

    expect(screen.getByText("No Credentials Configured")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /Add Your First Credential/i }),
    ).toBeInTheDocument();

    // Test filtered empty state
    mocks.sessions = [credential("shared", [])];
    rerender(<CredentialSessions />);
    const searchInput = screen.getByPlaceholderText(
      /Search by credential name/i,
    );
    await user.type(searchInput, "nonexistent-query-12345");

    expect(
      screen.getByText("No matching credentials found"),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /Reset all filters/i }),
    ).toBeInTheDocument();
  });

  it("displays error alert when the query returns an error", () => {
    mocks.apiError = new Error("Failed to load credentials from server");
    render(<CredentialSessions />);

    expect(screen.getByRole("alert")).toHaveTextContent(
      "Failed to load credentials from server",
    );
  });
});
