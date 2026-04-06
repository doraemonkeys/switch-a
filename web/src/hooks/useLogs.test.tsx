import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type {
  ApiClient,
  LogsResponse,
  NormalizedRequestLog,
} from "../api/client";
import { createMockApiClient, createWrapper } from "./test-utils";
import { useLogs } from "./useLogs";

const mockLogs: NormalizedRequestLog[] = [
  {
    id: 1,
    request_id: "request-1",
    provider_id: "provider-1",
    api_type: "claude",
    model: "claude-3-7-sonnet",
    client_ip: "127.0.0.1",
    user_id: "user-1",
    semantics_version: "normalized_v1",
    client_transport_status_code: 101,
    completion_state: "completed",
    service_outcome: "completed",
    termination_actor: null,
    termination_reason: null,
    client_action: "none",
    session_evidence_json: null,
    latency_ms: 150,
    is_sse: false,
    is_websocket: true,
    retry_count: 0,
    is_sticky: false,
    created_at: "2026-04-01T00:00:00Z",
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

  it("fetches logs on mount with default params", async () => {
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

  it("supports normalized filter updates", async () => {
    const { result } = renderHook(() => useLogs(), {
      wrapper: createWrapper(mockApi),
    });

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    act(() => {
      result.current.updateFilter({
        semantics_version: "normalized_v1",
        service_outcome: "interrupted",
      });
    });

    await waitFor(() => {
      expect(mockApi.logs.list).toHaveBeenCalledWith({
        limit: 20,
        offset: 0,
        semantics_version: "normalized_v1",
        service_outcome: "interrupted",
      });
    });
  });

  it("exposes sort metadata from the response", async () => {
    const { result } = renderHook(() => useLogs(), {
      wrapper: createWrapper(mockApi),
    });

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    expect(result.current.sortBy).toBe("created_at");
    expect(result.current.sortOrder).toBe("desc");
  });

  it("surfaces request failures as hook errors", async () => {
    mockApi.logs.list = vi.fn().mockRejectedValue(new Error("Network error"));

    const { result } = renderHook(() => useLogs(), {
      wrapper: createWrapper(mockApi),
    });

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    expect(result.current.error?.message).toBe("Network error");
    expect(result.current.logs).toEqual([]);
  });
});
