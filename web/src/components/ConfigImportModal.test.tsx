import { describe, it, expect, vi, beforeEach } from "vitest";
import {
  act,
  render,
  screen,
  fireEvent,
  waitFor,
} from "@testing-library/react";
import { ConfigImportModal } from "./ConfigImportModal";
import type {
  ExportedConfig,
  ImportPreviewResponse,
  ImportResult,
} from "../api/types";

const exportedConfig: ExportedConfig = {
  version: "3.0",
  exported_at: "2026-03-29T00:00:00Z",
  providers: [
    {
      id: "provider-alpha-1",
      name: "Alpha One",
      api_key: "sk-alpha-1",
      api_types: [{ api_type: "claude", base_url: "https://api.alpha-1.test" }],
      auth_mode: "bearer",
      group_id: "group-alpha",
      weight: 1,
      priority: 1,
      concurrency: 10,
      max_retries: 3,
      enabled: true,
    },
    {
      id: "provider-beta-1",
      name: "Beta One",
      api_key: "sk-beta-1",
      api_types: [{ api_type: "openai", base_url: "https://api.beta-1.test" }],
      auth_mode: "bearer",
      group_id: "group-beta",
      weight: 1,
      priority: 1,
      concurrency: 10,
      max_retries: 3,
      enabled: true,
    },
    {
      id: "provider-solo",
      name: "Solo Provider",
      api_key: "sk-solo",
      api_types: [{ api_type: "gemini", base_url: "https://api.solo.test" }],
      auth_mode: "bearer",
      weight: 1,
      priority: 3,
      concurrency: 10,
      max_retries: 3,
      enabled: true,
    },
  ],
  groups: [
    {
      id: "group-alpha",
      name: "Alpha Group",
      strategy: "priority",
      priority: 1,
      weight: 1,
      enabled: true,
    },
    {
      id: "group-beta",
      name: "Beta Group",
      strategy: "random",
      priority: 2,
      weight: 1,
      enabled: true,
    },
  ],
  routing_policies: [
    {
      api_type: "claude",
      enabled: true,
      model_match_type: "exact",
      model_match_value: "claude-3-7-sonnet",
      target_provider_id: "provider-alpha-1",
      allowed_group_ids: ["group-alpha"],
      allowed_vendors: [],
    },
  ],
  settings: {
    auth_mode: "auto",
  },
};

function createPreviewResponse(
  changes: Partial<{
    [Key in keyof ImportPreviewResponse["changes"]]: Partial<
      ImportPreviewResponse["changes"][Key]
    >;
  }> = {},
): ImportPreviewResponse {
  return {
    dry_run: true,
    changes: {
      providers: {
        add: 0,
        update: 0,
        delete: 0,
        unchanged: 0,
        ...changes.providers,
      },
      groups: {
        add: 0,
        update: 0,
        delete: 0,
        unchanged: 0,
        ...changes.groups,
      },
      routing_policies: {
        add: 0,
        update: 0,
        delete: 0,
        unchanged: 0,
        ...changes.routing_policies,
      },
      settings: {
        add: 0,
        update: 0,
        delete: 0,
        unchanged: 0,
        ...changes.settings,
      },
    },
    warnings: [],
  };
}

function createImportResult(
  applied: Partial<ImportResult["applied"]> = {},
): ImportResult {
  return {
    success: true,
    applied: {
      providers: { added: 0, updated: 0, deleted: 0 },
      groups: { added: 0, updated: 0, deleted: 0 },
      routing_policies: { added: 0, updated: 0, deleted: 0 },
      settings: { added: 0, updated: 0, deleted: 0 },
      ...applied,
    },
  };
}

function createDeferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });

  return { promise, resolve, reject };
}

async function uploadConfigFile(config: ExportedConfig = exportedConfig) {
  const input = document.querySelector(
    'input[type="file"]',
  ) as HTMLInputElement;
  const file = new File(["ignored"], "config.json", {
    type: "application/json",
  });
  const textSpy = vi.fn().mockResolvedValue(JSON.stringify(config));

  Object.defineProperty(file, "text", {
    value: textSpy,
  });

  fireEvent.change(input, { target: { files: [file] } });
  await waitFor(() => expect(textSpy).toHaveBeenCalledTimes(1));

  return { file, textSpy };
}

describe("ConfigImportModal", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("closes on Escape when idle", () => {
    const onClose = vi.fn();

    render(
      <ConfigImportModal
        isOpen={true}
        onClose={onClose}
        onPreview={vi.fn()}
        onImport={vi.fn()}
        importing={false}
      />,
    );

    fireEvent.keyDown(document, { key: "Escape" });

    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("parses the file once and sends a full-scope request for preview and import", async () => {
    const previewResponse = createPreviewResponse({
      providers: { add: 1, update: 1, delete: 0 },
      groups: { add: 0, update: 1, delete: 0 },
      routing_policies: { add: 0, update: 1, delete: 0 },
      settings: { add: 0, update: 1, delete: 0 },
    });
    const importResult = createImportResult({
      providers: { added: 1, updated: 1, deleted: 0 },
      groups: { added: 0, updated: 1, deleted: 0 },
      routing_policies: { added: 0, updated: 1, deleted: 0 },
      settings: { added: 0, updated: 1, deleted: 0 },
    });
    const onPreview = vi.fn().mockResolvedValue(previewResponse);
    const onImport = vi.fn().mockResolvedValue(importResult);

    render(
      <ConfigImportModal
        isOpen={true}
        onClose={vi.fn()}
        onPreview={onPreview}
        onImport={onImport}
        importing={false}
      />,
    );

    const { textSpy } = await uploadConfigFile();

    expect(onPreview).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button", { name: "预览变更" }));

    await waitFor(() => {
      expect(onPreview).toHaveBeenCalledWith({
        version: "3.0",
        import_scope: { mode: "full" },
        providers: exportedConfig.providers,
        groups: exportedConfig.groups,
        routing_policies: exportedConfig.routing_policies,
        settings: exportedConfig.settings,
      });
    });

    fireEvent.click(await screen.findByRole("button", { name: "确认导入" }));

    await waitFor(() => {
      expect(onImport).toHaveBeenCalledWith({
        version: "3.0",
        import_scope: { mode: "full" },
        providers: exportedConfig.providers,
        groups: exportedConfig.groups,
        routing_policies: exportedConfig.routing_policies,
        settings: exportedConfig.settings,
      });
    });

    expect(textSpy).toHaveBeenCalledTimes(1);
    expect(await screen.findByText("导入成功")).toBeInTheDocument();
    expect(screen.getByText("Routing Policies")).toBeInTheDocument();
  });

  it("requires at least one selection before previewing a scoped import", async () => {
    render(
      <ConfigImportModal
        isOpen={true}
        onClose={vi.fn()}
        onPreview={vi.fn()}
        onImport={vi.fn()}
        importing={false}
      />,
    );

    await uploadConfigFile();

    fireEvent.click(screen.getByText("按 Group / Provider 选择"));

    expect(
      screen.getByText("至少选择一个 Group 或 Provider 后才能预览。"),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "预览变更" })).toBeDisabled();
  });

  it("keeps provider-only requests scoped to explicit provider IDs", async () => {
    const onPreview = vi.fn().mockResolvedValue(
      createPreviewResponse({
        providers: { add: 0, update: 1, delete: 0 },
        groups: { add: 0, update: 1, delete: 0 },
      }),
    );

    render(
      <ConfigImportModal
        isOpen={true}
        onClose={vi.fn()}
        onPreview={onPreview}
        onImport={vi.fn()}
        importing={false}
      />,
    );

    await uploadConfigFile();

    fireEvent.click(screen.getByText("按 Group / Provider 选择"));
    fireEvent.click(screen.getByText("Alpha One"));
    fireEvent.click(screen.getByRole("button", { name: "预览变更" }));

    await waitFor(() => {
      expect(onPreview).toHaveBeenCalledWith({
        version: "3.0",
        import_scope: {
          mode: "selection",
          group_ids: [],
          provider_ids: ["provider-alpha-1"],
        },
        providers: exportedConfig.providers,
        groups: exportedConfig.groups,
        routing_policies: exportedConfig.routing_policies,
        settings: exportedConfig.settings,
      });
    });
  });

  it("locks scope controls while previewing to keep the request scope stable", async () => {
    const deferredPreview = createDeferred<ImportPreviewResponse>();
    const onPreview = vi.fn().mockReturnValue(deferredPreview.promise);

    render(
      <ConfigImportModal
        isOpen={true}
        onClose={vi.fn()}
        onPreview={onPreview}
        onImport={vi.fn()}
        importing={false}
      />,
    );

    await uploadConfigFile();

    fireEvent.click(screen.getByText("按 Group / Provider 选择"));

    const selectionRadio = screen.getByRole("radio", {
      name: /按 Group \/ Provider 选择/,
    });
    const settingsRadio = screen.getByRole("radio", {
      name: /仅导入 Settings/,
    });
    const providerCheckbox = screen.getByRole("checkbox", {
      name: /Alpha One/,
    });
    const reselectButton = screen.getByRole("button", { name: "重新选择文件" });

    fireEvent.click(providerCheckbox);
    fireEvent.click(screen.getByRole("button", { name: "预览变更" }));

    await waitFor(() => {
      expect(onPreview).toHaveBeenCalledTimes(1);
    });

    expect(screen.getByRole("button", { name: "预览中..." })).toBeDisabled();
    expect(selectionRadio).toBeDisabled();
    expect(settingsRadio).toBeDisabled();
    expect(providerCheckbox).toBeDisabled();
    expect(reselectButton).toBeDisabled();

    await act(async () => {
      deferredPreview.resolve(
        createPreviewResponse({
          providers: { add: 0, update: 1, delete: 0 },
          groups: { add: 0, update: 1, delete: 0 },
        }),
      );
      await deferredPreview.promise;
    });

    expect(await screen.findByText("变更预览")).toBeInTheDocument();
    expect(onPreview).toHaveBeenCalledWith({
      version: "3.0",
      import_scope: {
        mode: "selection",
        group_ids: [],
        provider_ids: ["provider-alpha-1"],
      },
      providers: exportedConfig.providers,
      groups: exportedConfig.groups,
      routing_policies: exportedConfig.routing_policies,
      settings: exportedConfig.settings,
    });
  });

  it("allows provider-only selection when the provider has no group", async () => {
    const onPreview = vi.fn().mockResolvedValue(
      createPreviewResponse({
        providers: { add: 0, update: 1, delete: 0 },
      }),
    );

    render(
      <ConfigImportModal
        isOpen={true}
        onClose={vi.fn()}
        onPreview={onPreview}
        onImport={vi.fn()}
        importing={false}
      />,
    );

    await uploadConfigFile();

    fireEvent.click(screen.getByText("按 Group / Provider 选择"));
    fireEvent.click(screen.getByText("Solo Provider"));
    fireEvent.click(screen.getByRole("button", { name: "预览变更" }));

    await waitFor(() => {
      expect(onPreview).toHaveBeenCalledWith({
        version: "3.0",
        import_scope: {
          mode: "selection",
          group_ids: [],
          provider_ids: ["provider-solo"],
        },
        providers: exportedConfig.providers,
        groups: exportedConfig.groups,
        routing_policies: exportedConfig.routing_policies,
        settings: exportedConfig.settings,
      });
    });
  });

  it("explains that selected groups also import their providers", async () => {
    const onPreview = vi.fn().mockResolvedValue(
      createPreviewResponse({
        providers: { add: 0, update: 1, delete: 0 },
        groups: { add: 0, update: 1, delete: 0 },
      }),
    );

    render(
      <ConfigImportModal
        isOpen={true}
        onClose={vi.fn()}
        onPreview={onPreview}
        onImport={vi.fn()}
        importing={false}
      />,
    );

    await uploadConfigFile();

    fireEvent.click(screen.getByText("按 Group / Provider 选择"));

    expect(
      screen.getByText(
        /选中的 Group 会同时导入该 Group 下的 Providers；选中的 Provider 会自动补齐其所属 Group/,
      ),
    ).toBeInTheDocument();

    fireEvent.click(screen.getByText("Alpha Group"));

    expect(
      screen.getByText("已选 1 个 Group，会同时导入其下 1 个 Provider。"),
    ).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "预览变更" }));

    await waitFor(() => {
      expect(onPreview).toHaveBeenCalledWith({
        version: "3.0",
        import_scope: {
          mode: "selection",
          group_ids: ["group-alpha"],
          provider_ids: [],
        },
        providers: exportedConfig.providers,
        groups: exportedConfig.groups,
        routing_policies: exportedConfig.routing_policies,
        settings: exportedConfig.settings,
      });
    });
  });
});

describe("ConfigImportModal preview behavior", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("keeps settings-only imports independent from provider and group selections", async () => {
    const onPreview = vi.fn().mockResolvedValue(
      createPreviewResponse({
        settings: { add: 0, update: 1, delete: 0 },
      }),
    );

    render(
      <ConfigImportModal
        isOpen={true}
        onClose={vi.fn()}
        onPreview={onPreview}
        onImport={vi.fn()}
        importing={false}
      />,
    );

    await uploadConfigFile();

    fireEvent.click(screen.getByText("按 Group / Provider 选择"));
    fireEvent.click(screen.getByText("Alpha One"));
    fireEvent.click(screen.getByText("仅导入 Settings"));
    fireEvent.click(screen.getByRole("button", { name: "预览变更" }));

    await waitFor(() => {
      expect(onPreview).toHaveBeenCalledWith({
        version: "3.0",
        import_scope: { mode: "settings_only" },
        providers: exportedConfig.providers,
        groups: exportedConfig.groups,
        routing_policies: exportedConfig.routing_policies,
        settings: exportedConfig.settings,
      });
    });

    expect(await screen.findByText("Settings")).toBeInTheDocument();
    expect(screen.queryByText("Providers")).not.toBeInTheDocument();
    expect(screen.queryByText("Groups")).not.toBeInTheDocument();
    expect(screen.queryByText("Routing Policies")).not.toBeInTheDocument();
  });

  it("ignores routing-policy-only deltas for scoped preview gating", async () => {
    const onPreview = vi.fn().mockResolvedValue(
      createPreviewResponse({
        providers: { unchanged: 2 },
        groups: { unchanged: 1 },
        routing_policies: { add: 0, update: 1, delete: 0 },
      }),
    );

    render(
      <ConfigImportModal
        isOpen={true}
        onClose={vi.fn()}
        onPreview={onPreview}
        onImport={vi.fn()}
        importing={false}
      />,
    );

    await uploadConfigFile();

    fireEvent.click(screen.getByText("按 Group / Provider 选择"));
    fireEvent.click(screen.getByText("Alpha Group"));
    fireEvent.click(screen.getByRole("button", { name: "预览变更" }));

    await screen.findByText("没有检测到任何变更，配置已是最新");

    expect(screen.queryByText("Routing Policies")).not.toBeInTheDocument();
    expect(screen.getByText("2 无变化")).toBeInTheDocument();
    expect(screen.getByText("1 无变化")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "确认导入" })).toBeDisabled();
  });

  it("tolerates null preview warnings without crashing", async () => {
    const onPreview = vi.fn().mockResolvedValue({
      ...createPreviewResponse({
        providers: { unchanged: 1 },
      }),
      warnings: null,
    });

    render(
      <ConfigImportModal
        isOpen={true}
        onClose={vi.fn()}
        onPreview={onPreview}
        onImport={vi.fn()}
        importing={false}
      />,
    );

    await uploadConfigFile();
    fireEvent.click(screen.getByRole("button", { name: "预览变更" }));

    expect(await screen.findByText("变更预览")).toBeInTheDocument();
    expect(screen.queryByText("警告信息")).not.toBeInTheDocument();
  });

  it("disables confirm when preview warnings indicate apply would be rejected", async () => {
    const onPreview = vi.fn().mockResolvedValue({
      ...createPreviewResponse({
        providers: { add: 0, update: 1, delete: 0 },
        groups: { add: 0, update: 1, delete: 0 },
      }),
      warnings: [
        'Selected provider "provider-missing" was not found in the import file',
      ],
    });

    render(
      <ConfigImportModal
        isOpen={true}
        onClose={vi.fn()}
        onPreview={onPreview}
        onImport={vi.fn()}
        importing={false}
      />,
    );

    await uploadConfigFile();

    fireEvent.click(screen.getByText("按 Group / Provider 选择"));
    fireEvent.click(screen.getByText("Alpha Group"));
    fireEvent.click(screen.getByRole("button", { name: "预览变更" }));

    expect(
      await screen.findByText(
        'Selected provider "provider-missing" was not found in the import file',
      ),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "确认导入" })).toBeDisabled();
  });

  it("shows routing-policy deltas in full preview and full result summaries", async () => {
    const onPreview = vi.fn().mockResolvedValue(
      createPreviewResponse({
        routing_policies: { add: 0, update: 1, delete: 0 },
      }),
    );
    const onImport = vi.fn().mockResolvedValue(
      createImportResult({
        routing_policies: { added: 0, updated: 0, deleted: 1 },
      }),
    );

    render(
      <ConfigImportModal
        isOpen={true}
        onClose={vi.fn()}
        onPreview={onPreview}
        onImport={onImport}
        importing={false}
      />,
    );

    await uploadConfigFile();
    fireEvent.click(screen.getByRole("button", { name: "预览变更" }));

    expect(await screen.findByText("Routing Policies")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "确认导入" }));

    await screen.findByText("导入成功");
    expect(screen.getByText("Routing Policies")).toBeInTheDocument();
    expect(screen.getByText("-1 删除")).toBeInTheDocument();
  });

  it("hides routing-policy rows in scoped result summaries", async () => {
    const onPreview = vi.fn().mockResolvedValue(
      createPreviewResponse({
        providers: { add: 0, update: 1, delete: 0 },
        groups: { add: 0, update: 1, delete: 0 },
      }),
    );
    const onImport = vi.fn().mockResolvedValue(
      createImportResult({
        providers: { added: 0, updated: 1, deleted: 0 },
        groups: { added: 0, updated: 1, deleted: 0 },
        routing_policies: { added: 0, updated: 1, deleted: 0 },
      }),
    );

    render(
      <ConfigImportModal
        isOpen={true}
        onClose={vi.fn()}
        onPreview={onPreview}
        onImport={onImport}
        importing={false}
      />,
    );

    await uploadConfigFile();
    fireEvent.click(screen.getByText("按 Group / Provider 选择"));
    fireEvent.click(screen.getByText("Alpha Group"));
    fireEvent.click(screen.getByRole("button", { name: "预览变更" }));
    fireEvent.click(await screen.findByRole("button", { name: "确认导入" }));

    await screen.findByText("导入成功");
    expect(screen.queryByText("Routing Policies")).not.toBeInTheDocument();
    expect(screen.getByText("Providers")).toBeInTheDocument();
    expect(screen.getByText("Groups")).toBeInTheDocument();
  });
});
