import { describe, it, expect, vi, beforeEach } from "vitest";
import { renderHook, waitFor, act } from "@testing-library/react";
import { useQuery, useMutation } from "./useQuery";

describe("useQuery", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("should fetch data on mount", async () => {
    const mockData = { id: 1, name: "Test" };
    const fetcher = vi.fn().mockResolvedValue(mockData);

    const { result } = renderHook(() => useQuery(fetcher));

    expect(result.current.loading).toBe(true);
    expect(result.current.data).toBeNull();

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    expect(result.current.data).toEqual(mockData);
    expect(result.current.error).toBeNull();
    expect(fetcher).toHaveBeenCalledTimes(1);
  });

  it("should handle fetch error", async () => {
    const error = new Error("Network error");
    const fetcher = vi.fn().mockRejectedValue(error);

    const { result } = renderHook(() => useQuery(fetcher));

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    expect(result.current.data).toBeNull();
    expect(result.current.error).toEqual(error);
  });

  it("should handle non-Error rejection with custom error message", async () => {
    const fetcher = vi.fn().mockRejectedValue("string error");

    const { result } = renderHook(() =>
      useQuery(fetcher, [], { errorMessage: "Custom error message" }),
    );

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    expect(result.current.error?.message).toBe("Custom error message");
  });

  it("should skip initial fetch when skip option is true", async () => {
    const fetcher = vi.fn().mockResolvedValue({ data: "test" });

    const { result } = renderHook(() => useQuery(fetcher, [], { skip: true }));

    // Should not be loading since skip is true
    expect(result.current.loading).toBe(false);
    expect(fetcher).not.toHaveBeenCalled();
  });

  it("should refetch data when refetch is called", async () => {
    const mockData = { id: 1 };
    const fetcher = vi.fn().mockResolvedValue(mockData);

    const { result } = renderHook(() => useQuery(fetcher));

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    expect(fetcher).toHaveBeenCalledTimes(1);

    await act(async () => {
      await result.current.refetch();
    });

    expect(fetcher).toHaveBeenCalledTimes(2);
  });

  it("should not refetch when skip is true and refetch is called", async () => {
    const fetcher = vi.fn().mockResolvedValue({ data: "test" });

    const { result } = renderHook(() => useQuery(fetcher, [], { skip: true }));

    await act(async () => {
      await result.current.refetch();
    });

    expect(fetcher).not.toHaveBeenCalled();
  });

  it("should refetch when dependencies change", async () => {
    const fetcher = vi.fn().mockResolvedValue({ data: "test" });

    const { result, rerender } = renderHook(
      ({ id }) => useQuery(() => fetcher(id), [id]),
      { initialProps: { id: 1 } },
    );

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    expect(fetcher).toHaveBeenCalledWith(1);

    rerender({ id: 2 });

    await waitFor(() => {
      expect(fetcher).toHaveBeenCalledWith(2);
    });
  });

  it("should use default error message", async () => {
    const fetcher = vi.fn().mockRejectedValue("string error");

    const { result } = renderHook(() => useQuery(fetcher));

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    expect(result.current.error?.message).toBe("Failed to fetch data");
  });

  it("should clear error on successful refetch", async () => {
    const fetcher = vi
      .fn()
      .mockRejectedValueOnce(new Error("Error"))
      .mockResolvedValueOnce({ data: "success" });

    const { result } = renderHook(() => useQuery(fetcher));

    await waitFor(() => {
      expect(result.current.error).not.toBeNull();
    });

    await act(async () => {
      await result.current.refetch();
    });

    expect(result.current.error).toBeNull();
    expect(result.current.data).toEqual({ data: "success" });
  });
});

describe("useMutation", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("should execute mutation and return result", async () => {
    const mockResult = { id: 1, created: true };
    const mutator = vi.fn().mockResolvedValue(mockResult);

    const { result } = renderHook(() =>
      useMutation<typeof mockResult, { name: string }>(mutator),
    );

    expect(result.current.loading).toBe(false);
    expect(result.current.error).toBeNull();

    let mutationResult: typeof mockResult | undefined;
    await act(async () => {
      mutationResult = await result.current.mutate({ name: "test" });
    });

    expect(mutationResult).toEqual(mockResult);
    expect(mutator).toHaveBeenCalledWith({ name: "test" });
    expect(result.current.loading).toBe(false);
  });

  it("should set loading state during mutation", async () => {
    let resolveFn: (value: unknown) => void;
    const mutator = vi.fn().mockImplementation(
      () =>
        new Promise((resolve) => {
          resolveFn = resolve;
        }),
    );

    const { result } = renderHook(() => useMutation(mutator));

    expect(result.current.loading).toBe(false);

    let mutatePromise: Promise<unknown>;
    act(() => {
      mutatePromise = result.current.mutate({});
    });

    await waitFor(() => {
      expect(result.current.loading).toBe(true);
    });

    await act(async () => {
      resolveFn!({ success: true });
      await mutatePromise;
    });

    expect(result.current.loading).toBe(false);
  });

  it("should handle mutation error", async () => {
    const error = new Error("Mutation failed");
    const mutator = vi.fn().mockRejectedValue(error);

    const { result } = renderHook(() => useMutation(mutator));

    let caughtError: Error | undefined;
    await act(async () => {
      try {
        await result.current.mutate({});
      } catch (err) {
        caughtError = err as Error;
      }
    });

    expect(caughtError).toEqual(error);
    expect(result.current.error).toEqual(error);
    expect(result.current.loading).toBe(false);
  });

  it("should handle non-Error rejection", async () => {
    const mutator = vi.fn().mockRejectedValue("string error");

    const { result } = renderHook(() =>
      useMutation(mutator, { errorMessage: "Custom mutation error" }),
    );

    let caughtError: Error | undefined;
    await act(async () => {
      try {
        await result.current.mutate({});
      } catch (err) {
        caughtError = err as Error;
      }
    });

    expect(caughtError?.message).toBe("Custom mutation error");
    expect(result.current.error?.message).toBe("Custom mutation error");
  });

  it("should call onSuccess callback after successful mutation", async () => {
    const onSuccess = vi.fn();
    const mutator = vi.fn().mockResolvedValue({ success: true });

    const { result } = renderHook(() => useMutation(mutator, { onSuccess }));

    await act(async () => {
      await result.current.mutate({});
    });

    expect(onSuccess).toHaveBeenCalledTimes(1);
  });

  it("should not call onSuccess callback on error", async () => {
    const onSuccess = vi.fn();
    const mutator = vi.fn().mockRejectedValue(new Error("Failed"));

    const { result } = renderHook(() => useMutation(mutator, { onSuccess }));

    await act(async () => {
      try {
        await result.current.mutate({});
      } catch {
        // Expected
      }
    });

    expect(onSuccess).not.toHaveBeenCalled();
  });

  it("should use default error message when not provided", async () => {
    const mutator = vi.fn().mockRejectedValue("string error");

    const { result } = renderHook(() => useMutation(mutator));

    await act(async () => {
      try {
        await result.current.mutate({});
      } catch {
        // Expected
      }
    });

    expect(result.current.error?.message).toBe("Operation failed");
  });

  it("should clear error on successful mutation after previous error", async () => {
    const mutator = vi
      .fn()
      .mockRejectedValueOnce(new Error("First error"))
      .mockResolvedValueOnce({ success: true });

    const { result } = renderHook(() => useMutation(mutator));

    // First mutation fails
    await act(async () => {
      try {
        await result.current.mutate({});
      } catch {
        // Expected
      }
    });

    expect(result.current.error).not.toBeNull();

    // Second mutation succeeds
    await act(async () => {
      await result.current.mutate({});
    });

    expect(result.current.error).toBeNull();
  });
});
