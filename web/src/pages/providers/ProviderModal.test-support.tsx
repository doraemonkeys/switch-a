import { vi } from "vitest";
import { render } from "@testing-library/react";
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

const testAPICatalog = parseAPICatalog(
  JSON.parse(
    readFileSync(
      resolve(process.cwd(), "../contracts/internal-error/v1/api-catalog.json"),
      "utf8",
    ),
  ) as unknown,
);

export function apiKeySession(id: string): CredentialSession {
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

export function chatGPTSession(
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

export function createCredentialSessionsApi(sessions: CredentialSession[]) {
  let storedSessions = sessions;
  return {
    list: vi.fn().mockImplementation(() => Promise.resolve(storedSessions)),
    create: vi.fn().mockImplementation((input: CreateCredentialSessionInput) =>
      Promise.resolve({
        ...apiKeySession("credential-created"),
        kind: input.kind,
      }),
    ),
    reauthenticate: vi
      .fn()
      .mockImplementation((id: string): Promise<CredentialSession> => {
        const current = storedSessions.find((session) => session.id === id);
        if (!current) {
          return Promise.reject(
            new Error(`Credential session not found: ${id}`),
          );
        }
        const updated: CredentialSession = {
          ...current,
          version: current.version + 1,
          auth_state: { ...current.auth_state, status: "active" },
        };
        storedSessions = storedSessions.map((session) =>
          session.id === id ? updated : session,
        );
        return Promise.resolve(updated);
      }),
  };
}

export function renderModal(element: ReactElement, api: ApiClient) {
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

export function persistedSplitProvider(): Provider {
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

export function persistedMixedProvider(): Provider {
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
        transport: "http",
        base_url: "https://claude.example.com",
        credential_session_id: apiKey.id,
      },
      {
        api_type: "codex",
        transport: "http",
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

export function persistedGPTProvider(): Provider {
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
