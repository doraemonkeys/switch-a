import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { usePollingQuery } from "./usePollingQuery";

describe("usePollingQuery", () => {
  const originalVisibilityState = Object.getOwnPropertyDescriptor(
    document,
    "visibilityState",
  );

  beforeEach(() => {
    vi.useFakeTimers();
    Object.defineProperty(document, "visibilityState", {
      configurable: true,
      value: "visible",
    });
  });

  afterEach(() => {
    vi.useRealTimers();
    if (originalVisibilityState) {
      Object.defineProperty(
        document,
        "visibilityState",
        originalVisibilityState,
      );
    }
  });

  it("performs one initial request and then polls", async () => {
    const fetcher = vi.fn().mockResolvedValue("snapshot");
    const { result } = renderHook(() =>
      usePollingQuery(fetcher, { intervalMs: 1_000 }),
    );

    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });
    expect(result.current.data).toBe("snapshot");
    expect(fetcher).toHaveBeenCalledTimes(1);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(1_000);
    });
    expect(fetcher).toHaveBeenCalledTimes(2);
  });

  it("does not overlap polls when a request outlives the interval", async () => {
    let resolveRequest: (value: string) => void = (value: string) => {
      throw new Error(`request resolver was not initialized for ${value}`);
    };
    const fetcher = vi.fn(
      () =>
        new Promise<string>((resolve) => {
          resolveRequest = resolve;
        }),
    );
    const { result } = renderHook(() =>
      usePollingQuery(fetcher, { intervalMs: 1_000 }),
    );

    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });
    expect(fetcher).toHaveBeenCalledTimes(1);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(3_000);
    });
    expect(fetcher).toHaveBeenCalledTimes(1);

    await act(async () => {
      resolveRequest("first snapshot");
    });
    expect(result.current.data).toBe("first snapshot");

    await act(async () => {
      await vi.advanceTimersByTimeAsync(1_000);
    });
    expect(fetcher).toHaveBeenCalledTimes(2);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(3_000);
    });
    expect(fetcher).toHaveBeenCalledTimes(2);

    await act(async () => {
      resolveRequest("second snapshot");
    });
    expect(result.current.data).toBe("second snapshot");
  });

  it("does not request or poll while disabled", async () => {
    const fetcher = vi.fn().mockResolvedValue("snapshot");
    renderHook(() =>
      usePollingQuery(fetcher, {
        intervalMs: 1_000,
        enabled: false,
      }),
    );

    await act(async () => {
      await vi.advanceTimersByTimeAsync(5_000);
    });
    expect(fetcher).not.toHaveBeenCalled();
  });

  it("pauses while hidden and refreshes immediately when visible", async () => {
    let visibilityState: DocumentVisibilityState = "visible";
    Object.defineProperty(document, "visibilityState", {
      configurable: true,
      get: () => visibilityState,
    });
    const fetcher = vi.fn().mockResolvedValue("snapshot");
    renderHook(() => usePollingQuery(fetcher, { intervalMs: 1_000 }));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });

    visibilityState = "hidden";
    document.dispatchEvent(new Event("visibilitychange"));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(3_000);
    });
    expect(fetcher).toHaveBeenCalledTimes(1);

    visibilityState = "visible";
    await act(async () => {
      document.dispatchEvent(new Event("visibilitychange"));
      await vi.advanceTimersByTimeAsync(0);
    });
    expect(fetcher).toHaveBeenCalledTimes(2);
  });

  it("backs off after a failed synchronization", async () => {
    const fetcher = vi
      .fn()
      .mockRejectedValueOnce(new Error("offline"))
      .mockResolvedValue("recovered");
    renderHook(() =>
      usePollingQuery(fetcher, {
        intervalMs: 1_000,
        maxIntervalMs: 8_000,
      }),
    );

    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });
    expect(fetcher).toHaveBeenCalledTimes(1);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(1_999);
    });
    expect(fetcher).toHaveBeenCalledTimes(1);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(1);
    });
    expect(fetcher).toHaveBeenCalledTimes(2);
  });

  it("cleans up its timer when unmounted", async () => {
    const fetcher = vi.fn().mockResolvedValue("snapshot");
    const { unmount } = renderHook(() =>
      usePollingQuery(fetcher, { intervalMs: 1_000 }),
    );
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });

    unmount();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(2_000);
    });
    expect(fetcher).toHaveBeenCalledTimes(1);
  });
});
