import { describe, it, expect, vi, beforeEach } from "vitest";
import { renderHook, waitFor, act } from "@testing-library/react";
import { useLogs } from "./useLogs";
import type { ApiClient, RequestLog, LogsResponse } from "../api/client";
import { createMockApiClient, createWrapper } from "./test-utils";

const mockLogs: RequestLog[] = [
  {
    id: 1,
    provider_id: "1",
    api_type: "claude",
    model: "claude-3",
    client_ip: "127.0.0.1",
    user_id: "user1",
    status_code: 200,
    latency_ms: 150,
    success: true,
    is_sse: false,
    error_msg: null,
    created_at: "2024-01-01T00:00:00Z",
  },
];

const mockLogsResponse: LogsResponse = {
  logs: mockLogs,
  total: 100,
  limit: 20,
  offset: 0,
  sort_by: "created_at",
  sort_order: "desc",
};

function setupMockApiClient(): ApiClient {
  const mockApi = createMockApiClient();
  mockApi.logs.list = vi.fn().mockResolvedValue(mockLogsResponse);
  return mockApi;
}

describe("useLogs", () => {
  let mockApi: ApiClient;

  beforeEach(() => {
    mockApi = setupMockApiClient();
  });

  it("should fetch logs on mount with default params", async () => {
    const { result } = renderHook(() => useLogs(), {
      wrapper: createWrapper(mockApi),
    });

    expect(result.current.loading).toBe(true);

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    expect(result.current.logs).toEqual(mockLogs);
    expect(result.current.total).toBe(100);
    expect(result.current.error).toBeNull();
    expect(mockApi.logs.list).toHaveBeenCalledWith({ limit: 20, offset: 0 });
  });

  it("should fetch logs with custom initial filter", async () => {
    const { result } = renderHook(() => useLogs({ limit: 50, offset: 100 }), {
      wrapper: createWrapper(mockApi),
    });

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    expect(mockApi.logs.list).toHaveBeenCalledWith({ limit: 50, offset: 100 });
    expect(result.current.filter).toEqual({ limit: 50, offset: 100 });
  });

  it("should handle fetch error", async () => {
    mockApi.logs.list = vi.fn().mockRejectedValue(new Error("Network error"));

    const { result } = renderHook(() => useLogs(), {
      wrapper: createWrapper(mockApi),
    });

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    expect(result.current.error).toBeInstanceOf(Error);
    expect(result.current.error?.message).toBe("Network error");
    expect(result.current.logs).toEqual([]);
  });

  it("should handle non-Error rejection", async () => {
    mockApi.logs.list = vi.fn().mockRejectedValue("string error");

    const { result } = renderHook(() => useLogs(), {
      wrapper: createWrapper(mockApi),
    });

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    expect(result.current.error?.message).toBe("Failed to fetch logs");
  });

  it("should refetch logs", async () => {
    const { result } = renderHook(() => useLogs(), {
      wrapper: createWrapper(mockApi),
    });

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    await act(async () => {
      await result.current.refetch();
    });

    expect(mockApi.logs.list).toHaveBeenCalledTimes(2);
  });

  it("should update filter and refetch", async () => {
    const { result } = renderHook(() => useLogs(), {
      wrapper: createWrapper(mockApi),
    });

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    act(() => {
      result.current.setFilter({ limit: 50, offset: 20 });
    });

    await waitFor(() => {
      expect(mockApi.logs.list).toHaveBeenCalledWith({ limit: 50, offset: 20 });
    });

    expect(result.current.filter).toEqual({ limit: 50, offset: 20 });
  });

  it("should partially update filter with updateFilter", async () => {
    const { result } = renderHook(() => useLogs(), {
      wrapper: createWrapper(mockApi),
    });

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    act(() => {
      result.current.updateFilter({ provider_id: "provider-1" });
    });

    await waitFor(() => {
      expect(mockApi.logs.list).toHaveBeenCalledWith({
        limit: 20,
        offset: 0,
        provider_id: "provider-1",
      });
    });

    expect(result.current.filter).toEqual({
      limit: 20,
      offset: 0,
      provider_id: "provider-1",
    });
  });

  it("should expose sortBy and sortOrder from response", async () => {
    const { result } = renderHook(() => useLogs(), {
      wrapper: createWrapper(mockApi),
    });

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    expect(result.current.sortBy).toBe("created_at");
    expect(result.current.sortOrder).toBe("desc");
  });
});
