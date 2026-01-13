import { vi } from "vitest";
import type { Storage, HttpClient } from "./interfaces";

// Mock storage implementation
export function createMockStorage(): Storage & { data: Map<string, string> } {
  const data = new Map<string, string>();
  return {
    data,
    getItem: vi.fn((key: string) => data.get(key) ?? null),
    setItem: vi.fn((key: string, value: string) => {
      data.set(key, value);
    }),
    removeItem: vi.fn((key: string) => {
      data.delete(key);
    }),
  };
}

// Mock HTTP client implementation
export function createMockHttpClient(): HttpClient & {
  mockResponse: (response: Partial<Response>) => void;
} {
  const mockFetch = vi.fn();

  return {
    fetch: mockFetch,
    mockResponse: (response: Partial<Response>) => {
      mockFetch.mockResolvedValue({
        ok: response.ok ?? true,
        status: response.status ?? 200,
        statusText: response.statusText ?? "OK",
        json: response.json ?? (() => Promise.resolve({})),
        ...response,
      });
    },
  };
}
