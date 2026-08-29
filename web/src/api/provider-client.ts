import {
  parseCredentialSession,
  parseCredentialSessions,
  parseProvider,
  parseProviders,
} from "./provider-contract";
import type {
  BatchProviderRequest,
  BatchProviderResponse,
  ChatGPTLoginStartResponse,
  ChatGPTLoginStatusResponse,
  CodexAuthDocument,
  CreateCredentialSessionInput,
  HealthState,
  ProviderInput,
  ReauthenticateCredentialSessionInput,
  RenameCredentialSessionInput,
  UpdateCredentialSessionInput,
} from "./types";

type AuthenticatedRequest = <T>(
  endpoint: string,
  options?: RequestInit,
) => Promise<T>;

export function createProvidersApi(request: AuthenticatedRequest) {
  return {
    list: async () => parseProviders(await request<unknown>("/providers")),
    get: async (id: string) =>
      parseProvider(await request<unknown>(`/providers/${id}`)),
    create: async (data: ProviderInput) =>
      parseProvider(
        await request<unknown>("/providers", {
          method: "POST",
          body: JSON.stringify(data),
        }),
      ),
    update: async (id: string, data: ProviderInput) =>
      parseProvider(
        await request<unknown>(`/providers/${id}`, {
          method: "PUT",
          body: JSON.stringify(data),
        }),
      ),
    delete: (id: string) =>
      request<void>(`/providers/${id}`, { method: "DELETE" }),
    enable: async (id: string) =>
      parseProvider(
        await request<unknown>(`/providers/${id}/enable`, { method: "POST" }),
      ),
    disable: async (id: string) =>
      parseProvider(
        await request<unknown>(`/providers/${id}/disable`, { method: "POST" }),
      ),
    reset: (id: string) =>
      request<HealthState>(`/providers/${id}/reset`, { method: "POST" }),
    startChatGPTLogin: () =>
      request<ChatGPTLoginStartResponse>("/provider-auth/chatgpt/start", {
        method: "POST",
      }),
    getChatGPTLoginStatus: (loginId: string) =>
      request<ChatGPTLoginStatusResponse>(
        `/provider-auth/chatgpt/sessions/${encodeURIComponent(loginId)}`,
      ),
    importChatGPTLogin: (authData: string) =>
      request<ChatGPTLoginStatusResponse>("/provider-auth/chatgpt/import", {
        method: "POST",
        body: JSON.stringify({ auth_data: authData }),
      }),
    batch: (data: BatchProviderRequest) =>
      request<BatchProviderResponse>("/providers/batch", {
        method: "POST",
        body: JSON.stringify(data),
      }),
  };
}

export function createCredentialSessionsApi(request: AuthenticatedRequest) {
  return {
    list: async () =>
      parseCredentialSessions(await request<unknown>("/credential-sessions")),
    get: async (id: string) =>
      parseCredentialSession(
        await request<unknown>(
          `/credential-sessions/${encodeURIComponent(id)}`,
        ),
      ),
    create: async (data: CreateCredentialSessionInput) =>
      parseCredentialSession(
        await request<unknown>("/credential-sessions", {
          method: "POST",
          body: JSON.stringify(data),
        }),
      ),
    update: async (id: string, data: UpdateCredentialSessionInput) =>
      parseCredentialSession(
        await request<unknown>(
          `/credential-sessions/${encodeURIComponent(id)}`,
          {
            method: "PUT",
            body: JSON.stringify(data),
          },
        ),
      ),
    rename: async (id: string, data: RenameCredentialSessionInput) =>
      parseCredentialSession(
        await request<unknown>(
          `/credential-sessions/${encodeURIComponent(id)}/name`,
          {
            method: "PATCH",
            body: JSON.stringify(data),
          },
        ),
      ),
    reauthenticate: async (
      id: string,
      data: ReauthenticateCredentialSessionInput,
    ) =>
      parseCredentialSession(
        await request<unknown>(
          `/credential-sessions/${encodeURIComponent(id)}/reauthenticate`,
          {
            method: "POST",
            body: JSON.stringify(data),
          },
        ),
      ),
    delete: (id: string) =>
      request<void>(`/credential-sessions/${encodeURIComponent(id)}`, {
        method: "DELETE",
      }),
    refresh: async (id: string) =>
      parseCredentialSession(
        await request<unknown>(
          `/credential-sessions/${encodeURIComponent(id)}/refresh`,
          { method: "POST" },
        ),
      ),
    refreshUsage: async (id: string) =>
      parseCredentialSession(
        await request<unknown>(
          `/credential-sessions/${encodeURIComponent(id)}/refresh-usage`,
          { method: "POST" },
        ),
      ),
    exportCodexAuth: (id: string) =>
      request<CodexAuthDocument>(
        `/credential-sessions/${encodeURIComponent(id)}/codex-auth`,
      ),
  };
}
