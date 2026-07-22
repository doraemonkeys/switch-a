import { describe, it, expect, vi, beforeEach } from "vitest";
import { renderHook, waitFor, act } from "@testing-library/react";
import type { ReactNode } from "react";
import { useProviders, useProvider } from "./useProviders";
import { ApiContext } from "../api/context";
import type { ApiClient, Provider } from "../api/client";
import { PROVIDER_CREDENTIAL_TYPES } from "../config/constants";

const mockProvider: Provider = {
  id: "1",
  name: "OpenAI",
  api_key: "sk-xxx",
  api_types: [
    {
      provider_id: "1",
      api_type: "claude",
      base_url: "https://api.openai.com",
    },
  ],
  auth_mode: "bearer",
  credential_type: PROVIDER_CREDENTIAL_TYPES.API_KEY,
  group_id: null,
  weight: 1,
  priority: 1,
  concurrency: 10,
  max_retries: 0,
  vendor: "",
  failover_scope: "any",
  accept_failover: "any",
  enabled: true,
  created_at: "2024-01-01T00:00:00Z",
  updated_at: "2024-01-01T00:00:00Z",
};

function createMockApiClient() {
  return {
    setToken: vi.fn(),
    clearToken: vi.fn(),
    providers: {
      list: vi.fn().mockResolvedValue([mockProvider]),
      get: vi.fn().mockResolvedValue(mockProvider),
      create: vi.fn().mockResolvedValue(mockProvider),
      update: vi.fn().mockResolvedValue(mockProvider),
      delete: vi.fn().mockResolvedValue(undefined),
      enable: vi.fn().mockResolvedValue(undefined),
      disable: vi.fn().mockResolvedValue(undefined),
      reset: vi.fn().mockResolvedValue(undefined),
      refreshCredential: vi.fn().mockResolvedValue(undefined),
      refreshUsage: vi.fn().mockResolvedValue(undefined),
    },
    groups: {
      list: vi.fn(),
      get: vi.fn(),
      create: vi.fn(),
      update: vi.fn(),
      delete: vi.fn(),
    },
    config: { get: vi.fn(), update: vi.fn() },
    status: { get: vi.fn(), health: vi.fn() },
    logs: { list: vi.fn() },
  } as unknown as ApiClient;
}

function createWrapper(apiClient: ApiClient) {
  return ({ children }: { children: ReactNode }) => (
    <ApiContext.Provider value={apiClient}>{children}</ApiContext.Provider>
  );
}

describe("useProviders", () => {
  let mockApi: ReturnType<typeof createMockApiClient>;

  beforeEach(() => {
    mockApi = createMockApiClient();
  });

  it("should fetch providers on mount", async () => {
    const { result } = renderHook(() => useProviders(), {
      wrapper: createWrapper(mockApi),
    });

    expect(result.current.loading).toBe(true);

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    expect(result.current.providers).toEqual([mockProvider]);
    expect(result.current.error).toBeNull();
    expect(mockApi.providers.list).toHaveBeenCalled();
  });

  it("should handle fetch error", async () => {
    mockApi.providers.list = vi
      .fn()
      .mockRejectedValue(new Error("Network error"));

    const { result } = renderHook(() => useProviders(), {
      wrapper: createWrapper(mockApi),
    });

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    expect(result.current.error).toBeInstanceOf(Error);
    expect(result.current.error?.message).toBe("Network error");
    expect(result.current.providers).toEqual([]);
  });

  it("should handle non-Error rejection", async () => {
    mockApi.providers.list = vi.fn().mockRejectedValue("string error");

    const { result } = renderHook(() => useProviders(), {
      wrapper: createWrapper(mockApi),
    });

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    expect(result.current.error?.message).toBe("Failed to fetch providers");
  });

  it("should refetch providers", async () => {
    const { result } = renderHook(() => useProviders(), {
      wrapper: createWrapper(mockApi),
    });

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    await act(async () => {
      await result.current.refetch();
    });

    expect(mockApi.providers.list).toHaveBeenCalledTimes(2);
  });

  it("should create provider and refetch", async () => {
    const { result } = renderHook(() => useProviders(), {
      wrapper: createWrapper(mockApi),
    });

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    const input = {
      name: "New",
      api_key: "key",
      api_types: [{ api_type: "claude", base_url: "https://new.example.com" }],
    };
    await act(async () => {
      await result.current.createProvider(input);
    });

    expect(mockApi.providers.create).toHaveBeenCalledWith(input);
    expect(mockApi.providers.list).toHaveBeenCalledTimes(2);
  });

  it("should update provider and refetch", async () => {
    const { result } = renderHook(() => useProviders(), {
      wrapper: createWrapper(mockApi),
    });

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    const input = {
      name: "Updated",
      api_key: "key",
      api_types: [
        { api_type: "claude", base_url: "https://updated.example.com" },
      ],
    };
    await act(async () => {
      await result.current.updateProvider("1", input);
    });

    expect(mockApi.providers.update).toHaveBeenCalledWith("1", input);
    expect(mockApi.providers.list).toHaveBeenCalledTimes(2);
  });

  it("should delete provider and refetch", async () => {
    const { result } = renderHook(() => useProviders(), {
      wrapper: createWrapper(mockApi),
    });

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    await act(async () => {
      await result.current.deleteProvider("1");
    });

    expect(mockApi.providers.delete).toHaveBeenCalledWith("1");
    expect(mockApi.providers.list).toHaveBeenCalledTimes(2);
  });

  it("should enable provider and refetch", async () => {
    const { result } = renderHook(() => useProviders(), {
      wrapper: createWrapper(mockApi),
    });

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    await act(async () => {
      await result.current.enableProvider("1");
    });

    expect(mockApi.providers.enable).toHaveBeenCalledWith("1");
    expect(mockApi.providers.list).toHaveBeenCalledTimes(2);
  });

  it("should disable provider and refetch", async () => {
    const { result } = renderHook(() => useProviders(), {
      wrapper: createWrapper(mockApi),
    });

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    await act(async () => {
      await result.current.disableProvider("1");
    });

    expect(mockApi.providers.disable).toHaveBeenCalledWith("1");
    expect(mockApi.providers.list).toHaveBeenCalledTimes(2);
  });

  it("should reset provider and refetch", async () => {
    const { result } = renderHook(() => useProviders(), {
      wrapper: createWrapper(mockApi),
    });

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    await act(async () => {
      await result.current.resetProvider("1");
    });

    expect(mockApi.providers.reset).toHaveBeenCalledWith("1");
    expect(mockApi.providers.list).toHaveBeenCalledTimes(2);
  });

  it("should refresh provider credential and refetch", async () => {
    const { result } = renderHook(() => useProviders(), {
      wrapper: createWrapper(mockApi),
    });

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    await act(async () => {
      await result.current.refreshCredential("1");
    });

    expect(mockApi.providers.refreshCredential).toHaveBeenCalledWith("1");
    expect(mockApi.providers.list).toHaveBeenCalledTimes(2);
  });

  it("should refresh provider usage and refetch", async () => {
    const { result } = renderHook(() => useProviders(), {
      wrapper: createWrapper(mockApi),
    });

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    await act(async () => {
      await result.current.refreshUsage("1");
    });

    expect(mockApi.providers.refreshUsage).toHaveBeenCalledWith("1");
    expect(mockApi.providers.list).toHaveBeenCalledTimes(2);
  });

  it("should reconcile provider auth state after a rejected usage refresh", async () => {
    const rejection = new Error("provider requires reauthentication");
    const reauthProvider: Provider = {
      ...mockProvider,
      credential_type: PROVIDER_CREDENTIAL_TYPES.CHATGPT,
      auth: {
        type: PROVIDER_CREDENTIAL_TYPES.CHATGPT,
        status: "reauth_required",
        reason: "token_invalidated",
      },
    };
    vi.mocked(mockApi.providers.refreshUsage).mockRejectedValue(rejection);
    vi.mocked(mockApi.providers.list)
      .mockResolvedValueOnce([mockProvider])
      .mockResolvedValueOnce([reauthProvider]);
    const { result } = renderHook(() => useProviders(), {
      wrapper: createWrapper(mockApi),
    });

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    let caught: unknown;
    await act(async () => {
      try {
        await result.current.refreshUsage("1");
      } catch (error) {
        caught = error;
      }
    });

    expect(caught).toBe(rejection);
    expect(mockApi.providers.list).toHaveBeenCalledTimes(2);
    expect(result.current.providers).toEqual([reauthProvider]);
  });
});

describe("useProvider", () => {
  let mockApi: ReturnType<typeof createMockApiClient>;

  beforeEach(() => {
    mockApi = createMockApiClient();
  });

  it("should fetch single provider on mount", async () => {
    const { result } = renderHook(() => useProvider("1"), {
      wrapper: createWrapper(mockApi),
    });

    expect(result.current.loading).toBe(true);

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    expect(result.current.provider).toEqual(mockProvider);
    expect(result.current.error).toBeNull();
    expect(mockApi.providers.get).toHaveBeenCalledWith("1");
  });

  it("should handle fetch error", async () => {
    mockApi.providers.get = vi.fn().mockRejectedValue(new Error("Not found"));

    const { result } = renderHook(() => useProvider("1"), {
      wrapper: createWrapper(mockApi),
    });

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    expect(result.current.error).toBeInstanceOf(Error);
    expect(result.current.error?.message).toBe("Not found");
  });

  it("should handle non-Error rejection", async () => {
    mockApi.providers.get = vi.fn().mockRejectedValue("string error");

    const { result } = renderHook(() => useProvider("1"), {
      wrapper: createWrapper(mockApi),
    });

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    expect(result.current.error?.message).toBe("Failed to fetch provider");
  });

  it("should not fetch when id is empty", async () => {
    const { result } = renderHook(() => useProvider(""), {
      wrapper: createWrapper(mockApi),
    });

    // Give time for any potential fetch to complete
    await new Promise((resolve) => setTimeout(resolve, 50));

    expect(mockApi.providers.get).not.toHaveBeenCalled();
    expect(result.current.provider).toBeNull();
  });

  it("should refetch provider", async () => {
    const { result } = renderHook(() => useProvider("1"), {
      wrapper: createWrapper(mockApi),
    });

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    await act(async () => {
      await result.current.refetch();
    });

    expect(mockApi.providers.get).toHaveBeenCalledTimes(2);
  });
});
