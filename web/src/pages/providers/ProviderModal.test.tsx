import { describe, expect, it, vi, afterEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ProviderModal } from "./ProviderModal";
import { ApiContext } from "../../api/context";
import type { ApiClient } from "../../api/client";
import type { Provider } from "../../api";
import {
  AUTH_MODES,
  CHATGPT_CODEX_BASE_URL,
  PROVIDER_CREDENTIAL_TYPES,
  PROVIDER_USAGE_LIMIT_POLICIES,
} from "../../config/constants";

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

function buildPersistedChatGPTProvider(): Provider {
  return {
    id: "provider-gpt",
    name: "GPT Provider",
    api_key: "",
    api_types: [
      {
        provider_id: "provider-gpt",
        api_type: "codex",
        base_url: CHATGPT_CODEX_BASE_URL,
        api_key: "",
      },
    ],
    auth_mode: AUTH_MODES.BEARER,
    credential_type: PROVIDER_CREDENTIAL_TYPES.CHATGPT,
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
    auth: {
      type: PROVIDER_CREDENTIAL_TYPES.CHATGPT,
      status: "reauth_required",
      email: "user@example.com",
      reason: "invalid_grant",
      last_error: "refresh_token_reused",
    },
  } as Provider;
}

describe("ProviderModal", () => {
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

    await waitFor(() =>
      expect(onSubmit).toHaveBeenCalledWith(
        expect.objectContaining({
          id: expect.stringMatching(/^split-credentials/),
          name: "Split Credentials",
          api_key: "",
          api_types: [
            {
              api_type: "claude",
              base_url: "https://api.example.com",
              api_key: "claude-key",
            },
          ],
        }),
      ),
    );
    await waitFor(() => expect(onClose).toHaveBeenCalled());
  });

  it("shows an error when neither the default key nor an override is provided", async () => {
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
      await screen.findByText(/api key is required for api type "claude"/i),
    ).toBeInTheDocument();
    expect(onSubmit).not.toHaveBeenCalled();
  });

  it("normalizes whitespace-only overrides before submit", async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn().mockResolvedValue(undefined);

    render(<ProviderModal onClose={vi.fn()} onSubmit={onSubmit} groups={[]} />);

    await user.type(screen.getByLabelText("Name"), "Whitespace Override");
    await user.type(
      screen.getByLabelText("Default API Key"),
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

    await waitFor(() =>
      expect(onSubmit).toHaveBeenCalledWith(
        expect.objectContaining({
          api_key: "default-key",
          api_types: [
            expect.objectContaining({
              api_type: "claude",
              api_key: "",
            }),
          ],
        }),
      ),
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
      credential_type: PROVIDER_CREDENTIAL_TYPES.CHATGPT,
      credential_login_id: "login-1",
      auth_mode: "bearer",
      api_key: "",
      api_types: [
        {
          api_type: "codex",
          base_url: CHATGPT_CODEX_BASE_URL,
          api_key: "",
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
    } as unknown as ApiClient;

    render(
      <ApiContext.Provider value={mockApi}>
        <ProviderModal onClose={vi.fn()} onSubmit={onSubmit} groups={[]} />
      </ApiContext.Provider>,
    );

    await user.type(screen.getByLabelText("Name"), "Switch Back");
    await user.type(screen.getByLabelText("Default API Key"), "default-key");
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

    expect(screen.getByLabelText("Default API Key")).toHaveValue("default-key");
    expect(screen.getByLabelText("Base URL for claude")).toHaveValue(
      "https://api.example.com",
    );
    expect(screen.getByLabelText("Auth Mode")).toHaveValue(
      AUTH_MODES.X_API_KEY,
    );

    await user.click(screen.getByRole("button", { name: /add provider/i }));

    await waitFor(() => expect(onSubmit).toHaveBeenCalled());
    const submitted = onSubmit.mock.calls[0]?.[0];
    expect(submitted).toMatchObject({
      credential_type: PROVIDER_CREDENTIAL_TYPES.API_KEY,
      api_key: "default-key",
      auth_mode: AUTH_MODES.X_API_KEY,
      api_types: [
        {
          api_type: "claude",
          base_url: "https://api.example.com",
          api_key: "",
        },
      ],
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
      credential_type: PROVIDER_CREDENTIAL_TYPES.CHATGPT,
      credential_login_id: "login-import",
      auth_mode: "bearer",
      api_key: "",
      api_types: [
        {
          api_type: "codex",
          base_url: CHATGPT_CODEX_BASE_URL,
          api_key: "",
        },
      ],
    });
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
