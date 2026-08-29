import { describe, expect, it, vi, afterEach } from "vitest";
import {
  render as testingLibraryRender,
  screen,
  waitFor,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactElement } from "react";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { ProviderModal } from "./ProviderModal";
import { APICatalogContext, ApiContext } from "../../api/context";
import type { ApiClient } from "../../api/client";
import {
  parseAPICatalog,
  type CreateCredentialSessionInput,
  type CredentialSession,
  type Provider,
} from "../../api";
import {
  ADD_PROVIDER_DEFAULTS,
  AUTH_MODES,
  CHATGPT_CODEX_BASE_URL,
  PROVIDER_CREDENTIAL_TYPES,
  PROVIDER_USAGE_LIMIT_POLICIES,
} from "../../config/constants";

const testAPICatalog = parseAPICatalog(
  JSON.parse(
    readFileSync(
      resolve(process.cwd(), "../contracts/internal-error/v1/api-catalog.json"),
      "utf8",
    ),
  ) as unknown,
);

function credentialSession(
  input: CreateCredentialSessionInput,
  id = "credential-created",
): CredentialSession {
  return {
    id,
    name: input.name,
    kind: input.kind,
    version: 1,
    subject: {
      kind: input.kind === "chatgpt" ? "account" : "keyed_digest",
      value: input.kind === "chatgpt" ? "account-created" : "digest-created",
    },
    auth_state: { status: "active" },
    referenced_route_target_ids: [],
    route_references: [],
    created_at: "2026-08-28T00:00:00Z",
    updated_at: "2026-08-28T00:00:00Z",
  };
}

function createCredentialSessionsApi() {
  return {
    list: vi.fn().mockResolvedValue([]),
    create: vi
      .fn()
      .mockImplementation((input: CreateCredentialSessionInput) =>
        Promise.resolve(credentialSession(input)),
      ),
    reauthenticate: vi.fn(),
  };
}

function render(element: ReactElement) {
  const api = {
    credentialSessions: createCredentialSessionsApi(),
  } as unknown as ApiClient;
  return testingLibraryRender(
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

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

function buildPersistedChatGPTProvider(): Provider {
  const credentialSessionID = "credential-gpt";
  return {
    id: "provider-gpt",
    name: "GPT Provider",
    api_types: [
      {
        api_type: "codex",
        base_url: CHATGPT_CODEX_BASE_URL,
        credential_session_id: credentialSessionID,
      },
    ],
    auth_mode: AUTH_MODES.BEARER,
    credential_sessions: [
      {
        id: credentialSessionID,
        name: "GPT credential",
        kind: PROVIDER_CREDENTIAL_TYPES.CHATGPT,
        version: 1,
        subject: { kind: "account", value: "acct_test" },
        auth_state: {
          status: "reauth_required",
          email: "user@example.com",
          status_reason: "invalid_grant",
          last_error: "refresh_token_reused",
        },
      },
    ],
    usage_limit_policy: PROVIDER_USAGE_LIMIT_POLICIES.SUSPEND,
    usage_limit_policy_explicit: true,
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
  };
}

describe("ProviderModal", () => {
  it("submits the add-provider retry defaults", async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn().mockResolvedValue(undefined);

    render(<ProviderModal onClose={vi.fn()} onSubmit={onSubmit} groups={[]} />);

    expect(screen.getByLabelText("Max Retries")).toHaveValue(
      ADD_PROVIDER_DEFAULTS.MAX_RETRIES,
    );
    expect(screen.getByLabelText("Initial Delay")).toHaveValue(
      ADD_PROVIDER_DEFAULTS.BACKOFF.INITIAL_DELAY,
    );
    expect(screen.getByLabelText("Max Delay")).toHaveValue(
      ADD_PROVIDER_DEFAULTS.BACKOFF.MAX_DELAY,
    );
    expect(screen.getByLabelText("Multiplier")).toHaveValue(
      ADD_PROVIDER_DEFAULTS.BACKOFF.MULTIPLIER,
    );
    expect(
      screen.getByRole("checkbox", { name: /enable jitter/i }),
    ).toBeChecked();

    await user.type(screen.getByLabelText("Name"), "Retry Defaults");
    await user.type(screen.getByLabelText("New Shared API Key"), "default-key");
    await user.click(screen.getByRole("button", { name: "claude" }));
    await user.type(
      screen.getByLabelText("Base URL for claude"),
      "https://api.example.com",
    );
    await user.click(screen.getByRole("button", { name: /add provider/i }));

    await waitFor(() =>
      expect(onSubmit).toHaveBeenCalledWith(
        expect.objectContaining({
          max_retries: ADD_PROVIDER_DEFAULTS.MAX_RETRIES,
          backoff: {
            initial_delay: ADD_PROVIDER_DEFAULTS.BACKOFF.INITIAL_DELAY,
            max_delay: ADD_PROVIDER_DEFAULTS.BACKOFF.MAX_DELAY,
            multiplier: ADD_PROVIDER_DEFAULTS.BACKOFF.MULTIPLIER,
            jitter: ADD_PROVIDER_DEFAULTS.BACKOFF.JITTER,
          },
        }),
      ),
    );
  });

  it("preserves persisted retry settings in edit mode", async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn().mockResolvedValue(undefined);
    const persistedBackoff = {
      initial_delay: "100ms",
      max_delay: "5s",
      multiplier: 2.0,
      jitter: false,
    };
    const initialData: Provider = {
      id: "provider-existing",
      name: "Existing Provider",
      api_types: [
        {
          api_type: "claude",
          base_url: "https://api.example.com",
          credential_session_id: "credential-existing",
        },
      ],
      auth_mode: AUTH_MODES.AUTO,
      credential_sessions: [
        {
          id: "credential-existing",
          name: "Existing credential",
          kind: PROVIDER_CREDENTIAL_TYPES.API_KEY,
          version: 1,
          subject: { kind: "keyed_digest", value: "digest-existing" },
          auth_state: { status: "active" },
        },
      ],
      group_id: null,
      weight: 1,
      priority: 0,
      concurrency: 10,
      max_retries: 1,
      backoff: persistedBackoff,
      vendor: "",
      failover_scope: "any",
      accept_failover: "any",
      enabled: true,
      created_at: "2026-03-22T12:00:00Z",
      updated_at: "2026-03-22T12:00:00Z",
    };

    render(
      <ProviderModal
        initialData={initialData}
        onClose={vi.fn()}
        onSubmit={onSubmit}
        groups={[]}
      />,
    );

    expect(screen.getByLabelText("Max Retries")).toHaveValue(1);
    expect(screen.getByLabelText("Initial Delay")).toHaveValue("100ms");
    expect(screen.getByLabelText("Max Delay")).toHaveValue("5s");
    expect(screen.getByLabelText("Multiplier")).toHaveValue(2);
    expect(
      screen.getByRole("checkbox", { name: /enable jitter/i }),
    ).not.toBeChecked();

    await user.click(screen.getByRole("button", { name: /save changes/i }));

    await waitFor(() =>
      expect(onSubmit).toHaveBeenCalledWith(
        expect.objectContaining({
          max_retries: initialData.max_retries,
          backoff: persistedBackoff,
        }),
      ),
    );
  });

  it("submits when an API type provides its own key override", async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn().mockResolvedValue(undefined);
    const onClose = vi.fn();

    render(<ProviderModal onClose={onClose} onSubmit={onSubmit} groups={[]} />);

    await user.type(screen.getByLabelText("Name"), "Split Credentials");
    await user.click(screen.getByRole("button", { name: "claude" }));
    await user.type(
      screen.getByLabelText("Base URL for claude"),
      "https://api.example.com",
    );
    await user.type(
      screen.getByLabelText("API key override for claude"),
      "claude-key",
    );

    await user.click(screen.getByRole("button", { name: /add provider/i }));

    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
    const submitted = onSubmit.mock.calls[0]?.[0];
    const createdSession = submitted.new_credential_sessions?.[0];
    expect(createdSession).toMatchObject({
      name: "Split Credentials · claude",
      secret_data: "claude-key",
    });
    expect(submitted).toEqual(
      expect.objectContaining({
        id: expect.stringMatching(/^split-credentials/),
        name: "Split Credentials",
        api_types: [
          {
            api_type: "claude",
            base_url: "https://api.example.com",
            credential_session_id: createdSession?.id,
          },
        ],
        new_credential_sessions: [createdSession],
      }),
    );
    await waitFor(() => expect(onClose).toHaveBeenCalled());
  });

  it("shows an error when neither a shared key nor an override is provided", async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn().mockResolvedValue(undefined);

    render(<ProviderModal onClose={vi.fn()} onSubmit={onSubmit} groups={[]} />);

    await user.type(screen.getByLabelText("Name"), "Missing Key");
    await user.click(screen.getByRole("button", { name: "claude" }));
    await user.type(
      screen.getByLabelText("Base URL for claude"),
      "https://api.example.com",
    );

    await user.click(screen.getByRole("button", { name: /add provider/i }));

    expect(
      await screen.findByText(
        /credential session is required for api type "claude"/i,
      ),
    ).toBeInTheDocument();
    expect(onSubmit).not.toHaveBeenCalled();
  });

  it("normalizes whitespace-only overrides before submit", async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn().mockResolvedValue(undefined);

    render(<ProviderModal onClose={vi.fn()} onSubmit={onSubmit} groups={[]} />);

    await user.type(screen.getByLabelText("Name"), "Whitespace Override");
    await user.type(
      screen.getByLabelText("New Shared API Key"),
      "  default-key  ",
    );
    await user.click(screen.getByRole("button", { name: "claude" }));
    await user.type(
      screen.getByLabelText("Base URL for claude"),
      "https://api.example.com",
    );
    await user.type(
      screen.getByLabelText("API key override for claude"),
      "   ",
    );

    await user.click(screen.getByRole("button", { name: /add provider/i }));

    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
    const submitted = onSubmit.mock.calls[0]?.[0];
    const createdSession = submitted.new_credential_sessions?.[0];
    expect(createdSession).toMatchObject({
      name: "Whitespace Override",
      secret_data: "default-key",
    });
    expect(submitted).toEqual(
      expect.objectContaining({
        api_types: [
          expect.objectContaining({
            api_type: "claude",
            credential_session_id: createdSession?.id,
          }),
        ],
        new_credential_sessions: [createdSession],
      }),
    );
  });

  it("toggles visibility for an API key override", async () => {
    const user = userEvent.setup();

    render(<ProviderModal onClose={vi.fn()} onSubmit={vi.fn()} groups={[]} />);

    await user.type(screen.getByLabelText("Name"), "Visible Override");
    await user.click(screen.getByRole("button", { name: "claude" }));

    const overrideInput = screen.getByLabelText("API key override for claude");
    expect(overrideInput).toHaveAttribute("type", "password");

    await user.click(
      screen.getByRole("button", {
        name: "Show API key override for claude",
      }),
    );

    expect(overrideInput).toHaveAttribute("type", "text");
  });
});

describe("ProviderModal GPT login", () => {
  it("tracks GPT login completion by polling the login session", async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn().mockResolvedValue(undefined);
    const mockApi = {
      providers: {
        startChatGPTLogin: vi.fn().mockResolvedValue({
          login_id: "login-1",
          auth_url: "http://127.0.0.1:1455/auth/authorize",
        }),
        getChatGPTLoginStatus: vi
          .fn()
          .mockResolvedValueOnce({
            login_id: "login-1",
            status: "pending",
          })
          .mockResolvedValueOnce({
            login_id: "login-1",
            status: "completed",
            auth: {
              type: PROVIDER_CREDENTIAL_TYPES.CHATGPT,
              status: "active",
              email: "user@example.com",
              account_id: "acct_test",
            },
          }),
      },
      credentialSessions: createCredentialSessionsApi(),
    } as unknown as ApiClient;
    const openSpy = vi.spyOn(window, "open").mockReturnValue(null);

    render(
      <ApiContext.Provider value={mockApi}>
        <ProviderModal onClose={vi.fn()} onSubmit={onSubmit} groups={[]} />
      </ApiContext.Provider>,
    );

    await user.type(screen.getByLabelText("Name"), "GPT Session");
    await user.selectOptions(
      screen.getByLabelText("Credential Type"),
      PROVIDER_CREDENTIAL_TYPES.CHATGPT,
    );
    await user.click(screen.getByRole("button", { name: /start sign-in/i }));

    expect(mockApi.providers.startChatGPTLogin).toHaveBeenCalledTimes(1);
    expect(openSpy).toHaveBeenCalledWith(
      "http://127.0.0.1:1455/auth/authorize",
      "_blank",
      "noopener,noreferrer",
    );
    expect(screen.getByLabelText("GPT sign-in link")).toHaveValue(
      "http://127.0.0.1:1455/auth/authorize",
    );

    await waitFor(() => {
      expect(mockApi.providers.getChatGPTLoginStatus).toHaveBeenCalledTimes(1);
    });

    await waitFor(
      () => {
        expect(mockApi.providers.getChatGPTLoginStatus).toHaveBeenCalledTimes(
          2,
        );
      },
      { timeout: 2500 },
    );
    expect(
      await screen.findByText(
        "Connected as user@example.com. Save the provider to persist it.",
      ),
    ).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: /add provider/i }));

    await waitFor(() => expect(onSubmit).toHaveBeenCalled());
    const submitted = onSubmit.mock.calls[0]?.[0];
    expect(submitted).toMatchObject({
      auth_mode: "bearer",
      api_types: [
        {
          api_type: "codex",
          base_url: CHATGPT_CODEX_BASE_URL,
          credential_session_id: "credential-created",
        },
      ],
    });
    expect(submitted?.usage_limit_policy).toBeUndefined();
  });

  it("preserves the API-key draft when switching back after GPT login completes", async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn().mockResolvedValue(undefined);
    const mockApi = {
      providers: {
        startChatGPTLogin: vi.fn().mockResolvedValue({
          login_id: "login-2",
          auth_url: "http://127.0.0.1:1455/auth/authorize",
        }),
        getChatGPTLoginStatus: vi.fn().mockResolvedValue({
          login_id: "login-2",
          status: "completed",
          auth: {
            type: PROVIDER_CREDENTIAL_TYPES.CHATGPT,
            status: "active",
            email: "user@example.com",
            account_id: "acct_test",
          },
        }),
      },
      credentialSessions: createCredentialSessionsApi(),
    } as unknown as ApiClient;

    render(
      <ApiContext.Provider value={mockApi}>
        <ProviderModal onClose={vi.fn()} onSubmit={onSubmit} groups={[]} />
      </ApiContext.Provider>,
    );

    await user.type(screen.getByLabelText("Name"), "Switch Back");
    await user.type(screen.getByLabelText("New Shared API Key"), "default-key");
    await user.click(screen.getByRole("button", { name: "claude" }));
    await user.type(
      screen.getByLabelText("Base URL for claude"),
      "https://api.example.com",
    );
    await user.selectOptions(
      screen.getByLabelText("Auth Mode"),
      AUTH_MODES.X_API_KEY,
    );

    await user.selectOptions(
      screen.getByLabelText("Credential Type"),
      PROVIDER_CREDENTIAL_TYPES.CHATGPT,
    );
    await user.click(screen.getByRole("button", { name: /start sign-in/i }));

    expect(
      await screen.findByText(
        "Connected as user@example.com. Save the provider to persist it.",
      ),
    ).toBeInTheDocument();

    await user.selectOptions(
      screen.getByLabelText("Credential Type"),
      PROVIDER_CREDENTIAL_TYPES.API_KEY,
    );

    expect(screen.getByLabelText("New Shared API Key")).toHaveValue(
      "default-key",
    );
    expect(screen.getByLabelText("Base URL for claude")).toHaveValue(
      "https://api.example.com",
    );
    expect(screen.getByLabelText("Auth Mode")).toHaveValue(
      AUTH_MODES.X_API_KEY,
    );

    await user.click(screen.getByRole("button", { name: /add provider/i }));

    await waitFor(() => expect(onSubmit).toHaveBeenCalled());
    const submitted = onSubmit.mock.calls[0]?.[0];
    const createdSession = submitted.new_credential_sessions?.[0];
    expect(submitted).toMatchObject({
      auth_mode: AUTH_MODES.X_API_KEY,
      api_types: [
        {
          api_type: "claude",
          base_url: "https://api.example.com",
          credential_session_id: createdSession?.id,
        },
      ],
      new_credential_sessions: [createdSession],
    });
    expect(submitted?.usage_limit_policy).toBeUndefined();
  });

  it("renders reconnect-required auth state for persisted GPT providers", async () => {
    render(
      <ProviderModal
        initialData={buildPersistedChatGPTProvider()}
        onClose={vi.fn()}
        onSubmit={vi.fn()}
        groups={[]}
      />,
    );

    expect(
      await screen.findByText("Reconnect required for user@example.com."),
    ).toBeInTheDocument();
    expect(screen.getByText("reauth_required")).toBeInTheDocument();
    expect(screen.getByText("Reason: invalid_grant")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /reconnect gpt/i }),
    ).toBeInTheDocument();
  });
});

describe("ProviderModal token import", () => {
  it("imports a GPT account from pasted tokens and saves it", async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn().mockResolvedValue(undefined);
    const tokenBlob = '{"tokens":{"access_token":"acc","refresh_token":"ref"}}';
    const mockApi = {
      providers: {
        importChatGPTLogin: vi.fn().mockResolvedValue({
          login_id: "login-import",
          status: "completed",
          auth: {
            type: PROVIDER_CREDENTIAL_TYPES.CHATGPT,
            status: "active",
            email: "import@example.com",
            account_id: "acct_import",
          },
        }),
      },
      credentialSessions: createCredentialSessionsApi(),
    } as unknown as ApiClient;

    render(
      <ApiContext.Provider value={mockApi}>
        <ProviderModal onClose={vi.fn()} onSubmit={onSubmit} groups={[]} />
      </ApiContext.Provider>,
    );

    await user.type(screen.getByLabelText("Name"), "Imported GPT");
    await user.selectOptions(
      screen.getByLabelText("Credential Type"),
      PROVIDER_CREDENTIAL_TYPES.CHATGPT,
    );

    // Paste rather than type so the JSON braces are inserted literally.
    await user.click(screen.getByLabelText("Import via token"));
    await user.paste(tokenBlob);
    await user.click(screen.getByRole("button", { name: /import token/i }));

    expect(
      await screen.findByText(
        "Connected as import@example.com. Save the provider to persist it.",
      ),
    ).toBeInTheDocument();
    expect(mockApi.providers.importChatGPTLogin).toHaveBeenCalledWith(
      tokenBlob,
    );

    await user.click(screen.getByRole("button", { name: /add provider/i }));

    await waitFor(() => expect(onSubmit).toHaveBeenCalled());
    const submitted = onSubmit.mock.calls[0]?.[0];
    expect(submitted).toMatchObject({
      auth_mode: "bearer",
      api_types: [
        {
          api_type: "codex",
          base_url: CHATGPT_CODEX_BASE_URL,
          credential_session_id: "credential-created",
        },
      ],
    });
  });

  it("reuses the materialized GPT session when the provider write is retried", async () => {
    const user = userEvent.setup();
    const onClose = vi.fn();
    const onSubmit = vi
      .fn()
      .mockRejectedValueOnce(new Error("provider write failed"))
      .mockResolvedValueOnce(undefined);
    const credentialSessions = createCredentialSessionsApi();
    const mockApi = {
      providers: {
        importChatGPTLogin: vi.fn().mockResolvedValue({
          login_id: "login-retry",
          status: "completed",
          auth: {
            type: PROVIDER_CREDENTIAL_TYPES.CHATGPT,
            status: "active",
            account_id: "acct-retry",
          },
        }),
      },
      credentialSessions,
    } as unknown as ApiClient;

    render(
      <ApiContext.Provider value={mockApi}>
        <ProviderModal onClose={onClose} onSubmit={onSubmit} groups={[]} />
      </ApiContext.Provider>,
    );

    await user.type(screen.getByLabelText("Name"), "Retry GPT");
    await user.selectOptions(
      screen.getByLabelText("Credential Type"),
      PROVIDER_CREDENTIAL_TYPES.CHATGPT,
    );
    await user.click(screen.getByLabelText("Import via token"));
    await user.paste('{"tokens":{"access_token":"acc","refresh_token":"ref"}}');
    await user.click(screen.getByRole("button", { name: /import token/i }));
    await screen.findByText(
      "GPT login completed. Save the provider to persist it.",
    );
    await user.click(screen.getByRole("button", { name: /add provider/i }));

    expect(
      await screen.findByText("provider write failed"),
    ).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /add provider/i }));

    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(2));
    expect(credentialSessions.create).toHaveBeenCalledTimes(1);
    expect(onSubmit.mock.calls[1]?.[0]).toMatchObject({
      api_types: [
        expect.objectContaining({
          credential_session_id: "credential-created",
        }),
      ],
    });
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("keeps the pasted token editable when import fails", async () => {
    const user = userEvent.setup();
    const tokenBlob = '{"access_token":"acc"}';
    const mockApi = {
      providers: {
        importChatGPTLogin: vi
          .fn()
          .mockRejectedValue(new Error("auth data is missing a refresh token")),
      },
      credentialSessions: createCredentialSessionsApi(),
    } as unknown as ApiClient;

    render(
      <ApiContext.Provider value={mockApi}>
        <ProviderModal onClose={vi.fn()} onSubmit={vi.fn()} groups={[]} />
      </ApiContext.Provider>,
    );

    await user.type(screen.getByLabelText("Name"), "Failed Import");
    await user.selectOptions(
      screen.getByLabelText("Credential Type"),
      PROVIDER_CREDENTIAL_TYPES.CHATGPT,
    );

    const textarea = screen.getByLabelText("Import via token");
    await user.click(textarea);
    await user.paste(tokenBlob);
    await user.click(screen.getByRole("button", { name: /import token/i }));

    expect(
      await screen.findByText("auth data is missing a refresh token"),
    ).toBeInTheDocument();
    // A failed import must not discard the user's pasted credential.
    expect(textarea).toHaveValue(tokenBlob);
  });
});
