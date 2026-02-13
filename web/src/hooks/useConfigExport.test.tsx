import { describe, it, expect, vi, beforeEach } from "vitest";
import { renderHook, waitFor, act } from "@testing-library/react";
import { useConfigExport } from "./useConfigExport";
import type {
  ApiClient,
  ExportedConfig,
  ImportPreviewResponse,
  ImportResult,
} from "../api/client";
import { createMockApiClient, createWrapper } from "./test-utils";

const mockExportedConfig: ExportedConfig = {
  version: "1.0",
  exported_at: "2024-01-01T00:00:00Z",
  providers: [
    {
      id: "1",
      name: "Provider 1",
      api_key: "key-123",
      api_types: [{ api_type: "claude", base_url: "https://api.example.com" }],
      auth_mode: "bearer",
      group_id: null,
      weight: 1,
      priority: 1,
      concurrency: 10,
      max_retries: 3,
      enabled: true,
    },
  ],
  groups: [
    {
      id: "g1",
      name: "Group 1",
      strategy: "priority",
      priority: 1,
      weight: 1,
      enabled: true,
    },
  ],
  settings: {
    auth_mode: "auto",
  },
};

const mockPreviewResponse: ImportPreviewResponse = {
  dry_run: true,
  changes: {
    providers: { add: 1, update: 0, delete: 0 },
    groups: { add: 0, update: 1, delete: 0 },
    settings: { add: 0, update: 2, delete: 0 },
  },
  warnings: ["Provider API key will be overwritten"],
};

const mockImportResult: ImportResult = {
  success: true,
  applied: {
    providers: { added: 1, updated: 0 },
    groups: { added: 0, updated: 1 },
    settings: { added: 0, updated: 2 },
  },
};

function setupMockApiClient(): ApiClient {
  const mockApi = createMockApiClient();
  mockApi.config.export = vi.fn().mockResolvedValue(mockExportedConfig);
  mockApi.config.importPreview = vi.fn().mockResolvedValue(mockPreviewResponse);
  mockApi.config.import = vi.fn().mockResolvedValue(mockImportResult);
  return mockApi;
}

describe("useConfigExport", () => {
  let mockApi: ApiClient;

  beforeEach(() => {
    mockApi = setupMockApiClient();
  });

  it("should start with initial state", () => {
    const { result } = renderHook(() => useConfigExport(), {
      wrapper: createWrapper(mockApi),
    });

    expect(result.current.exporting).toBe(false);
    expect(result.current.importing).toBe(false);
    expect(result.current.error).toBeNull();
    expect(result.current.exportedConfig).toBeNull();
    expect(result.current.preview).toBeNull();
    expect(result.current.importResult).toBeNull();
  });

  it("should export config", async () => {
    const { result } = renderHook(() => useConfigExport(), {
      wrapper: createWrapper(mockApi),
    });

    let config: ExportedConfig | undefined;
    await act(async () => {
      config = await result.current.exportConfig();
    });

    expect(mockApi.config.export).toHaveBeenCalled();
    expect(config).toEqual(mockExportedConfig);
    expect(result.current.exportedConfig).toEqual(mockExportedConfig);
    expect(result.current.exporting).toBe(false);
  });

  it("should set exporting state during export", async () => {
    let resolvePromise: (value: ExportedConfig) => void;
    mockApi.config.export = vi.fn().mockImplementation(
      () =>
        new Promise<ExportedConfig>((resolve) => {
          resolvePromise = resolve;
        }),
    );

    const { result } = renderHook(() => useConfigExport(), {
      wrapper: createWrapper(mockApi),
    });

    act(() => {
      result.current.exportConfig();
    });

    await waitFor(() => {
      expect(result.current.exporting).toBe(true);
    });

    await act(async () => {
      resolvePromise!(mockExportedConfig);
    });

    await waitFor(() => {
      expect(result.current.exporting).toBe(false);
    });
  });

  it("should handle export error", async () => {
    mockApi.config.export = vi
      .fn()
      .mockRejectedValue(new Error("Export failed"));

    const { result } = renderHook(() => useConfigExport(), {
      wrapper: createWrapper(mockApi),
    });

    await act(async () => {
      try {
        await result.current.exportConfig();
      } catch {
        // Expected to throw
      }
    });

    await waitFor(() => {
      expect(result.current.error).toBeInstanceOf(Error);
    });
    expect(result.current.error?.message).toBe("Export failed");
  });

  it("should preview import", async () => {
    const { result } = renderHook(() => useConfigExport(), {
      wrapper: createWrapper(mockApi),
    });

    const importData = {
      providers: mockExportedConfig.providers,
      groups: mockExportedConfig.groups,
      settings: mockExportedConfig.settings,
    };

    let preview: ImportPreviewResponse | undefined;
    await act(async () => {
      preview = await result.current.previewImport(importData);
    });

    expect(mockApi.config.importPreview).toHaveBeenCalledWith(importData);
    expect(preview).toEqual(mockPreviewResponse);
    expect(result.current.preview).toEqual(mockPreviewResponse);
  });

  it("should import config", async () => {
    const { result } = renderHook(() => useConfigExport(), {
      wrapper: createWrapper(mockApi),
    });

    const importData = {
      providers: mockExportedConfig.providers,
      groups: mockExportedConfig.groups,
      settings: mockExportedConfig.settings,
    };

    let importResult: ImportResult | undefined;
    await act(async () => {
      importResult = await result.current.importConfig(importData);
    });

    expect(mockApi.config.import).toHaveBeenCalledWith(importData);
    expect(importResult).toEqual(mockImportResult);
    expect(result.current.importResult).toEqual(mockImportResult);
  });

  it("should set importing state during import", async () => {
    let resolvePromise: (value: ImportResult) => void;
    mockApi.config.import = vi.fn().mockImplementation(
      () =>
        new Promise<ImportResult>((resolve) => {
          resolvePromise = resolve;
        }),
    );

    const { result } = renderHook(() => useConfigExport(), {
      wrapper: createWrapper(mockApi),
    });

    const importData = {
      providers: [],
      groups: [],
      settings: {},
    };

    act(() => {
      result.current.importConfig(importData);
    });

    await waitFor(() => {
      expect(result.current.importing).toBe(true);
    });

    await act(async () => {
      resolvePromise!(mockImportResult);
    });

    await waitFor(() => {
      expect(result.current.importing).toBe(false);
    });
  });

  it("should handle import error", async () => {
    mockApi.config.import = vi
      .fn()
      .mockRejectedValue(new Error("Import failed"));

    const { result } = renderHook(() => useConfigExport(), {
      wrapper: createWrapper(mockApi),
    });

    await act(async () => {
      try {
        await result.current.importConfig({
          providers: [],
          groups: [],
          settings: {},
        });
      } catch {
        // Expected to throw
      }
    });

    await waitFor(() => {
      expect(result.current.error?.message).toBe("Import failed");
    });
  });

  it("should handle preview error", async () => {
    mockApi.config.importPreview = vi
      .fn()
      .mockRejectedValue(new Error("Preview failed"));

    const { result } = renderHook(() => useConfigExport(), {
      wrapper: createWrapper(mockApi),
    });

    await act(async () => {
      try {
        await result.current.previewImport({
          providers: [],
          groups: [],
          settings: {},
        });
      } catch {
        // Expected to throw
      }
    });

    await waitFor(() => {
      expect(result.current.error?.message).toBe("Preview failed");
    });
  });

  it("should reset state", async () => {
    const { result } = renderHook(() => useConfigExport(), {
      wrapper: createWrapper(mockApi),
    });

    await act(async () => {
      await result.current.exportConfig();
    });

    expect(result.current.exportedConfig).not.toBeNull();

    act(() => {
      result.current.reset();
    });

    expect(result.current.exportedConfig).toBeNull();
    expect(result.current.preview).toBeNull();
    expect(result.current.importResult).toBeNull();
    expect(result.current.error).toBeNull();
  });
});
