import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ConfigImportModal } from "../ConfigImportModal";
import type {
  ExportedConfig,
  ImportPreviewResponse,
  ImportResult,
} from "@/api/types";
import { buildImportRequest, hasVisibleChanges } from "./helpers";
import { PreviewStep } from "./PreviewStep";
import { ResultStep } from "./ResultStep";

const zero = { add: 0, update: 0, delete: 0, unchanged: 0 };
const preview: ImportPreviewResponse = {
  dry_run: true,
  changes: {
    providers: zero,
    credential_sessions: zero,
    groups: zero,
    routing_policies: zero,
    settings: zero,
    internal_error_rules: zero,
    client_api_keys: zero,
    client_api_key_policy: { from: "permissive", to: "restricted" },
  },
  warnings: [],
  credential_reauthentication_requirements: [],
  rule_set_revision: "0",
  rule_set_etag: "etag",
};
const config: ExportedConfig = {
  version: "5.0",
  exported_at: "",
  providers: [],
  credential_sessions: [],
  groups: [],
  routing_policies: [],
  settings: {},
  internal_error_rules: [],
  client_api_keys: { mode: "restricted", keys: [] },
};

describe("client API key backup UI", () => {
  it("ignores malformed client key aggregates for scoped imports but validates full import", async () => {
    const onPreview = vi.fn().mockResolvedValue(preview);
    const { container } = render(
      <ConfigImportModal
        isOpen
        onClose={vi.fn()}
        onPreview={onPreview}
        onImport={vi.fn()}
        importing={false}
      />,
    );
    const file = new File(["ignored"], "config.json", {
      type: "application/json",
    });
    Object.defineProperty(file, "text", {
      value: vi.fn().mockResolvedValue(
        JSON.stringify({
          ...config,
          client_api_keys: { mode: "invalid-mode", keys: [] },
        }),
      ),
    });
    fireEvent.change(container.querySelector('input[type="file"]')!, {
      target: { files: [file] },
    });
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "预览变更" })).toBeEnabled(),
    );
    fireEvent.click(screen.getByRole("button", { name: "预览变更" }));
    expect(
      await screen.findByText("Invalid client access mode"),
    ).toBeInTheDocument();
    expect(onPreview).not.toHaveBeenCalled();
    fireEvent.click(screen.getByText("仅导入 Settings"));
    fireEvent.click(screen.getByRole("button", { name: "预览变更" }));
    await waitFor(() => expect(onPreview).toHaveBeenCalledTimes(1));
    expect(onPreview.mock.calls[0][0]).not.toHaveProperty("client_api_keys");
    expect(onPreview.mock.calls[0][0].import_scope.mode).toBe("settings_only");
  });

  it("preserves an explicit empty aggregate only for full import", () => {
    expect(
      buildImportRequest(config, { mode: "full" }).client_api_keys,
    ).toEqual(config.client_api_keys);
    expect(
      buildImportRequest(config, { mode: "settings_only" }),
    ).not.toHaveProperty("client_api_keys");
    expect(
      buildImportRequest(config, {
        mode: "selection",
        selection: { group_ids: [], provider_ids: [] },
      }),
    ).not.toHaveProperty("client_api_keys");
    expect(
      buildImportRequest(
        { ...config, client_api_keys: undefined },
        { mode: "full" },
      ),
    ).not.toHaveProperty("client_api_keys");
    expect(
      buildImportRequest(
        { ...config, client_api_keys: null },
        { mode: "full" },
      ),
    ).not.toHaveProperty("client_api_keys");
  });

  it("enables policy-only changes only in full scope", () => {
    expect(hasVisibleChanges(preview, "full")).toBe(true);
    expect(hasVisibleChanges(preview, "selection")).toBe(false);
    expect(hasVisibleChanges(preview, "settings_only")).toBe(false);
    expect(
      hasVisibleChanges(
        {
          ...preview,
          changes: {
            ...preview.changes,
            client_api_key_policy: { from: "restricted", to: "restricted" },
          },
        },
        "full",
      ),
    ).toBe(false);
    expect(
      hasVisibleChanges(
        {
          ...preview,
          changes: {
            ...preview.changes,
            client_api_key_policy: undefined,
            client_api_keys: { ...zero, add: 1 },
          },
        },
        "full",
      ),
    ).toBe(true);
  });

  it("shows policy transition and empty restricted consequence in preview", () => {
    render(
      <PreviewStep
        selectedFile={new File(["{}"], "backup.json")}
        preview={preview}
        mode="full"
        hasAnyChanges
        importing={false}
        onBackToSelect={() => undefined}
      />,
    );
    expect(
      screen.getByText("Allow any key → Configured keys only"),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/All new client requests will be blocked/),
    ).toBeInTheDocument();
    expect(screen.getByText("Client API Keys")).toBeInTheDocument();
  });

  it("states preservation for scoped imports and reports applied policy separately", () => {
    const { unmount } = render(
      <PreviewStep
        selectedFile={new File(["{}"], "backup.json")}
        preview={preview}
        mode="settings_only"
        hasAnyChanges={false}
        importing={false}
        onBackToSelect={() => undefined}
      />,
    );
    expect(
      screen.getByText("Existing client keys and access policy are preserved."),
    ).toBeInTheDocument();
    expect(
      screen.queryByText(/All new client requests will be blocked/),
    ).not.toBeInTheDocument();
    unmount();
    const counts = { added: 0, updated: 0, deleted: 0 };
    const result: ImportResult = {
      success: true,
      applied: {
        providers: counts,
        credential_sessions: counts,
        groups: counts,
        routing_policies: counts,
        settings: counts,
        internal_error_rules: counts,
        client_api_keys: counts,
        client_api_key_policy: { from: "permissive", to: "restricted" },
      },
      credential_reauthentication_requirements: [],
      rule_set_revision: "0",
      rule_set_etag: "etag",
    };
    render(<ResultStep result={result} mode="full" />);
    expect(
      screen.getByText("Allow any key → Configured keys only"),
    ).toBeInTheDocument();
  });
});
