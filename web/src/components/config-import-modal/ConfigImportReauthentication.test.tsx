import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { ImportPreviewResponse, ImportResult } from "../../api/types";
import { PreviewStep } from "./PreviewStep";
import { ResultStep } from "./ResultStep";

const requirement = {
  credential_session_id: "chatgpt-session-alpha",
  name: "ChatGPT Alpha",
};
const previewCount = { add: 0, update: 0, delete: 0, unchanged: 0 };
const appliedCount = { added: 0, updated: 0, deleted: 0 };

const preview: ImportPreviewResponse = {
  dry_run: true,
  changes: {
    providers: previewCount,
    credential_sessions: previewCount,
    groups: previewCount,
    routing_policies: previewCount,
    settings: previewCount,
    internal_error_rules: previewCount,
  },
  warnings: [],
  credential_reauthentication_requirements: [requirement],
  rule_set_revision: "0",
  rule_set_etag: '"internal-error-rules/0"',
};

const result: ImportResult = {
  success: true,
  applied: {
    providers: appliedCount,
    credential_sessions: appliedCount,
    groups: appliedCount,
    routing_policies: appliedCount,
    settings: appliedCount,
    internal_error_rules: appliedCount,
  },
  credential_reauthentication_requirements: [requirement],
  rule_set_revision: "0",
  rule_set_etag: '"internal-error-rules/0"',
};

describe("ChatGPT config import recovery guidance", () => {
  it("explains the required reconnect before import", () => {
    render(
      <PreviewStep
        selectedFile={new File(["{}"], "config.json")}
        preview={preview}
        mode="full"
        hasAnyChanges={true}
        importing={false}
        onBackToSelect={() => undefined}
      />,
    );

    expect(
      screen.getByText("1 个 ChatGPT 登录需要重新认证"),
    ).toBeInTheDocument();
    expect(screen.getByText("ChatGPT Alpha")).toBeInTheDocument();
    expect(screen.getByText(/配置可以正常导入/)).toBeInTheDocument();
  });

  it("does not claim that an unrestored login is already effective", () => {
    render(<ResultStep result={result} mode="full" />);

    expect(
      screen.getByText(/ChatGPT Provider 将在连接后生效/),
    ).toBeInTheDocument();
    expect(screen.getByText(/Provider 配置已经恢复/)).toBeInTheDocument();
  });
});
