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
  api_key: "",
  api_types: [],
  credential_type: "chatgpt",
  credential_login_id: "login-id",
};

const pausedGPTProvider = {
  id: "gpt-paused",
  name: "Paused GPT",
  enabled: false,
  credential_type: "chatgpt",
} as Provider;

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

  it("leaves credential binding conflicts to the replacement prompt", async () => {
    const conflict = new ApiError(
      "CONFLICT",
      "account already connected",
      409,
      {
        kind: "credential_binding",
        account_id: "acct-shared",
        provider_id: "old-provider",
      },
    );
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

    expect(toast.error).not.toHaveBeenCalled();
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

    expect(api.providers.exportCodexAuth).toHaveBeenCalledWith("gpt-paused");
    expect(downloadJsonFile).toHaveBeenCalledWith("auth.json", authDocument);
    expect(toast.success).toHaveBeenCalledWith(
      'Codex auth.json exported for "Paused GPT". Keep this provider paused while the file is in use.',
    );
    expect(api.providers.list).toHaveBeenCalledTimes(1);
    expect(result.current.exportingProviderId).toBeNull();
  });

  it("reports export failures without creating a credential file", async () => {
    const error = new Error(
      "Pause the provider before exporting Codex auth.json",
    );
    const api = {
      providers: {
        list: vi.fn().mockResolvedValue([pausedGPTProvider]),
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
    expect(result.current.exportingProviderId).toBeNull();
  });
});
