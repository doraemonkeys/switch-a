import { describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { PROVIDER_CREDENTIAL_TYPES } from "../../config/constants";
import { ProviderModal } from "./ProviderModal";
import type { ApiClient, CredentialSession } from "../../api";
import {
  chatGPTSession,
  createCredentialSessionsApi,
  persistedGPTProvider,
  persistedMixedProvider,
  renderModal,
} from "./ProviderModal.test-support";

describe("ProviderModal GPT credential precedence", () => {
  const tokenBlob = '{"tokens":{"access_token":"acc","refresh_token":"ref"}}';

  function gptCredentialAPI(existingSession: CredentialSession) {
    const credentialSessions = createCredentialSessionsApi([existingSession]);
    const api = {
      providers: {
        importChatGPTLogin: vi.fn().mockResolvedValue({
          login_id: "login-new-account",
          status: "completed",
          auth: {
            type: PROVIDER_CREDENTIAL_TYPES.CHATGPT,
            status: "active",
            email: "new@example.com",
            account_id: "account-new",
          },
        }),
      },
      credentialSessions,
    } as unknown as ApiClient;
    return { api, credentialSessions };
  }

  async function openGPTForm(
    user: ReturnType<typeof userEvent.setup>,
    status = "active",
  ) {
    await user.type(screen.getByLabelText("Name"), "GPT Credential Choice");
    await user.selectOptions(
      screen.getByLabelText("Credential Type"),
      PROVIDER_CREDENTIAL_TYPES.CHATGPT,
    );
    await screen.findByRole("option", {
      name: new RegExp(`existing@example\\.com · ${status}`),
    });
  }

  async function importGPTCredential(user: ReturnType<typeof userEvent.setup>) {
    await user.click(screen.getByLabelText("Import via token"));
    await user.paste(tokenBlob);
    await user.click(screen.getByRole("button", { name: /import token/i }));
    await screen.findByText(
      "Connected as new@example.com. Save the provider to persist it.",
    );
  }

  it("uses an existing session selected after a completed import", async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn().mockResolvedValue(undefined);
    const existingSession = chatGPTSession(
      "credential-existing-gpt",
      "existing@example.com",
    );
    const { api, credentialSessions } = gptCredentialAPI(existingSession);
    renderModal(
      <ProviderModal onClose={vi.fn()} onSubmit={onSubmit} groups={[]} />,
      api,
    );
    await openGPTForm(user);
    await importGPTCredential(user);

    await user.selectOptions(
      screen.getByLabelText("Credential Session"),
      existingSession.id,
    );

    expect(
      screen.queryByText(
        "Connected as new@example.com. Save the provider to persist it.",
      ),
    ).not.toBeInTheDocument();
    expect(
      screen.getByText("Account: existing@example.com"),
    ).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /add provider/i }));

    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
    expect(credentialSessions.create).not.toHaveBeenCalled();
    expect(onSubmit.mock.calls[0]?.[0]).toMatchObject({
      api_types: ["http", "websocket"].map((transport) =>
        expect.objectContaining({
          transport,
          credential_session_id: existingSession.id,
        }),
      ),
    });
  });

  it("creates a new session only after switching back to new GPT login", async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn().mockResolvedValue(undefined);
    const existingSession = chatGPTSession(
      "credential-existing-gpt",
      "existing@example.com",
    );
    const { api, credentialSessions } = gptCredentialAPI(existingSession);
    renderModal(
      <ProviderModal onClose={vi.fn()} onSubmit={onSubmit} groups={[]} />,
      api,
    );
    await openGPTForm(user);
    await user.selectOptions(
      screen.getByLabelText("Credential Session"),
      existingSession.id,
    );
    await user.click(
      screen.getByRole("button", { name: "Connect another GPT account" }),
    );
    await importGPTCredential(user);

    expect(screen.getByLabelText("Credential Session")).toHaveValue("");
    await user.click(screen.getByRole("button", { name: /add provider/i }));

    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
    expect(credentialSessions.create).toHaveBeenCalledWith({
      name: "GPT Credential Choice",
      kind: PROVIDER_CREDENTIAL_TYPES.CHATGPT,
      credential_login_id: "login-new-account",
    });
    expect(onSubmit.mock.calls[0]?.[0]).toMatchObject({
      api_types: ["http", "websocket"].map((transport) =>
        expect.objectContaining({
          transport,
          credential_session_id: "credential-created",
        }),
      ),
    });
  });

  it("reauthenticates a session selected while adding a provider", async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn().mockResolvedValue(undefined);
    const existingSession = {
      ...chatGPTSession(
        "credential-reconnect-gpt",
        "existing@example.com",
        "reauth_required",
      ),
      version: 9,
    };
    const { api, credentialSessions } = gptCredentialAPI(existingSession);
    renderModal(
      <ProviderModal onClose={vi.fn()} onSubmit={onSubmit} groups={[]} />,
      api,
    );
    await openGPTForm(user, "reauth_required");

    await user.selectOptions(
      screen.getByLabelText("Credential Session"),
      existingSession.id,
    );

    expect(
      screen.getByText(/Sign in again with the same GPT account/),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /reconnect gpt/i }),
    ).toBeInTheDocument();
    await user.click(screen.getByLabelText("Import via token"));
    await user.paste(tokenBlob);
    await user.click(screen.getByRole("button", { name: /import token/i }));
    await screen.findByText(
      "Reconnected as existing@example.com. Provider routes were not changed.",
    );
    expect(
      await screen.findByRole("option", {
        name: /existing@example\.com · active/,
      }),
    ).toBeInTheDocument();
    expect(credentialSessions.list).toHaveBeenCalledTimes(2);

    expect(credentialSessions.reauthenticate).toHaveBeenCalledWith(
      existingSession.id,
      {
        credential_login_id: "login-new-account",
      },
    );
    await user.click(screen.getByRole("button", { name: /add provider/i }));

    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
    expect(credentialSessions.create).not.toHaveBeenCalled();
    expect(onSubmit.mock.calls[0]?.[0]).toMatchObject({
      api_types: ["http", "websocket"].map((transport) =>
        expect.objectContaining({
          transport,
          credential_session_id: existingSession.id,
        }),
      ),
    });
  });

  it("reauthenticates an existing pure GPT provider without requiring a provider save", async () => {
    const user = userEvent.setup();
    const provider = persistedGPTProvider();
    const sessions = createCredentialSessionsApi(
      provider.credential_sessions as CredentialSession[],
    );
    const api = {
      providers: {
        importChatGPTLogin: vi.fn().mockResolvedValue({
          login_id: "login-pure-reconnect",
          status: "completed",
          auth: {
            type: PROVIDER_CREDENTIAL_TYPES.CHATGPT,
            status: "active",
            email: "mixed@example.com",
            account_id: "account-credential-gpt",
          },
        }),
      },
      credentialSessions: sessions,
    } as unknown as ApiClient;
    const onSubmit = vi.fn().mockResolvedValue(undefined);
    renderModal(
      <ProviderModal
        initialData={provider}
        onClose={vi.fn()}
        onSubmit={onSubmit}
        groups={[]}
      />,
      api,
    );

    await user.click(screen.getByLabelText("Import via token"));
    await user.paste(tokenBlob);
    await user.click(screen.getByRole("button", { name: /import token/i }));

    expect(
      await screen.findByText(
        "Reconnected as mixed@example.com. Provider routes were not changed.",
      ),
    ).toBeInTheDocument();
    expect(sessions.reauthenticate).toHaveBeenCalledWith("credential-gpt", {
      credential_login_id: "login-pure-reconnect",
    });
    expect(onSubmit).not.toHaveBeenCalled();
  });

  it("reauthenticates the GPT session of a mixed provider without rewriting routes", async () => {
    const user = userEvent.setup();
    const provider = persistedMixedProvider();
    const sessions = createCredentialSessionsApi(
      provider.credential_sessions as CredentialSession[],
    );
    const api = {
      providers: {
        importChatGPTLogin: vi.fn().mockResolvedValue({
          login_id: "login-mixed-reconnect",
          status: "completed",
          auth: {
            type: PROVIDER_CREDENTIAL_TYPES.CHATGPT,
            status: "active",
            email: "mixed@example.com",
            account_id: "account-credential-gpt",
          },
        }),
      },
      credentialSessions: sessions,
    } as unknown as ApiClient;
    const onSubmit = vi.fn().mockResolvedValue(undefined);
    const onReauthenticated = vi.fn().mockResolvedValue(undefined);
    renderModal(
      <ProviderModal
        initialData={provider}
        onClose={vi.fn()}
        onSubmit={onSubmit}
        onCredentialSessionReauthenticated={onReauthenticated}
        groups={[]}
      />,
      api,
    );

    const credentialType = screen.getByLabelText("Credential Type");
    expect(credentialType).toHaveValue("Mixed route credentials");
    expect(credentialType).toHaveAttribute("readonly");
    expect(
      screen.getByText(
        /Reconnection takes effect immediately for every provider/i,
      ),
    ).toBeInTheDocument();

    await user.click(screen.getByLabelText("Import via token"));
    await user.paste(tokenBlob);
    await user.click(screen.getByRole("button", { name: /import token/i }));

    await waitFor(() =>
      expect(sessions.reauthenticate).toHaveBeenCalledWith("credential-gpt", {
        credential_login_id: "login-mixed-reconnect",
      }),
    );
    expect(
      await screen.findByText(
        "Reconnected as mixed@example.com. Provider routes were not changed.",
      ),
    ).toBeInTheDocument();
    await waitFor(() => expect(onReauthenticated).toHaveBeenCalledTimes(1));
    expect(onSubmit).not.toHaveBeenCalled();

    await user.click(screen.getByRole("button", { name: /save changes/i }));
    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
    expect(onSubmit.mock.calls[0]?.[0]).toMatchObject({
      api_types: [
        {
          api_type: "claude",
          transport: "http",
          base_url: "https://claude.example.com",
          credential_session_id: "credential-api-key",
        },
        {
          api_type: "codex",
          transport: "http",
          base_url: "https://codex.example.com",
          credential_session_id: "credential-gpt",
        },
      ],
    });
    expect(sessions.create).not.toHaveBeenCalled();
  });

  it("keeps mixed-provider routes editable when reauthentication uses a different account", async () => {
    const user = userEvent.setup();
    const provider = persistedMixedProvider();
    const sessions = createCredentialSessionsApi(
      provider.credential_sessions as CredentialSession[],
    );
    sessions.reauthenticate.mockRejectedValueOnce(
      new Error(
        "The authenticated GPT account differs from this credential session. Select another session for the route instead.",
      ),
    );
    const api = {
      providers: {
        importChatGPTLogin: vi.fn().mockResolvedValue({
          login_id: "login-other-account",
          status: "completed",
          auth: {
            type: PROVIDER_CREDENTIAL_TYPES.CHATGPT,
            status: "active",
            email: "other@example.com",
            account_id: "other-account",
          },
        }),
      },
      credentialSessions: sessions,
    } as unknown as ApiClient;
    renderModal(
      <ProviderModal
        initialData={provider}
        onClose={vi.fn()}
        onSubmit={vi.fn()}
        groups={[]}
      />,
      api,
    );

    const tokenInput = screen.getByLabelText("Import via token");
    await user.click(tokenInput);
    await user.paste(tokenBlob);
    await user.click(screen.getByRole("button", { name: /import token/i }));

    expect(
      await screen.findByText(/authenticated GPT account differs/i),
    ).toBeInTheDocument();
    expect(tokenInput).toHaveValue(tokenBlob);
    expect(screen.getByLabelText("Base URL for claude")).toHaveValue(
      "https://claude.example.com",
    );
    expect(screen.getByLabelText("Base URL for codex")).toHaveValue(
      "https://codex.example.com",
    );
  });
});
