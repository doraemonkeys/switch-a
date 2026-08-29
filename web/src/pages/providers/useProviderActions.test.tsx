import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ApiContext } from "../../api/context";
import { ApiError, type ApiClient, type ProviderInput } from "../../api";
import type { Provider } from "../../api";
import { ToastContext, type ToastContextValue } from "../../hooks/useToast";
import { downloadJsonFile } from "../../lib/jsonDownload";
import { useProviderActions } from "./useProviderActions";

vi.mock("../../lib/jsonDownload", () => ({
  downloadJsonFile: vi.fn(),
}));

const providerInput: ProviderInput = {
  id: "new-provider",
  name: "New Provider",
  api_types: [
    {
      api_type: "codex",
      base_url: "https://chatgpt.com/backend-api/codex",
      credential_session_id: "credential-gpt",
    },
  ],
};

const pausedGPTProvider: Provider = {
  id: "gpt-paused",
  name: "Paused GPT",
  api_types: [
    {
      api_type: "codex",
      base_url: "https://chatgpt.com/backend-api/codex",
      credential_session_id: "credential-gpt",
    },
  ],
  auth_mode: "bearer",
  credential_sessions: [
    {
      id: "credential-gpt",
      kind: "chatgpt",
      version: 1,
      subject: { kind: "account", value: "account-123" },
      auth_state: { status: "active", account_id: "account-123" },
    },
  ],
  group_id: null,
  weight: 1,
  priority: 0,
  concurrency: 0,
  max_retries: 0,
  vendor: "",
  failover_scope: "any",
  accept_failover: "any",
  enabled: false,
  created_at: "2026-08-28T00:00:00Z",
  updated_at: "2026-08-28T00:00:00Z",
};

function createToast(): ToastContextValue {
  return {
    toasts: [],
    addToast: vi.fn(),
    removeToast: vi.fn(),
    success: vi.fn(),
    error: vi.fn(),
    warning: vi.fn(),
    info: vi.fn(),
  } as unknown as ToastContextValue;
}

function createWrapper(api: ApiClient, toast: ToastContextValue) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return (
      <ApiContext.Provider value={api}>
        <ToastContext.Provider value={toast}>{children}</ToastContext.Provider>
      </ApiContext.Provider>
    );
  };
}

describe("useProviderActions", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("reports provider save failures and preserves the rejection", async () => {
    const conflict = new Error("account already connected");
    const api = {
      providers: {
        list: vi.fn().mockResolvedValue([]),
        create: vi.fn().mockRejectedValue(conflict),
      },
    } as unknown as ApiClient;
    const toast = createToast();
    const { result } = renderHook(() => useProviderActions(), {
      wrapper: createWrapper(api, toast),
    });
    await waitFor(() => expect(api.providers.list).toHaveBeenCalled());

    await expect(
      act(() => result.current.handleSaveProvider(providerInput, null)),
    ).rejects.toBe(conflict);

    expect(toast.error).toHaveBeenCalledWith(conflict.message);
  });

  it("downloads an auth.json snapshot without changing the provider list", async () => {
    const authDocument = {
      auth_mode: "chatgpt",
      OPENAI_API_KEY: null,
      tokens: {
        id_token: "id-token",
        access_token: "access-token",
        refresh_token: "refresh-token",
        account_id: "account-123",
      },
    } as const;
    const api = {
      providers: {
        list: vi.fn().mockResolvedValue([pausedGPTProvider]),
      },
      credentialSessions: {
        exportCodexAuth: vi.fn().mockResolvedValue(authDocument),
      },
    } as unknown as ApiClient;
    const toast = createToast();
    const { result } = renderHook(() => useProviderActions(), {
      wrapper: createWrapper(api, toast),
    });
    await waitFor(() => expect(api.providers.list).toHaveBeenCalled());

    await act(async () => {
      await result.current.handleExportCodexAuth(pausedGPTProvider);
    });

    expect(api.credentialSessions.exportCodexAuth).toHaveBeenCalledWith(
      "credential-gpt",
    );
    expect(downloadJsonFile).toHaveBeenCalledWith("auth.json", authDocument);
    expect(toast.success).toHaveBeenCalledWith(
      'Codex auth.json exported for credential session "credential-gpt". Keep every referencing provider paused while the file is in use.',
    );
    expect(api.providers.list).toHaveBeenCalledTimes(1);
    expect(result.current.exportingCredentialSessionId).toBeNull();
  });

  it("reports export failures without creating a credential file", async () => {
    const error = new Error(
      "Pause the provider before exporting Codex auth.json",
    );
    const api = {
      providers: {
        list: vi.fn().mockResolvedValue([pausedGPTProvider]),
      },
      credentialSessions: {
        exportCodexAuth: vi.fn().mockRejectedValue(error),
      },
    } as unknown as ApiClient;
    const toast = createToast();
    const { result } = renderHook(() => useProviderActions(), {
      wrapper: createWrapper(api, toast),
    });
    await waitFor(() => expect(api.providers.list).toHaveBeenCalled());

    await act(async () => {
      await result.current.handleExportCodexAuth(pausedGPTProvider);
    });

    expect(downloadJsonFile).not.toHaveBeenCalled();
    expect(toast.error).toHaveBeenCalledWith(error.message);
    expect(result.current.exportingCredentialSessionId).toBeNull();
  });

  it("rejects export locally when another route sharing the session is live", async () => {
    const liveProvider = {
      ...pausedGPTProvider,
      id: "gpt-live",
      name: "Live GPT",
      enabled: true,
    };
    const api = {
      providers: {
        list: vi.fn().mockResolvedValue([pausedGPTProvider, liveProvider]),
      },
      credentialSessions: {
        exportCodexAuth: vi.fn(),
      },
    } as unknown as ApiClient;
    const toast = createToast();
    const { result } = renderHook(() => useProviderActions(), {
      wrapper: createWrapper(api, toast),
    });
    await waitFor(() => expect(result.current.providers).toHaveLength(2));

    await act(async () => {
      await result.current.handleExportCodexAuth(pausedGPTProvider);
    });

    expect(api.credentialSessions.exportCodexAuth).not.toHaveBeenCalled();
    expect(toast.error).toHaveBeenCalledWith(
      'Pause every provider using credential session "credential-gpt" before exporting: "Live GPT" (gpt-live).',
    );
  });

  it("uses structured backend blockers when route state changes during export", async () => {
    const stalePausedProvider = {
      ...pausedGPTProvider,
      id: "gpt-race",
      name: "Race GPT",
    };
    const conflict = new ApiError(
      "CONFLICT",
      "Disable every referencing route target before exporting Codex auth.json",
      409,
      {
        credential_session_id: "credential-gpt",
        blocking_route_target_ids: ["gpt-race"],
      },
    );
    const api = {
      providers: {
        list: vi
          .fn()
          .mockResolvedValue([pausedGPTProvider, stalePausedProvider]),
      },
      credentialSessions: {
        exportCodexAuth: vi.fn().mockRejectedValue(conflict),
      },
    } as unknown as ApiClient;
    const toast = createToast();
    const { result } = renderHook(() => useProviderActions(), {
      wrapper: createWrapper(api, toast),
    });
    await waitFor(() => expect(result.current.providers).toHaveLength(2));

    await act(async () => {
      await result.current.handleExportCodexAuth(pausedGPTProvider);
    });

    expect(downloadJsonFile).not.toHaveBeenCalled();
    expect(toast.error).toHaveBeenCalledWith(
      'Pause every provider using credential session "credential-gpt" before exporting: "Race GPT" (gpt-race).',
    );
  });
});
