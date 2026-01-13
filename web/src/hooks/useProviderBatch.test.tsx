import { describe, it, expect, vi, beforeEach } from "vitest";
import { renderHook, waitFor, act } from "@testing-library/react";
import { useProviderBatch } from "./useProviderBatch";
import type { ApiClient, BatchProviderResponse } from "../api/client";
import { createMockApiClient, createWrapper } from "./test-utils";

const mockBatchResponse: BatchProviderResponse = {
  success: true,
  affected: 2,
  results: [
    { id: "1", success: true },
    { id: "2", success: true },
  ],
};

function setupMockApiClient(): ApiClient {
  const mockApi = createMockApiClient();
  mockApi.providers.batch = vi.fn().mockResolvedValue(mockBatchResponse);
  return mockApi;
}

describe("useProviderBatch", () => {
  let mockApi: ApiClient;

  beforeEach(() => {
    mockApi = setupMockApiClient();
  });

  it("should start with initial state", () => {
    const { result } = renderHook(() => useProviderBatch(), {
      wrapper: createWrapper(mockApi),
    });

    expect(result.current.loading).toBe(false);
    expect(result.current.error).toBeNull();
    expect(result.current.result).toBeNull();
  });

  it("should execute batch enable action", async () => {
    const { result } = renderHook(() => useProviderBatch(), {
      wrapper: createWrapper(mockApi),
    });

    let response: BatchProviderResponse | undefined;
    await act(async () => {
      response = await result.current.batchAction("enable", ["1", "2"]);
    });

    expect(mockApi.providers.batch).toHaveBeenCalledWith({
      action: "enable",
      ids: ["1", "2"],
    });
    expect(response).toEqual(mockBatchResponse);
    expect(result.current.result).toEqual(mockBatchResponse);
    expect(result.current.loading).toBe(false);
    expect(result.current.error).toBeNull();
  });

  it("should execute batch disable action", async () => {
    const { result } = renderHook(() => useProviderBatch(), {
      wrapper: createWrapper(mockApi),
    });

    await act(async () => {
      await result.current.batchAction("disable", ["1"]);
    });

    expect(mockApi.providers.batch).toHaveBeenCalledWith({
      action: "disable",
      ids: ["1"],
    });
  });

  it("should execute batch reset action", async () => {
    const { result } = renderHook(() => useProviderBatch(), {
      wrapper: createWrapper(mockApi),
    });

    await act(async () => {
      await result.current.batchAction("reset", ["1", "2", "3"]);
    });

    expect(mockApi.providers.batch).toHaveBeenCalledWith({
      action: "reset",
      ids: ["1", "2", "3"],
    });
  });

  it("should execute batch delete action", async () => {
    const { result } = renderHook(() => useProviderBatch(), {
      wrapper: createWrapper(mockApi),
    });

    await act(async () => {
      await result.current.batchAction("delete", ["1"]);
    });

    expect(mockApi.providers.batch).toHaveBeenCalledWith({
      action: "delete",
      ids: ["1"],
    });
  });

  it("should handle error", async () => {
    mockApi.providers.batch = vi
      .fn()
      .mockRejectedValue(new Error("Batch failed"));

    const { result } = renderHook(() => useProviderBatch(), {
      wrapper: createWrapper(mockApi),
    });

    await act(async () => {
      try {
        await result.current.batchAction("enable", ["1"]);
      } catch {
        // Expected to throw
      }
    });

    await waitFor(() => {
      expect(result.current.error).toBeInstanceOf(Error);
    });
    expect(result.current.error?.message).toBe("Batch failed");
    expect(result.current.result).toBeNull();
  });

  it("should handle non-Error rejection", async () => {
    mockApi.providers.batch = vi.fn().mockRejectedValue("string error");

    const { result } = renderHook(() => useProviderBatch(), {
      wrapper: createWrapper(mockApi),
    });

    await act(async () => {
      try {
        await result.current.batchAction("enable", ["1"]);
      } catch {
        // Expected to throw
      }
    });

    await waitFor(() => {
      expect(result.current.error?.message).toBe(
        "Failed to execute batch operation",
      );
    });
  });

  it("should set loading state during operation", async () => {
    let resolvePromise: (value: BatchProviderResponse) => void;
    mockApi.providers.batch = vi.fn().mockImplementation(
      () =>
        new Promise<BatchProviderResponse>((resolve) => {
          resolvePromise = resolve;
        }),
    );

    const { result } = renderHook(() => useProviderBatch(), {
      wrapper: createWrapper(mockApi),
    });

    act(() => {
      result.current.batchAction("enable", ["1"]);
    });

    await waitFor(() => {
      expect(result.current.loading).toBe(true);
    });

    await act(async () => {
      resolvePromise!(mockBatchResponse);
    });

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });
  });

  it("should reset state", async () => {
    const { result } = renderHook(() => useProviderBatch(), {
      wrapper: createWrapper(mockApi),
    });

    await act(async () => {
      await result.current.batchAction("enable", ["1"]);
    });

    expect(result.current.result).not.toBeNull();

    act(() => {
      result.current.reset();
    });

    expect(result.current.result).toBeNull();
    expect(result.current.error).toBeNull();
  });
});
