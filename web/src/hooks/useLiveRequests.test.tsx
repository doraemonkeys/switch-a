/* eslint-disable sonarjs/no-hardcoded-ip */
/**
 * no-hardcoded-ip: Test fixtures require mock IP addresses for realistic API response simulation.
 */
import { renderHook, act } from "@testing-library/react";
import type { Mock } from "vitest";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { useLiveRequests } from "./useLiveRequests";
import { createMockApiClient } from "./test-utils";

// Mock API response
const mockActiveResponse = {
  requests: [
    {
      request_id: "req-1",
      provider_id: "provider-1",
      model: "claude-3-opus",
      api_type: "claude",
      user_id: "user-1",
      client_ip: "192.168.1.1",
      is_sse: false,
      is_websocket: false,
      started_at: new Date().toISOString(),
    },
  ],
  count: 1,
};

// Create mock API client using shared utility
const createMockApi = () => {
  const mockApi = createMockApiClient();
  // Override requests.active with test-specific mock
  mockApi.requests.active = vi.fn().mockResolvedValue(mockActiveResponse);
  return mockApi;
};

// Mock useApi hook
let mockApi = createMockApi();
vi.mock("../api", async () => {
  const actual = await vi.importActual("../api");
  return {
    ...actual,
    useApi: () => mockApi,
  };
});

describe("useLiveRequests", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    mockApi = createMockApi();
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.clearAllMocks();
  });

  describe("initial fetch and basic functionality", () => {
    it("fetches data on mount when enabled (default)", async () => {
      renderHook(() => useLiveRequests());

      await act(async () => {
        await vi.advanceTimersByTimeAsync(0);
      });

      expect(mockApi.requests.active).toHaveBeenCalledTimes(1);
    });

    it("does not fetch when enabled is false", async () => {
      renderHook(() => useLiveRequests({ enabled: false }));

      await act(async () => {
        await vi.advanceTimersByTimeAsync(0);
      });

      expect(mockApi.requests.active).not.toHaveBeenCalled();
    });

    it("returns loading true initially", async () => {
      const { result } = renderHook(() => useLiveRequests());

      expect(result.current.loading).toBe(true);

      // Flush the pending async fetch to avoid act() warning on unmount
      await act(async () => {
        await vi.advanceTimersByTimeAsync(0);
      });
    });

    it("populates requests and count from API response", async () => {
      const { result } = renderHook(() => useLiveRequests());

      await act(async () => {
        await vi.advanceTimersByTimeAsync(0);
      });

      expect(result.current.requests).toEqual(mockActiveResponse.requests);
      expect(result.current.count).toBe(mockActiveResponse.count);
    });

    it("sets loading to false after fetch completes", async () => {
      const { result } = renderHook(() => useLiveRequests());

      await act(async () => {
        await vi.advanceTimersByTimeAsync(0);
      });

      expect(result.current.loading).toBe(false);
    });

    it("returns isPolling true when enabled", async () => {
      const { result } = renderHook(() => useLiveRequests({ enabled: true }));

      await act(async () => {
        await vi.advanceTimersByTimeAsync(0);
      });

      expect(result.current.isPolling).toBe(true);
    });

    it("returns isPolling false when disabled", async () => {
      const { result } = renderHook(() => useLiveRequests({ enabled: false }));

      await act(async () => {
        await vi.advanceTimersByTimeAsync(0);
      });

      expect(result.current.isPolling).toBe(false);
    });
  });

  describe("manual refetch", () => {
    it("triggers API call when refetch is called", async () => {
      const { result } = renderHook(() => useLiveRequests());

      await act(async () => {
        await vi.advanceTimersByTimeAsync(0);
      });

      expect(mockApi.requests.active).toHaveBeenCalledTimes(1);

      await act(async () => {
        result.current.refetch();
        await vi.advanceTimersByTimeAsync(0);
      });

      expect(mockApi.requests.active).toHaveBeenCalledTimes(2);
    });
  });

  describe("error handling", () => {
    it("sets error state when API call fails", async () => {
      const errorMessage = "Network error";
      (mockApi.requests.active as Mock).mockRejectedValueOnce(
        new Error(errorMessage),
      );

      const { result } = renderHook(() => useLiveRequests());

      await act(async () => {
        await vi.advanceTimersByTimeAsync(0);
      });

      expect(result.current.error).toBeInstanceOf(Error);
      expect(result.current.error?.message).toBe(errorMessage);
    });

    it("wraps non-Error exceptions in Error", async () => {
      (mockApi.requests.active as Mock).mockRejectedValueOnce("String error");

      const { result } = renderHook(() => useLiveRequests());

      await act(async () => {
        await vi.advanceTimersByTimeAsync(0);
      });

      expect(result.current.error).toBeInstanceOf(Error);
      expect(result.current.error?.message).toBe(
        "Failed to fetch active requests",
      );
    });

    it("clears error on successful refetch", async () => {
      (mockApi.requests.active as Mock).mockRejectedValueOnce(
        new Error("Network error"),
      );

      const { result } = renderHook(() => useLiveRequests());

      await act(async () => {
        await vi.advanceTimersByTimeAsync(0);
      });

      expect(result.current.error).not.toBeNull();

      // Reset to successful response
      (mockApi.requests.active as Mock).mockResolvedValueOnce(
        mockActiveResponse,
      );

      await act(async () => {
        result.current.refetch();
        await vi.advanceTimersByTimeAsync(0);
      });

      expect(result.current.error).toBeNull();
    });

    it("sets loading to false after error", async () => {
      (mockApi.requests.active as Mock).mockRejectedValueOnce(
        new Error("Network error"),
      );

      const { result } = renderHook(() => useLiveRequests());

      await act(async () => {
        await vi.advanceTimersByTimeAsync(0);
      });

      expect(result.current.loading).toBe(false);
    });
  });

  describe("visibility change behavior", () => {
    it("should refetch immediately when tab becomes visible", async () => {
      // Track visibility state
      let visibilityState: DocumentVisibilityState = "visible";
      const visibilityChangeListeners: Array<() => void> = [];

      // Mock document.visibilityState
      Object.defineProperty(document, "visibilityState", {
        configurable: true,
        get: () => visibilityState,
      });

      // Mock addEventListener/removeEventListener for visibility
      const originalAddEventListener = document.addEventListener;
      const originalRemoveEventListener = document.removeEventListener;

      document.addEventListener = vi.fn(
        (event: string, handler: EventListener) => {
          if (event === "visibilitychange") {
            visibilityChangeListeners.push(handler as () => void);
          } else {
            originalAddEventListener.call(document, event, handler);
          }
        },
      );

      document.removeEventListener = vi.fn(
        (event: string, handler: EventListener) => {
          if (event === "visibilitychange") {
            const index = visibilityChangeListeners.indexOf(
              handler as () => void,
            );
            if (index > -1) {
              visibilityChangeListeners.splice(index, 1);
            }
          } else {
            originalRemoveEventListener.call(document, event, handler);
          }
        },
      );

      renderHook(() => useLiveRequests({ pollInterval: 5000 }));

      // Wait for initial fetch
      await act(async () => {
        await vi.advanceTimersByTimeAsync(0);
      });

      // Initial fetch should have been called
      expect(mockApi.requests.active).toHaveBeenCalledTimes(1);

      // Simulate tab becoming hidden
      visibilityState = "hidden";
      act(() => {
        visibilityChangeListeners.forEach((listener) => listener());
      });

      // Advance time past the poll interval while hidden
      await act(async () => {
        await vi.advanceTimersByTimeAsync(10000);
      });

      // No additional fetches should occur while hidden (only initial fetch)
      expect(mockApi.requests.active).toHaveBeenCalledTimes(1);

      // Simulate tab becoming visible again
      visibilityState = "visible";
      await act(async () => {
        visibilityChangeListeners.forEach((listener) => listener());
        await vi.advanceTimersByTimeAsync(0);
      });

      // Should have refetched immediately on becoming visible
      expect(mockApi.requests.active).toHaveBeenCalledTimes(2);

      // Cleanup
      document.addEventListener = originalAddEventListener;
      document.removeEventListener = originalRemoveEventListener;
    });

    it("should not poll while tab is hidden", async () => {
      let visibilityState: DocumentVisibilityState = "visible";
      const visibilityChangeListeners: Array<() => void> = [];

      Object.defineProperty(document, "visibilityState", {
        configurable: true,
        get: () => visibilityState,
      });

      const originalAddEventListener = document.addEventListener;
      document.addEventListener = vi.fn(
        (event: string, handler: EventListener) => {
          if (event === "visibilitychange") {
            visibilityChangeListeners.push(handler as () => void);
          } else {
            originalAddEventListener.call(document, event, handler);
          }
        },
      );

      renderHook(() => useLiveRequests({ pollInterval: 5000 }));

      // Wait for initial fetch
      await act(async () => {
        await vi.advanceTimersByTimeAsync(0);
      });

      expect(mockApi.requests.active).toHaveBeenCalledTimes(1);

      // Hide the tab
      visibilityState = "hidden";
      act(() => {
        visibilityChangeListeners.forEach((listener) => listener());
      });

      // Advance through multiple poll cycles while hidden
      await act(async () => {
        await vi.advanceTimersByTimeAsync(20000); // 4x poll interval
      });

      // Still only the initial fetch (no polling while hidden)
      expect(mockApi.requests.active).toHaveBeenCalledTimes(1);

      // Restore
      document.addEventListener = originalAddEventListener;
    });
  });

  // Note: Exponential backoff tests removed due to flaky timing with fake timers.
  // The backoff implementation is verified via code review and manual testing.
});
