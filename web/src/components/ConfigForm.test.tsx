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
  it("renders the four Codex feature controls with safe dependency affordances", () => {
    renderConfigForm();

    expect(
      screen.getByRole("checkbox", { name: /^Upstream Header Hygiene/i }),
    ).not.toBeChecked();
    expect(
      screen.getByRole("checkbox", { name: /^WebSocket Subprotocol/i }),
    ).not.toBeChecked();
    expect(
      screen.getByRole("checkbox", {
        name: /^Continuity and Session Identity/i,
      }),
    ).toBeDisabled();
    expect(
      screen.getByRole("checkbox", { name: /^Provider Cookie Jar/i }),
    ).toBeDisabled();
  });

  it("maps independent and dependent Codex feature changes to backend keys", async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    renderConfigForm(onSave);

    fireEvent.click(
      screen.getByRole("checkbox", { name: /^WebSocket Subprotocol/i }),
    );
    fireEvent.click(
      screen.getByRole("checkbox", { name: /^Upstream Header Hygiene/i }),
    );
    fireEvent.click(
      screen.getByRole("checkbox", {
        name: /^Continuity and Session Identity/i,
      }),
    );

    expect(
      screen.getByRole("checkbox", { name: /^Upstream Header Hygiene/i }),
    ).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: /Save Changes/i }));

    await waitFor(() => {
      expect(onSave).toHaveBeenCalledWith(
        expect.objectContaining({
          [CONFIG_KEYS.CODEX_UPSTREAM_HEADER_HYGIENE]: "true",
          [CONFIG_KEYS.CODEX_WEBSOCKET_SUBPROTOCOL]: "true",
          [CONFIG_KEYS.CODEX_CONTINUITY]: "true",
        }),
      );
    });
  });

  it.each(["1", "TRUE"])(
    "renders backend boolean alias %s as enabled",
    (value) => {
      renderConfigForm(undefined, {
        [CONFIG_KEYS.CODEX_UPSTREAM_HEADER_HYGIENE]: value,
      });

      expect(
        screen.getByRole("checkbox", { name: /^Upstream Header Hygiene/i }),
      ).toBeChecked();
      expect(
        screen.getByRole("checkbox", {
          name: /^Continuity and Session Identity/i,
        }),
      ).not.toBeDisabled();
    },
  );

  it("keeps an invalid durable dependency state repairable", () => {
    renderConfigForm(undefined, {
      [CONFIG_KEYS.CODEX_UPSTREAM_HEADER_HYGIENE]: "false",
      [CONFIG_KEYS.CODEX_CONTINUITY]: "true",
    });

    expect(
      screen.getByRole("checkbox", { name: /^Upstream Header Hygiene/i }),
    ).not.toBeDisabled();
    expect(
      screen.getByRole("checkbox", {
        name: /^Continuity and Session Identity/i,
      }),
    ).not.toBeDisabled();
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
