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
    kind: PROVIDER_CREDENTIAL_TYPES.API_KEY,
    version: 1,
    subject: { kind: "keyed_digest", value: `digest-${id}` },
    auth_state: { status: "active" },
    referenced_route_target_ids: [],
    created_at: "2026-08-28T00:00:00Z",
    updated_at: "2026-08-28T00:00:00Z",
  };
}

function chatGPTSession(id: string, email: string): CredentialSession {
  return {
    ...apiKeySession(id),
    kind: PROVIDER_CREDENTIAL_TYPES.CHATGPT,
    subject: { kind: "account", value: `account-${id}` },
    auth_state: {
      status: "active",
      email,
      account_id: `account-${id}`,
    },
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

describe("ProviderModal credential binding precedence", () => {
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

    await waitFor(() =>
      expect(onSubmit).toHaveBeenCalledWith(
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
              credential_session_id: "credential-created",
            },
          ],
        }),
      ),
    );
    expect(credentialSessions.create).toHaveBeenCalledTimes(1);
    expect(credentialSessions.create).toHaveBeenCalledWith({
      kind: PROVIDER_CREDENTIAL_TYPES.API_KEY,
      secret_data: "shared-key",
    });
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

    await waitFor(() =>
      expect(onSubmit).toHaveBeenCalledWith(
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
              credential_session_id: "credential-created",
            },
          ],
        }),
      ),
    );
    expect(credentialSessions.create).toHaveBeenCalledTimes(1);
    expect(credentialSessions.create).toHaveBeenCalledWith({
      kind: PROVIDER_CREDENTIAL_TYPES.API_KEY,
      secret_data: "shared-key",
    });
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
      name: "existing@example.com - active",
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
});
