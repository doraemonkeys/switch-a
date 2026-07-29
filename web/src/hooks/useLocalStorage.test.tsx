import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { renderHook, act } from "@testing-library/react";
import { useLocalStorage } from "./useLocalStorage";

describe("useLocalStorage", () => {
  const mockStorage: Record<string, string> = {};

  beforeEach(() => {
    // Clear mock storage
    Object.keys(mockStorage).forEach((key) => delete mockStorage[key]);

    // Mock localStorage
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(
      (key) => mockStorage[key] ?? null,
    );
    vi.spyOn(Storage.prototype, "setItem").mockImplementation((key, value) => {
      mockStorage[key] = value;
    });
    vi.spyOn(Storage.prototype, "removeItem").mockImplementation((key) => {
      delete mockStorage[key];
    });
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("should return initial value when localStorage is empty", () => {
    const { result } = renderHook(() =>
      useLocalStorage("testKey", "initialValue"),
    );

    expect(result.current[0]).toBe("initialValue");
  });

  it("should return stored value from localStorage", () => {
    mockStorage["testKey"] = JSON.stringify("storedValue");

    const { result } = renderHook(() =>
      useLocalStorage("testKey", "initialValue"),
    );

    expect(result.current[0]).toBe("storedValue");
  });

  it("should update localStorage when setValue is called", () => {
    const { result } = renderHook(() =>
      useLocalStorage("testKey", "initialValue"),
    );

    act(() => {
      result.current[1]("newValue");
    });

    expect(mockStorage["testKey"]).toBe(JSON.stringify("newValue"));
  });

  it("should support function updater", () => {
    mockStorage["testKey"] = JSON.stringify(5);

    const { result } = renderHook(() => useLocalStorage<number>("testKey", 0));

    act(() => {
      result.current[1]((prev) => prev + 10);
    });

    expect(mockStorage["testKey"]).toBe(JSON.stringify(15));
  });

  it("should handle object values via JSON serialization", () => {
    // Test that objects are properly serialized to localStorage
    const { result } = renderHook(() =>
      useLocalStorage<string>("objectKey", "initial"),
    );

    // Set a JSON string value (simulating object storage)
    act(() => {
      result.current[1](JSON.stringify({ name: "test", count: 5 }));
    });

    expect(mockStorage["objectKey"]).toBe(
      JSON.stringify(JSON.stringify({ name: "test", count: 5 })),
    );
  });

  it("should handle array values via JSON serialization", () => {
    // Test that arrays are properly serialized to localStorage
    const { result } = renderHook(() =>
      useLocalStorage<string>("arrayKey", "initial"),
    );

    act(() => {
      result.current[1](JSON.stringify([1, 2, 3]));
    });

    expect(mockStorage["arrayKey"]).toBe(
      JSON.stringify(JSON.stringify([1, 2, 3])),
    );
  });

  it("should handle JSON parse errors gracefully", () => {
    mockStorage["testKey"] = "invalid json {";
    const consoleSpy = vi.spyOn(console, "error").mockImplementation(() => {});

    const { result } = renderHook(() => useLocalStorage("testKey", "fallback"));

    expect(result.current[0]).toBe("fallback");
    expect(consoleSpy).toHaveBeenCalled();

    consoleSpy.mockRestore();
  });

  it("should dispatch custom event for same-tab sync", () => {
    const dispatchSpy = vi.spyOn(window, "dispatchEvent");

    const { result } = renderHook(() =>
      useLocalStorage("testKey", "initialValue"),
    );

    act(() => {
      result.current[1]("newValue");
    });

    expect(dispatchSpy).toHaveBeenCalledWith(
      expect.objectContaining({ type: "localStorage:testKey" }),
    );
  });

  it("should update value when storage event fires for same key", () => {
    const { result } = renderHook(() =>
      useLocalStorage("testKey", "initialValue"),
    );

    // Simulate external storage change
    mockStorage["testKey"] = JSON.stringify("externalUpdate");

    act(() => {
      window.dispatchEvent(
        new StorageEvent("storage", {
          key: "testKey",
          newValue: JSON.stringify("externalUpdate"),
        }),
      );
    });

    expect(result.current[0]).toBe("externalUpdate");
  });

  it("should not update value when storage event fires for different key", () => {
    const { result } = renderHook(() =>
      useLocalStorage("testKey", "initialValue"),
    );

    act(() => {
      window.dispatchEvent(
        new StorageEvent("storage", {
          key: "differentKey",
          newValue: JSON.stringify("someValue"),
        }),
      );
    });

    expect(result.current[0]).toBe("initialValue");
  });

  it("should update value when custom localStorage event fires", () => {
    const { result } = renderHook(() =>
      useLocalStorage("testKey", "initialValue"),
    );

    // Simulate same-tab storage change
    mockStorage["testKey"] = JSON.stringify("sameTabUpdate");

    act(() => {
      window.dispatchEvent(new Event("localStorage:testKey"));
    });

    expect(result.current[0]).toBe("sameTabUpdate");
  });

  it("should cleanup event listeners on unmount", () => {
    const removeEventListenerSpy = vi.spyOn(window, "removeEventListener");

    const { unmount } = renderHook(() =>
      useLocalStorage("testKey", "initialValue"),
    );

    unmount();

    expect(removeEventListenerSpy).toHaveBeenCalledWith(
      "storage",
      expect.any(Function),
    );
    expect(removeEventListenerSpy).toHaveBeenCalledWith(
      "localStorage:testKey",
      expect.any(Function),
    );
  });

  it("should handle localStorage setItem error gracefully", () => {
    const consoleSpy = vi.spyOn(console, "error").mockImplementation(() => {});
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("QuotaExceededError");
    });

    const { result } = renderHook(() =>
      useLocalStorage("testKey", "initialValue"),
    );

    act(() => {
      result.current[1]("newValue");
    });

    expect(consoleSpy).toHaveBeenCalledWith(
      expect.stringContaining('Error setting localStorage key "testKey"'),
      expect.any(Error),
    );

    consoleSpy.mockRestore();
  });

  it("should work with boolean values", () => {
    const { result } = renderHook(() => useLocalStorage("boolKey", false));

    expect(result.current[0]).toBe(false);

    act(() => {
      result.current[1](true);
    });

    expect(mockStorage["boolKey"]).toBe("true");
  });

  it("should work with null values", () => {
    const { result } = renderHook(() =>
      useLocalStorage<string | null>("nullKey", null),
    );

    expect(result.current[0]).toBeNull();

    act(() => {
      result.current[1]("not null");
    });

    expect(mockStorage["nullKey"]).toBe(JSON.stringify("not null"));
  });
});
