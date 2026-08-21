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
    providerImports: {
      preview: vi.fn(),
      commit: vi.fn(),
      discard: vi.fn(),
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
      get: vi.fn(),
    },
    stats: {
      get: vi.fn(),
    },
    tokenUsage: {
      get: vi.fn(),
    },
    requests: {
      active: vi.fn().mockResolvedValue({ requests: [], count: 0 }),
    },
    debugCapture: {
      status: vi.fn().mockResolvedValue({
        state: "stopped",
        process_memory: {
          ceiling_bytes: 536_870_912,
          charged_bytes: 0,
          retained_bytes: 0,
          pinned_bytes: 0,
          releasing_bytes: 0,
          temporary_bytes: 0,
        },
        pending_export_count: 0,
        active_download_count: 0,
        session: null,
      }),
      start: vi.fn(),
      stop: vi.fn(),
      listRecords: vi.fn(),
      getRecord: vi.fn(),
      createExport: vi.fn(),
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
