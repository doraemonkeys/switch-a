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
        transport: "http",
        base_url: "https://claude.example.com",
        credential_session_id: "credential-override",
      },
      {
        api_type: "codex",
        transport: "http",
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

describe("Codex transport credentials", () => {
  it.each(["shared", "distinct"])(
    "creates both transports with %s keys",
    async (mode) => {
      const user = userEvent.setup();
      const onSubmit = vi.fn().mockResolvedValue(undefined);
      const credentialSessions = createCredentialSessionsApi([]);
      renderModal(
        <ProviderModal onClose={vi.fn()} onSubmit={onSubmit} groups={[]} />,
        { credentialSessions } as unknown as ApiClient,
      );
      await user.type(screen.getByLabelText("Name"), "Dual Codex");
      await user.click(screen.getByRole("button", { name: "codex" }));
      await user.type(
        screen.getByLabelText("Base URL for codex"),
        "https://http.example.com",
      );
      await user.type(
        screen.getByLabelText("API key override for codex"),
        "http-key",
      );
      await user.click(screen.getByRole("checkbox", { name: "WebSocket" }));
      await user.clear(screen.getByLabelText("Base URL for codex WebSocket"));
      await user.type(
        screen.getByLabelText("Base URL for codex WebSocket"),
        "https://ws.example.com",
      );
      if (mode === "distinct") {
        await user.clear(
          screen.getByLabelText("API key override for codex WebSocket"),
        );
        await user.type(
          screen.getByLabelText("API key override for codex WebSocket"),
          "ws-key",
        );
      }
      await user.click(screen.getByRole("button", { name: /add provider/i }));
      await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
      const payload = onSubmit.mock.calls[0][0];
      expect(payload.api_types).toHaveLength(2);
      expect(payload.api_types[0]).toMatchObject({
        api_type: "codex",
        transport: "http",
        base_url: "https://http.example.com",
      });
      expect(payload.api_types[1]).toMatchObject({
        api_type: "codex",
        transport: "websocket",
        base_url: "https://ws.example.com",
      });
      expect(payload.new_credential_sessions).toHaveLength(
        mode === "shared" ? 1 : 2,
      );
      expect(
        payload.api_types[0].credential_session_id ===
          payload.api_types[1].credential_session_id,
      ).toBe(mode === "shared");
      expect(payload.new_credential_sessions[0].secret_data).toBe("http-key");
      if (mode === "distinct")
        expect(payload.new_credential_sessions[1].secret_data).toBe("ws-key");
    },
  );

  it("keeps the WS credential when HTTP is disabled while editing", async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn().mockResolvedValue(undefined);
    const initial = persistedSplitProvider();
    initial.api_types = [
      {
        api_type: "codex",
        transport: "http",
        base_url: "https://http.example.com",
        credential_session_id: "credential-default",
      },
      {
        api_type: "codex",
        transport: "websocket",
        base_url: "https://ws.example.com",
        credential_session_id: "credential-override",
      },
    ];
    const credentialSessions = createCredentialSessionsApi([
      apiKeySession("credential-default"),
      apiKeySession("credential-override"),
    ]);
    renderModal(
      <ProviderModal
        initialData={initial}
        onClose={vi.fn()}
        onSubmit={onSubmit}
        groups={[]}
      />,
      { credentialSessions } as unknown as ApiClient,
    );
    expect(
      await screen.findByLabelText("Current API key for codex WebSocket"),
    ).toHaveValue("secret-credential-override");
    await user.click(screen.getByRole("checkbox", { name: "HTTP / SSE" }));
    await user.click(screen.getByRole("button", { name: /save changes/i }));
    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
    expect(onSubmit.mock.calls[0][0].api_types).toEqual([initial.api_types[1]]);
    expect(credentialSessions.create).not.toHaveBeenCalled();
  });

  it("requires at least one GPT transport and can re-enable WS alone", async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn().mockResolvedValue(undefined);
    const credentialSessions = createCredentialSessionsApi([
      chatGPTSession("gpt", "test@example.com"),
    ]);
    renderModal(
      <ProviderModal onClose={vi.fn()} onSubmit={onSubmit} groups={[]} />,
      { credentialSessions } as unknown as ApiClient,
    );
    await user.type(screen.getByLabelText("Name"), "WS GPT");
    await user.selectOptions(
      screen.getByLabelText("Credential Type"),
      "chatgpt",
    );
    await user.selectOptions(
      screen.getByLabelText("Credential Session"),
      "gpt",
    );
    await user.click(screen.getByRole("checkbox", { name: "HTTP / SSE" }));
    await user.click(screen.getByRole("checkbox", { name: "WebSocket" }));
    await user.click(screen.getByRole("button", { name: /add provider/i }));
    expect(
      await screen.findByText("Enable at least one transport."),
    ).toBeInTheDocument();
    expect(onSubmit).not.toHaveBeenCalled();
    await user.click(screen.getByRole("checkbox", { name: "WebSocket" }));
    await user.click(screen.getByRole("button", { name: /add provider/i }));
    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
    expect(onSubmit.mock.calls[0][0].api_types).toEqual([
      expect.objectContaining({
        transport: "websocket",
        credential_session_id: "gpt",
      }),
    ]);
  });
});
