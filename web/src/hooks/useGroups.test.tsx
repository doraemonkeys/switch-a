import { describe, it, expect, vi, beforeEach } from "vitest";
import { renderHook, waitFor, act } from "@testing-library/react";
import { useGroups, useGroup } from "./useGroups";
import type { ApiClient, Group } from "../api/client";
import { createMockApiClient, createWrapper } from "./test-utils";

const mockGroup: Group = {
  id: "1",
  name: "Primary",
  strategy: "priority",
  priority: 1,
  weight: 1,
  enabled: true,
  created_at: "2024-01-01T00:00:00Z",
  updated_at: "2024-01-01T00:00:00Z",
};

function setupMockApiClient(): ApiClient {
  const mockApi = createMockApiClient();
  mockApi.groups.list = vi.fn().mockResolvedValue([mockGroup]);
  mockApi.groups.get = vi.fn().mockResolvedValue(mockGroup);
  mockApi.groups.create = vi.fn().mockResolvedValue(mockGroup);
  mockApi.groups.update = vi.fn().mockResolvedValue(mockGroup);
  mockApi.groups.delete = vi.fn().mockResolvedValue(undefined);
  mockApi.groups.enable = vi
    .fn()
    .mockResolvedValue({ ...mockGroup, enabled: true });
  mockApi.groups.disable = vi
    .fn()
    .mockResolvedValue({ ...mockGroup, enabled: false });
  return mockApi;
}

describe("useGroups", () => {
  let mockApi: ApiClient;

  beforeEach(() => {
    mockApi = setupMockApiClient();
  });

  it("should fetch groups on mount", async () => {
    const { result } = renderHook(() => useGroups(), {
      wrapper: createWrapper(mockApi),
    });

    expect(result.current.loading).toBe(true);

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    expect(result.current.groups).toEqual([mockGroup]);
    expect(result.current.error).toBeNull();
    expect(mockApi.groups.list).toHaveBeenCalled();
  });

  it("should handle fetch error", async () => {
    mockApi.groups.list = vi.fn().mockRejectedValue(new Error("Network error"));

    const { result } = renderHook(() => useGroups(), {
      wrapper: createWrapper(mockApi),
    });

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    expect(result.current.error).toBeInstanceOf(Error);
    expect(result.current.error?.message).toBe("Network error");
    expect(result.current.groups).toEqual([]);
  });

  it("should handle non-Error rejection", async () => {
    mockApi.groups.list = vi.fn().mockRejectedValue("string error");

    const { result } = renderHook(() => useGroups(), {
      wrapper: createWrapper(mockApi),
    });

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    expect(result.current.error?.message).toBe("Failed to fetch groups");
  });

  it("should refetch groups", async () => {
    const { result } = renderHook(() => useGroups(), {
      wrapper: createWrapper(mockApi),
    });

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    await act(async () => {
      await result.current.refetch();
    });

    expect(mockApi.groups.list).toHaveBeenCalledTimes(2);
  });

  it("should create group and refetch", async () => {
    const { result } = renderHook(() => useGroups(), {
      wrapper: createWrapper(mockApi),
    });

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    const input = { name: "New Group" };
    await act(async () => {
      await result.current.createGroup(input);
    });

    expect(mockApi.groups.create).toHaveBeenCalledWith(input);
    expect(mockApi.groups.list).toHaveBeenCalledTimes(2);
  });

  it("should update group and refetch", async () => {
    const { result } = renderHook(() => useGroups(), {
      wrapper: createWrapper(mockApi),
    });

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    const input = { name: "Updated Group" };
    await act(async () => {
      await result.current.updateGroup("1", input);
    });

    expect(mockApi.groups.update).toHaveBeenCalledWith("1", input);
    expect(mockApi.groups.list).toHaveBeenCalledTimes(2);
  });

  it("should delete group and refetch", async () => {
    const { result } = renderHook(() => useGroups(), {
      wrapper: createWrapper(mockApi),
    });

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    await act(async () => {
      await result.current.deleteGroup("1");
    });

    expect(mockApi.groups.delete).toHaveBeenCalledWith("1");
    expect(mockApi.groups.list).toHaveBeenCalledTimes(2);
  });

  it("should enable group and refetch", async () => {
    const { result } = renderHook(() => useGroups(), {
      wrapper: createWrapper(mockApi),
    });

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    await act(async () => {
      await result.current.enableGroup("1");
    });

    expect(mockApi.groups.enable).toHaveBeenCalledWith("1");
    expect(mockApi.groups.list).toHaveBeenCalledTimes(2);
  });

  it("should disable group and refetch", async () => {
    const { result } = renderHook(() => useGroups(), {
      wrapper: createWrapper(mockApi),
    });

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    await act(async () => {
      await result.current.disableGroup("1");
    });

    expect(mockApi.groups.disable).toHaveBeenCalledWith("1");
    expect(mockApi.groups.list).toHaveBeenCalledTimes(2);
  });
});

describe("useGroup", () => {
  let mockApi: ApiClient;

  beforeEach(() => {
    mockApi = setupMockApiClient();
  });

  it("should fetch single group on mount", async () => {
    const { result } = renderHook(() => useGroup("1"), {
      wrapper: createWrapper(mockApi),
    });

    expect(result.current.loading).toBe(true);

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    expect(result.current.group).toEqual(mockGroup);
    expect(result.current.error).toBeNull();
    expect(mockApi.groups.get).toHaveBeenCalledWith("1");
  });

  it("should handle fetch error", async () => {
    mockApi.groups.get = vi.fn().mockRejectedValue(new Error("Not found"));

    const { result } = renderHook(() => useGroup("1"), {
      wrapper: createWrapper(mockApi),
    });

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    expect(result.current.error).toBeInstanceOf(Error);
    expect(result.current.error?.message).toBe("Not found");
  });

  it("should handle non-Error rejection", async () => {
    mockApi.groups.get = vi.fn().mockRejectedValue("string error");

    const { result } = renderHook(() => useGroup("1"), {
      wrapper: createWrapper(mockApi),
    });

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    expect(result.current.error?.message).toBe("Failed to fetch group");
  });

  it("should not fetch when id is empty and set loading to false", async () => {
    const { result } = renderHook(() => useGroup(""), {
      wrapper: createWrapper(mockApi),
    });

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    expect(mockApi.groups.get).not.toHaveBeenCalled();
    expect(result.current.group).toBeNull();
  });

  it("should refetch group", async () => {
    const { result } = renderHook(() => useGroup("1"), {
      wrapper: createWrapper(mockApi),
    });

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    await act(async () => {
      await result.current.refetch();
    });

    expect(mockApi.groups.get).toHaveBeenCalledTimes(2);
  });
});
