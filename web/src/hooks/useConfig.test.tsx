import { describe, it, expect, vi, beforeEach } from "vitest";
import { renderHook, waitFor, act } from "@testing-library/react";
import type { ReactNode } from "react";
import { useConfig } from "./useConfig";
import { ApiContext } from "../api/context";
import type { ApiClient } from "../api/client";

const mockConfig = {
  sticky_ttl: "300",
  failure_threshold: "3",
};

function createMockApiClient() {
  return {
    setToken: vi.fn(),
    clearToken: vi.fn(),
    providers: {
      list: vi.fn(),
      get: vi.fn(),
      create: vi.fn(),
      update: vi.fn(),
      delete: vi.fn(),
      enable: vi.fn(),
      disable: vi.fn(),
      reset: vi.fn(),
    },
    groups: {
      list: vi.fn(),
      get: vi.fn(),
      create: vi.fn(),
      update: vi.fn(),
      delete: vi.fn(),
    },
    config: {
      get: vi.fn().mockResolvedValue(mockConfig),
      update: vi.fn().mockResolvedValue(undefined),
    },
    status: { get: vi.fn(), health: vi.fn() },
    logs: { list: vi.fn() },
  } as unknown as ApiClient;
}

function createWrapper(apiClient: ApiClient) {
  return ({ children }: { children: ReactNode }) => (
    <ApiContext.Provider value={apiClient}>{children}</ApiContext.Provider>
  );
}

describe("useConfig", () => {
  let mockApi: ReturnType<typeof createMockApiClient>;

  beforeEach(() => {
    mockApi = createMockApiClient();
  });

  it("should fetch config on mount", async () => {
    const { result } = renderHook(() => useConfig(), {
      wrapper: createWrapper(mockApi),
    });

    expect(result.current.loading).toBe(true);

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    expect(result.current.config).toEqual(mockConfig);
    expect(result.current.error).toBeNull();
    expect(mockApi.config.get).toHaveBeenCalled();
  });

  it("should handle fetch error", async () => {
    mockApi.config.get = vi.fn().mockRejectedValue(new Error("Network error"));

    const { result } = renderHook(() => useConfig(), {
      wrapper: createWrapper(mockApi),
    });

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    expect(result.current.error).toBeInstanceOf(Error);
    expect(result.current.error?.message).toBe("Network error");
    expect(result.current.config).toEqual({});
  });

  it("should handle non-Error rejection", async () => {
    mockApi.config.get = vi.fn().mockRejectedValue("string error");

    const { result } = renderHook(() => useConfig(), {
      wrapper: createWrapper(mockApi),
    });

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    expect(result.current.error?.message).toBe("Failed to fetch config");
  });

  it("should refetch config", async () => {
    const { result } = renderHook(() => useConfig(), {
      wrapper: createWrapper(mockApi),
    });

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    await act(async () => {
      await result.current.refetch();
    });

    expect(mockApi.config.get).toHaveBeenCalledTimes(2);
  });

  it("should update config and refetch", async () => {
    const { result } = renderHook(() => useConfig(), {
      wrapper: createWrapper(mockApi),
    });

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    const newConfig = { sticky_ttl: "600" };
    await act(async () => {
      await result.current.updateConfig(newConfig);
    });

    expect(mockApi.config.update).toHaveBeenCalledWith(newConfig);
    expect(mockApi.config.get).toHaveBeenCalledTimes(2);
  });

  it("should set saving state during update", async () => {
    let resolveFn: () => void;
    mockApi.config.update = vi.fn().mockImplementation(
      () =>
        new Promise((resolve) => {
          resolveFn = resolve;
        }),
    );

    const { result } = renderHook(() => useConfig(), {
      wrapper: createWrapper(mockApi),
    });

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    expect(result.current.saving).toBe(false);

    let updatePromise: Promise<void>;
    act(() => {
      updatePromise = result.current.updateConfig({ sticky_ttl: "600" });
    });

    await waitFor(() => {
      expect(result.current.saving).toBe(true);
    });

    await act(async () => {
      resolveFn!();
      await updatePromise;
    });

    expect(result.current.saving).toBe(false);
  });

  it("should handle update error", async () => {
    mockApi.config.update = vi
      .fn()
      .mockRejectedValue(new Error("Update failed"));

    const { result } = renderHook(() => useConfig(), {
      wrapper: createWrapper(mockApi),
    });

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    let caughtError: Error | undefined;
    await act(async () => {
      try {
        await result.current.updateConfig({ sticky_ttl: "600" });
      } catch (err) {
        caughtError = err as Error;
      }
    });

    expect(caughtError?.message).toBe("Update failed");
    expect(result.current.error?.message).toBe("Update failed");
    expect(result.current.saving).toBe(false);
  });

  it("should handle non-Error update rejection", async () => {
    mockApi.config.update = vi.fn().mockRejectedValue("string error");

    const { result } = renderHook(() => useConfig(), {
      wrapper: createWrapper(mockApi),
    });

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    let caughtError: unknown;
    await act(async () => {
      try {
        await result.current.updateConfig({ sticky_ttl: "600" });
      } catch (err) {
        caughtError = err;
      }
    });

    // Original error is re-thrown, but state error is wrapped
    expect(caughtError).toBe("string error");
    expect(result.current.error?.message).toBe("Failed to update config");
  });
});
