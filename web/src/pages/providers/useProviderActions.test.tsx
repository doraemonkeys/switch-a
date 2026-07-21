import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { ApiContext } from "../../api/context";
import { ApiError, type ApiClient, type ProviderInput } from "../../api";
import { ToastContext, type ToastContextValue } from "../../hooks/useToast";
import { useProviderActions } from "./useProviderActions";

const providerInput: ProviderInput = {
  id: "new-provider",
  name: "New Provider",
  api_key: "",
  api_types: [],
  credential_type: "chatgpt",
  credential_login_id: "login-id",
};

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
    const toast = {
      toasts: [],
      addToast: vi.fn(),
      removeToast: vi.fn(),
      success: vi.fn(),
      error: vi.fn(),
      warning: vi.fn(),
      info: vi.fn(),
    } as unknown as ToastContextValue;
    const { result } = renderHook(() => useProviderActions(), {
      wrapper: createWrapper(api, toast),
    });
    await waitFor(() => expect(api.providers.list).toHaveBeenCalled());

    await expect(
      act(() => result.current.handleSaveProvider(providerInput, null)),
    ).rejects.toBe(conflict);

    expect(toast.error).not.toHaveBeenCalled();
  });
});
