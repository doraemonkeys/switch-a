import { describe, it, expect, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { ConfigForm } from "./ConfigForm";
import { ToastProvider } from "./Toast";
import { CONFIG_KEYS } from "../config";

function renderConfigForm(
  onSave: (config: Record<string, string>) => Promise<void> = vi.fn(),
  initialOverrides: Record<string, string> = {},
) {
  const initialConfig = {
    [CONFIG_KEYS.STICKY_MODE]: "model",
    [CONFIG_KEYS.STICKY_TTL]: "300",
    [CONFIG_KEYS.WEBSOCKET_PROBE_CLIENT_MODEL]: "true",
    ...initialOverrides,
  };

  const defaults = {
    [CONFIG_KEYS.STICKY_MODE]: "model",
    [CONFIG_KEYS.STICKY_TTL]: "300",
    [CONFIG_KEYS.WEBSOCKET_PROBE_CLIENT_MODEL]: "true",
  };

  return render(
    <ToastProvider>
      <ConfigForm
        initialConfig={initialConfig}
        defaults={defaults}
        onSave={onSave}
        saving={false}
      />
    </ToastProvider>,
  );
}

describe("ConfigForm", () => {
  it("defaults recovery to the original account and explains switching back", () => {
    renderConfigForm();
    expect(
      screen.getByRole("combobox", {
        name: /对话恢复策略/,
      }),
    ).toHaveValue("preserve_conversation");
    expect(
      screen.getByText("切回固定原账号后，已跨账号续聊的对话可能无法继续。"),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("option", { name: /client_decides/ }),
    ).not.toBeInTheDocument();
  });

  it("saves recovery independently of sticky mode and can switch back", async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    renderConfigForm(onSave, { [CONFIG_KEYS.STICKY_MODE]: "off" });
    const select = screen.getByRole("combobox", {
      name: /对话恢复策略/,
    });
    fireEvent.change(select, {
      target: { value: "switch_account_preserve_conversation" },
    });
    fireEvent.click(screen.getByRole("button", { name: /Save Changes/i }));
    await waitFor(() =>
      expect(onSave).toHaveBeenLastCalledWith(
        expect.objectContaining({
          [CONFIG_KEYS.CONVERSATION_RECOVERY_POLICY]:
            "switch_account_preserve_conversation",
          [CONFIG_KEYS.STICKY_MODE]: "off",
        }),
      ),
    );
    fireEvent.change(select, { target: { value: "preserve_conversation" } });
    fireEvent.click(screen.getByRole("button", { name: /Save Changes/i }));
    await waitFor(() =>
      expect(onSave).toHaveBeenLastCalledWith(
        expect.objectContaining({
          [CONFIG_KEYS.CONVERSATION_RECOVERY_POLICY]: "preserve_conversation",
        }),
      ),
    );
  });

  it("resets recovery to its saved policy", () => {
    renderConfigForm(vi.fn(), {
      [CONFIG_KEYS.CONVERSATION_RECOVERY_POLICY]:
        "switch_account_preserve_conversation",
    });
    const select = screen.getByRole("combobox", {
      name: /对话恢复策略/,
    });
    fireEvent.change(select, { target: { value: "preserve_conversation" } });
    fireEvent.click(screen.getByRole("button", { name: /Reset/i }));
    expect(select).toHaveValue("switch_account_preserve_conversation");
  });

  it("omits Codex rollout controls", () => {
    renderConfigForm();

    expect(
      screen.queryByRole("checkbox", { name: /^Upstream Header Hygiene/i }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("checkbox", { name: /^WebSocket Subprotocol/i }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("checkbox", {
        name: /^Continuity and Session Identity/i,
      }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("checkbox", { name: /^Provider Cookie Jar/i }),
    ).not.toBeInTheDocument();
  });

  it("defaults websocket probe control to checked", () => {
    renderConfigForm();

    expect(
      screen.getByLabelText(/Probe WebSocket Client Model Before Selection/i),
    ).toBeChecked();
  });

  it("submits websocket probe updates as string config values", async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    renderConfigForm(onSave);

    const checkbox = screen.getByLabelText(
      /Probe WebSocket Client Model Before Selection/i,
    );
    fireEvent.click(checkbox);

    await waitFor(() => {
      expect(
        screen.getByRole("button", { name: /Save Changes/i }),
      ).not.toBeDisabled();
    });

    fireEvent.click(screen.getByRole("button", { name: /Save Changes/i }));

    await waitFor(() => {
      expect(onSave).toHaveBeenCalledWith(
        expect.objectContaining({
          [CONFIG_KEYS.WEBSOCKET_PROBE_CLIENT_MODEL]: "false",
        }),
      );
    });
  });
});
