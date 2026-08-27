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
  version: "5.0",
  exported_at: "2024-01-01T00:00:00Z",
  providers: [
    {
      id: "1",
      name: "Provider 1",
      api_types: [
        {
          api_type: "claude",
          base_url: "https://api.example.com",
          credential_session_id: "session-1",
        },
      ],
      auth_mode: "bearer",
      group_id: null,
      weight: 1,
      priority: 1,
      concurrency: 10,
      max_retries: 3,
      enabled: true,
    },
  ],
  credential_sessions: [],
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
  routing_policies: [
    {
      api_type: "claude",
      enabled: true,
      model_match_type: "prefix",
      model_match_value: "claude-3",
      target_provider_id: null,
      allowed_group_ids: ["g1"],
      allowed_vendors: [],
    },
  ],
  settings: {
    auth_mode: "auto",
  },
  internal_error_rules: [],
};

const mockPreviewResponse: ImportPreviewResponse = {
  dry_run: true,
  changes: {
    providers: { add: 1, update: 0, delete: 0, unchanged: 0 },
    credential_sessions: { add: 0, update: 0, delete: 0, unchanged: 0 },
    groups: { add: 0, update: 1, delete: 0, unchanged: 0 },
    routing_policies: { add: 0, update: 1, delete: 0, unchanged: 0 },
    settings: { add: 0, update: 2, delete: 0, unchanged: 0 },
    internal_error_rules: { add: 0, update: 0, delete: 0, unchanged: 0 },
  },
  warnings: ["Provider API key will be overwritten"],
  rule_set_revision: "0",
  rule_set_etag: '"internal-error-rules/0"',
};

const mockImportResult: ImportResult = {
  success: true,
  applied: {
    providers: { added: 1, updated: 0, deleted: 0 },
    credential_sessions: { added: 0, updated: 0, deleted: 0 },
    groups: { added: 0, updated: 1, deleted: 0 },
    routing_policies: { added: 0, updated: 1, deleted: 0 },
    settings: { added: 0, updated: 2, deleted: 0 },
    internal_error_rules: { added: 0, updated: 0, deleted: 0 },
  },
  rule_set_revision: "0",
  rule_set_etag: '"internal-error-rules/0"',
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
      version: "5.0",
      import_scope: {
        mode: "full" as const,
      },
      providers: mockExportedConfig.providers,
      credential_sessions: mockExportedConfig.credential_sessions,
      groups: mockExportedConfig.groups,
      routing_policies: mockExportedConfig.routing_policies,
      settings: mockExportedConfig.settings,
      internal_error_rules: mockExportedConfig.internal_error_rules,
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
      version: "5.0",
      import_scope: {
        mode: "full" as const,
      },
      providers: mockExportedConfig.providers,
      credential_sessions: mockExportedConfig.credential_sessions,
      groups: mockExportedConfig.groups,
      routing_policies: mockExportedConfig.routing_policies,
      settings: mockExportedConfig.settings,
      internal_error_rules: mockExportedConfig.internal_error_rules,
    };

    let importResult: ImportResult | undefined;
    await act(async () => {
      importResult = await result.current.importConfig(
        importData,
        mockPreviewResponse.rule_set_etag,
      );
    });

    expect(mockApi.config.import).toHaveBeenCalledWith(
      importData,
      mockPreviewResponse.rule_set_etag,
    );
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
      version: "5.0",
      import_scope: {
        mode: "settings_only" as const,
      },
      providers: [],
      credential_sessions: [],
      groups: [],
      routing_policies: [],
      settings: {},
      internal_error_rules: [],
    };

    act(() => {
      result.current.importConfig(
        importData,
        mockPreviewResponse.rule_set_etag,
      );
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
        await result.current.importConfig(
          {
            version: "5.0",
            import_scope: {
              mode: "settings_only" as const,
            },
            providers: [],
            credential_sessions: [],
            groups: [],
            routing_policies: [],
            settings: {},
            internal_error_rules: [],
          },
          mockPreviewResponse.rule_set_etag,
        );
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
          version: "5.0",
          import_scope: {
            mode: "settings_only" as const,
          },
          providers: [],
          credential_sessions: [],
          groups: [],
          routing_policies: [],
          settings: {},
          internal_error_rules: [],
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
