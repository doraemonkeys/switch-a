import { describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import type { ReactElement } from "react";
import {
  parseAPICatalog,
  type ApiClient,
  type CreateCredentialSessionInput,
  type CredentialSession,
  type Provider,
} from "../../api";
import { APICatalogContext, ApiContext } from "../../api/context";
import { AUTH_MODES, PROVIDER_CREDENTIAL_TYPES } from "../../config/constants";
import { ProviderModal } from "./ProviderModal";

const testAPICatalog = parseAPICatalog(
  JSON.parse(
    readFileSync(
      resolve(process.cwd(), "../contracts/internal-error/v1/api-catalog.json"),
      "utf8",
    ),
  ) as unknown,
);

function apiKeySession(id: string): CredentialSession {
  return {
    id,
    name: id,
    kind: PROVIDER_CREDENTIAL_TYPES.API_KEY,
    secret_data: `secret-${id}`,
    version: 1,
    subject: { kind: "keyed_digest", value: `digest-${id}` },
    auth_state: { status: "active" },
    referenced_route_target_ids: [],
    route_references: [],
    created_at: "2026-08-28T00:00:00Z",
    updated_at: "2026-08-28T00:00:00Z",
  };
}

function chatGPTSession(
  id: string,
  email: string,
  status: CredentialSession["auth_state"]["status"] = "active",
): CredentialSession {
  return {
    id,
    name: email,
    kind: PROVIDER_CREDENTIAL_TYPES.CHATGPT,
    version: 1,
    subject: { kind: "account", value: `account-${id}` },
    auth_state: {
      status,
      email,
      account_id: `account-${id}`,
    },
    referenced_route_target_ids: [],
    route_references: [],
    created_at: "2026-08-28T00:00:00Z",
    updated_at: "2026-08-28T00:00:00Z",
  };
}

function createCredentialSessionsApi(sessions: CredentialSession[]) {
  return {
    list: vi.fn().mockResolvedValue(sessions),
    create: vi.fn().mockImplementation((input: CreateCredentialSessionInput) =>
      Promise.resolve({
        ...apiKeySession("credential-created"),
        kind: input.kind,
      }),
    ),
    reauthenticate: vi
      .fn()
      .mockImplementation((id: string): Promise<CredentialSession> => {
        const current = sessions.find((session) => session.id === id);
        if (!current) {
          return Promise.reject(
            new Error(`Credential session not found: ${id}`),
          );
        }
        return Promise.resolve({
          ...current,
          version: current.version + 1,
          auth_state: { ...current.auth_state, status: "active" },
        });
      }),
  };
}

function renderModal(element: ReactElement, api: ApiClient) {
  return render(
    <ApiContext.Provider value={api}>
      <APICatalogContext.Provider
        value={{
          catalog: testAPICatalog,
          loading: false,
          error: null,
          refetch: () => Promise.resolve(),
        }}
      >
        {element}
      </APICatalogContext.Provider>
    </ApiContext.Provider>,
  );
}

function persistedSplitProvider(): Provider {
  return {
    id: "provider-split",
    name: "Split Credentials",
    api_types: [
      {
        api_type: "claude",
        base_url: "https://claude.example.com",
        credential_session_id: "credential-override",
      },
      {
        api_type: "codex",
        base_url: "https://codex.example.com",
        credential_session_id: "credential-default",
      },
    ],
    auth_mode: AUTH_MODES.AUTO,
    credential_sessions: [
      apiKeySession("credential-override"),
      apiKeySession("credential-default"),
    ],
    group_id: null,
    weight: 1,
    priority: 0,
    concurrency: 10,
    max_retries: 1,
    vendor: "",
    failover_scope: "any",
    accept_failover: "any",
    enabled: true,
    created_at: "2026-03-22T12:00:00Z",
    updated_at: "2026-03-22T12:00:00Z",
  };
}

function persistedMixedProvider(): Provider {
  const apiKey = apiKeySession("credential-api-key");
  const chatGPT = chatGPTSession(
    "credential-gpt",
    "mixed@example.com",
    "reauth_required",
  );
  return {
    id: "provider-mixed",
    name: "Mixed Credentials",
    api_types: [
      {
        api_type: "claude",
        base_url: "https://claude.example.com",
        credential_session_id: apiKey.id,
      },
      {
        api_type: "codex",
        base_url: "https://codex.example.com",
        credential_session_id: chatGPT.id,
      },
    ],
    auth_mode: AUTH_MODES.AUTO,
    credential_sessions: [apiKey, chatGPT],
    group_id: null,
    weight: 1,
    priority: 0,
    concurrency: 10,
    max_retries: 1,
    vendor: "",
    failover_scope: "any",
    accept_failover: "any",
    enabled: true,
    created_at: "2026-03-22T12:00:00Z",
    updated_at: "2026-03-22T12:00:00Z",
  };
}

function persistedGPTProvider(): Provider {
  const mixed = persistedMixedProvider();
  return {
    ...mixed,
    id: "provider-gpt",
    name: "GPT Credentials",
    auth_mode: AUTH_MODES.BEARER,
    api_types: mixed.api_types.filter((entry) => entry.api_type === "codex"),
    credential_sessions: mixed.credential_sessions.filter(
      (session) => session.kind === PROVIDER_CREDENTIAL_TYPES.CHATGPT,
    ),
  };
}

describe("ProviderModal credential binding precedence", () => {
  it("reveals and copies the current API key without creating a replacement", async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn().mockResolvedValue(undefined);
    const credentialSessions = createCredentialSessionsApi([
      apiKeySession("credential-override"),
      apiKeySession("credential-default"),
    ]);
    const api = { credentialSessions } as unknown as ApiClient;

    renderModal(
      <ProviderModal
        initialData={persistedSplitProvider()}
        onClose={vi.fn()}
        onSubmit={onSubmit}
        groups={[]}
      />,
      api,
    );

    const currentKey = await screen.findByLabelText(
      "Current API key for claude",
    );
    expect(currentKey).toHaveAttribute("type", "password");
    expect(currentKey).toHaveValue("secret-credential-override");

    await user.click(
      screen.getByRole("button", {
        name: "Show current API key for claude",
      }),
    );
    expect(currentKey).toHaveAttribute("type", "text");
    await user.click(screen.getAllByRole("button", { name: "Copy" })[0]);
    expect(await navigator.clipboard.readText()).toBe(
      "secret-credential-override",
    );

    await user.click(screen.getByRole("button", { name: /save changes/i }));
    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
    expect(credentialSessions.create).not.toHaveBeenCalled();
  });

  it("preserves existing bindings when a shared key credentials a new route", async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn().mockResolvedValue(undefined);
    const credentialSessions = createCredentialSessionsApi([
      apiKeySession("credential-override"),
      apiKeySession("credential-default"),
    ]);
    const api = { credentialSessions } as unknown as ApiClient;

    renderModal(
      <ProviderModal
        initialData={persistedSplitProvider()}
        onClose={vi.fn()}
        onSubmit={onSubmit}
        groups={[]}
      />,
      api,
    );

    expect(
      screen.queryByLabelText("New Shared API Key"),
    ).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "gemini" }));
    await user.type(screen.getByLabelText("New Shared API Key"), "shared-key");
    await user.clear(screen.getByLabelText("Base URL for gemini"));
    await user.type(
      screen.getByLabelText("Base URL for gemini"),
      "https://gemini.example.com",
    );
    await user.click(screen.getByRole("button", { name: /save changes/i }));

    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
    const submitted = onSubmit.mock.calls[0]?.[0];
    const createdSession = submitted.new_credential_sessions?.[0];
    expect(createdSession).toMatchObject({
      name: "Split Credentials",
      kind: PROVIDER_CREDENTIAL_TYPES.API_KEY,
      secret_data: "shared-key",
    });
    expect(submitted).toEqual(
      expect.objectContaining({
        api_types: [
          {
            api_type: "claude",
            base_url: "https://claude.example.com",
            credential_session_id: "credential-override",
          },
          {
            api_type: "codex",
            base_url: "https://codex.example.com",
            credential_session_id: "credential-default",
          },
          {
            api_type: "gemini",
            base_url: "https://gemini.example.com",
            credential_session_id: createdSession?.id,
          },
        ],
        new_credential_sessions: [createdSession],
      }),
    );
    expect(credentialSessions.create).not.toHaveBeenCalled();
  });

  it("uses a shared key only for new routes without a selected session", async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn().mockResolvedValue(undefined);
    const credentialSessions = createCredentialSessionsApi([
      apiKeySession("credential-selected"),
    ]);
    const api = { credentialSessions } as unknown as ApiClient;

    renderModal(
      <ProviderModal onClose={vi.fn()} onSubmit={onSubmit} groups={[]} />,
      api,
    );

    await user.type(screen.getByLabelText("Name"), "Mixed Bindings");
    await user.type(screen.getByLabelText("New Shared API Key"), "shared-key");
    await user.click(screen.getByRole("button", { name: "claude" }));
    await user.click(screen.getByRole("button", { name: "codex" }));
    await user.type(
      screen.getByLabelText("Base URL for claude"),
      "https://claude.example.com",
    );
    await user.type(
      screen.getByLabelText("Base URL for codex"),
      "https://codex.example.com",
    );
    await user.selectOptions(
      screen.getByLabelText("Credential session for claude"),
      "credential-selected",
    );
    await user.click(screen.getByRole("button", { name: /add provider/i }));

    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
    const submitted = onSubmit.mock.calls[0]?.[0];
    const createdSession = submitted.new_credential_sessions?.[0];
    expect(createdSession).toMatchObject({
      name: "Mixed Bindings",
      kind: PROVIDER_CREDENTIAL_TYPES.API_KEY,
      secret_data: "shared-key",
    });
    expect(submitted).toEqual(
      expect.objectContaining({
        api_types: [
          {
            api_type: "claude",
            base_url: "https://claude.example.com",
            credential_session_id: "credential-selected",
          },
          {
            api_type: "codex",
            base_url: "https://codex.example.com",
            credential_session_id: createdSession?.id,
          },
        ],
        new_credential_sessions: [createdSession],
      }),
    );
    expect(credentialSessions.create).not.toHaveBeenCalled();
  });

  it("retains a transactional API key draft when the provider write fails", async () => {
    const user = userEvent.setup();
    const onSubmit = vi
      .fn()
      .mockRejectedValueOnce(new Error("provider write failed"))
      .mockResolvedValueOnce(undefined);
    const credentialSessions = createCredentialSessionsApi([]);
    const api = { credentialSessions } as unknown as ApiClient;

    renderModal(
      <ProviderModal onClose={vi.fn()} onSubmit={onSubmit} groups={[]} />,
      api,
    );

    await user.type(screen.getByLabelText("Name"), "Retry Provider");
    await user.type(screen.getByLabelText("New Shared API Key"), "retry-key");
    await user.click(screen.getByRole("button", { name: "claude" }));
    await user.type(
      screen.getByLabelText("Base URL for claude"),
      "https://claude.example.com",
    );

    await user.click(screen.getByRole("button", { name: /add provider/i }));
    await screen.findByText("provider write failed");
    await user.click(screen.getByRole("button", { name: /add provider/i }));
    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(2));

    const first = onSubmit.mock.calls[0]?.[0];
    const second = onSubmit.mock.calls[1]?.[0];
    const firstSession = first.new_credential_sessions?.[0];
    const secondSession = second.new_credential_sessions?.[0];
    expect(firstSession).toMatchObject({
      name: "Retry Provider",
      secret_data: "retry-key",
    });
    expect(secondSession).toMatchObject({
      name: "Retry Provider",
      secret_data: "retry-key",
    });
    expect(secondSession?.id).not.toBe(firstSession?.id);
    expect(first.api_types[0]?.credential_session_id).toBe(firstSession?.id);
    expect(second.api_types[0]?.credential_session_id).toBe(secondSession?.id);
    expect(credentialSessions.create).not.toHaveBeenCalled();
  });
});

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

  async function openGPTForm(user: ReturnType<typeof userEvent.setup>) {
    await user.type(screen.getByLabelText("Name"), "GPT Credential Choice");
    await user.selectOptions(
      screen.getByLabelText("Credential Type"),
      PROVIDER_CREDENTIAL_TYPES.CHATGPT,
    );
    await screen.findByRole("option", {
      name: /existing@example\.com · active/,
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
      api_types: [
        expect.objectContaining({
          credential_session_id: existingSession.id,
        }),
      ],
    });
  });

  it("uses a completed import selected after an existing session", async () => {
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
      api_types: [
        expect.objectContaining({
          credential_session_id: "credential-created",
        }),
      ],
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
      expected_version: 1,
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
      screen.getByText(/every route sharing it recovers together/i),
    ).toBeInTheDocument();

    await user.click(screen.getByLabelText("Import via token"));
    await user.paste(tokenBlob);
    await user.click(screen.getByRole("button", { name: /import token/i }));

    await waitFor(() =>
      expect(sessions.reauthenticate).toHaveBeenCalledWith("credential-gpt", {
        expected_version: 1,
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
          base_url: "https://claude.example.com",
          credential_session_id: "credential-api-key",
        },
        {
          api_type: "codex",
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
