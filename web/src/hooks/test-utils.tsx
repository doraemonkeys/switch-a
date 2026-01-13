import { vi } from "vitest";
import type { ReactNode } from "react";
import { ApiContext } from "../api/context";
import type { ApiClient } from "../api/client";

/**
 * Creates a mock ApiClient with all methods stubbed via vi.fn().
 * This ensures consistency across all test files and matches the real ApiClient interface.
 */
export function createMockApiClient(): ApiClient {
  return {
    setToken: vi.fn(),
    clearToken: vi.fn(),
    getToken: vi.fn(),
    validateToken: vi.fn().mockResolvedValue(true),
    providers: {
      list: vi.fn().mockResolvedValue([]),
      get: vi.fn(),
      create: vi.fn(),
      update: vi.fn(),
      delete: vi.fn(),
      enable: vi.fn(),
      disable: vi.fn(),
      reset: vi.fn(),
      batch: vi.fn(),
    },
    groups: {
      list: vi.fn().mockResolvedValue([]),
      get: vi.fn(),
      create: vi.fn(),
      update: vi.fn(),
      delete: vi.fn(),
      enable: vi.fn(),
      disable: vi.fn(),
    },
    config: {
      get: vi.fn(),
      update: vi.fn(),
      export: vi.fn(),
      importPreview: vi.fn(),
      import: vi.fn(),
    },
    status: {
      get: vi.fn(),
      health: vi.fn(),
    },
    logs: {
      list: vi.fn(),
    },
    stats: {
      get: vi.fn(),
    },
  } as unknown as ApiClient;
}

/**
 * Creates a wrapper component for testing hooks that use the ApiContext.
 */
export function createWrapper(apiClient: ApiClient) {
  return ({ children }: { children: ReactNode }) => (
    <ApiContext.Provider value={apiClient}>{children}</ApiContext.Provider>
  );
}
