import { act, renderHook } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { Provider } from "../../api";
import type { ToastContextValue } from "../../hooks/useToast";
import { useProviderConfirmations } from "./useProviderConfirmations";

const provider = {
  id: "provider-1",
  name: "Provider One",
} as Provider;

function createDependencies() {
  return {
    deleteProvider: vi.fn().mockResolvedValue(undefined),
    resetProvider: vi.fn().mockResolvedValue(undefined),
    toast: {
      success: vi.fn(),
      error: vi.fn(),
    } as unknown as Pick<ToastContextValue, "success" | "error">,
  };
}

describe("useProviderConfirmations", () => {
  it("completes delete confirmation and closes it", async () => {
    const dependencies = createDependencies();
    const { result } = renderHook(() => useProviderConfirmations(dependencies));

    act(() => result.current.handleDeleteClick(provider));
    expect(result.current.deleteConfirm).toEqual({
      isOpen: true,
      provider,
    });
    await act(() => result.current.handleDeleteConfirm());

    expect(dependencies.deleteProvider).toHaveBeenCalledWith("provider-1");
    expect(dependencies.toast.success).toHaveBeenCalledWith(
      'Provider "Provider One" deleted',
    );
    expect(result.current.deleteConfirm.isOpen).toBe(false);
    expect(result.current.deleting).toBe(false);
  });

  it("keeps a failed reset reviewable and reports the failure", async () => {
    const dependencies = createDependencies();
    dependencies.resetProvider.mockRejectedValue(
      new Error("reset unavailable"),
    );
    const { result } = renderHook(() => useProviderConfirmations(dependencies));

    act(() => result.current.handleResetClick(provider));
    await act(() => result.current.handleResetConfirm());

    expect(dependencies.toast.error).toHaveBeenCalledWith("reset unavailable");
    expect(result.current.resetConfirm.isOpen).toBe(true);
    expect(result.current.resetting).toBe(false);
  });
});
