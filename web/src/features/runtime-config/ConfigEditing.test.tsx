import { describe, it, expect, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ConfigForm } from "../../components/ConfigForm";
import { ToastProvider } from "../../components/Toast";
import { CONFIG_KEYS as K } from "../../config";
import { effectiveConfig } from "./model";

const defaults = effectiveConfig({}, {});
function createForm(
  initial = defaults,
  onSave = vi.fn().mockResolvedValue(undefined),
) {
  const view = (values: Record<string, string>) => (
    <ToastProvider>
      <ConfigForm
        initialConfig={values}
        defaults={defaults}
        saving={false}
        onSave={onSave}
      />
    </ToastProvider>
  );
  return { ...render(view(initial)), view, onSave };
}
function category(name: string) {
  fireEvent.click(screen.getByRole("button", { name: new RegExp(name) }));
}
function search(query: string) {
  fireEvent.change(screen.getByRole("searchbox"), { target: { value: query } });
}

describe("configuration editing", () => {
  it("does not submit edits when confirming a search with Enter", async () => {
    const { onSave } = createForm();
    const user = userEvent.setup();
    fireEvent.change(screen.getByLabelText("粘性有效期"), {
      target: { value: "600" },
    });
    await user.type(screen.getByRole("searchbox"), "日志{Enter}");
    expect(screen.getByLabelText("日志保留时间")).toBeInTheDocument();
    expect(onSave).not.toHaveBeenCalled();
  });

  it("keeps drafts through category changes and equivalent server snapshots", () => {
    const { rerender, view } = createForm();
    fireEvent.change(screen.getByLabelText("粘性有效期"), {
      target: { value: "600" },
    });
    category("认证与账号");
    fireEvent.change(screen.getByLabelText("用户标识请求头"), {
      target: { value: "X-Test-User" },
    });
    rerender(view({ ...defaults }));
    category("路由与会话");
    expect(screen.getByLabelText("粘性有效期")).toHaveValue(600);
    expect(screen.getByText("2 项修改未保存")).toBeInTheDocument();
    category("认证与账号");
    expect(screen.getByLabelText("用户标识请求头")).toHaveValue("X-Test-User");
  });

  it("distinguishes custom saved values from unsaved edits and detects reverting a value", () => {
    createForm({ ...defaults, [K.STICKY_TTL]: "600" });
    expect(screen.getByText("自定义 · 默认：300 秒")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "保存修改" })).toBeDisabled();
    const ttl = screen.getByLabelText("粘性有效期");
    fireEvent.change(ttl, { target: { value: "900" } });
    expect(screen.getByText("1 项修改未保存")).toBeInTheDocument();
    fireEvent.change(ttl, { target: { value: "600" } });
    expect(screen.getByRole("button", { name: "保存修改" })).toBeDisabled();
  });

  it("searches across categories by key and filters unsaved values without discarding them", () => {
    createForm();
    search("first_byte_timeout");
    fireEvent.change(screen.getByLabelText("首字节超时"), {
      target: { value: "45" },
    });
    search("日志");
    expect(screen.getByLabelText("日志保留时间")).toBeInTheDocument();
    search("");
    fireEvent.click(screen.getByRole("button", { name: /仅看未保存/ }));
    expect(screen.getByLabelText("首字节超时")).toHaveValue(45);
    expect(screen.queryByLabelText("日志保留时间")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "撤销修改" }));
    expect(screen.getByText("没有待保存的修改")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "清除筛选" }));
    expect(screen.getByLabelText("粘性有效期")).toBeInTheDocument();
  });

  it("saves values across categories, preserves unexposed server keys, and establishes a new baseline", async () => {
    const { onSave } = createForm({
      ...defaults,
      server_extension: "preserved",
    });
    fireEvent.change(screen.getByLabelText("粘性有效期"), {
      target: { value: "600" },
    });
    category("代理与日志");
    fireEvent.change(screen.getByLabelText("日志保留时间"), {
      target: { value: "30" },
    });
    fireEvent.click(screen.getByRole("button", { name: "保存修改" }));
    await waitFor(() =>
      expect(onSave).toHaveBeenCalledWith(
        expect.objectContaining({
          [K.STICKY_TTL]: "600",
          [K.LOG_RETENTION_DAYS]: "30",
          server_extension: "preserved",
        }),
      ),
    );
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "保存修改" })).toBeDisabled(),
    );
    fireEvent.change(screen.getByLabelText("日志保留时间"), {
      target: { value: "90" },
    });
    fireEvent.click(screen.getByRole("button", { name: "撤销修改" }));
    expect(screen.getByLabelText("日志保留时间")).toHaveValue(30);
  });

  it("retains edited keys and adopts unrelated server updates", () => {
    const { rerender, view } = createForm();
    fireEvent.change(screen.getByLabelText("粘性有效期"), {
      target: { value: "600" },
    });
    rerender(view({ ...defaults, [K.LOG_RETENTION_DAYS]: "30" }));
    expect(screen.getByLabelText("粘性有效期")).toHaveValue(600);
    category("代理与日志");
    expect(screen.getByLabelText("日志保留时间")).toHaveValue(30);
    expect(screen.getByText("1 项修改未保存")).toBeInTheDocument();
  });

  it("reveals invalid values in hidden categories before saving", () => {
    const { onSave } = createForm();
    category("超时与重试");
    fireEvent.change(screen.getByLabelText("连接超时"), {
      target: { value: "" },
    });
    category("认证与账号");
    fireEvent.click(screen.getByRole("button", { name: "保存修改" }));
    expect(screen.getByLabelText("连接超时")).toHaveAttribute(
      "aria-invalid",
      "true",
    );
    expect(screen.getByText("请输入不小于 1 的整数")).toBeInTheDocument();
    expect(onSave).not.toHaveBeenCalled();
  });

  it("keeps the draft after save failure and permits retry", async () => {
    const onSave = vi
      .fn()
      .mockRejectedValueOnce(new Error("连接中断"))
      .mockResolvedValueOnce(undefined);
    createForm(defaults, onSave);
    fireEvent.change(screen.getByLabelText("粘性有效期"), {
      target: { value: "600" },
    });
    fireEvent.click(screen.getByRole("button", { name: "保存修改" }));
    await screen.findByText("连接中断");
    expect(screen.getByLabelText("粘性有效期")).toHaveValue(600);
    fireEvent.click(screen.getByRole("button", { name: "保存修改" }));
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "保存修改" })).toBeDisabled(),
    );
    expect(onSave).toHaveBeenCalledTimes(2);
  });

  it("disables editing while saving and prevents duplicate submissions", async () => {
    let resolveSave!: () => void;
    const onSave = vi.fn(
      () =>
        new Promise<void>((resolve) => {
          resolveSave = resolve;
        }),
    );
    createForm(defaults, onSave);
    fireEvent.change(screen.getByLabelText("粘性有效期"), {
      target: { value: "600" },
    });
    const form = screen.getByRole("form", { name: "运行配置" });
    fireEvent.submit(form);
    fireEvent.submit(form);
    expect(onSave).toHaveBeenCalledTimes(1);
    expect(screen.getByLabelText("粘性有效期")).toBeDisabled();
    resolveSave();
    await waitFor(() =>
      expect(screen.getByLabelText("粘性有效期")).toBeEnabled(),
    );
  });

  it("disables sticky TTL independently of conversation recovery", () => {
    createForm();
    fireEvent.change(screen.getByLabelText("粘性路由"), {
      target: { value: "off" },
    });
    expect(screen.getByLabelText("粘性有效期")).toBeDisabled();
    expect(screen.getByLabelText("对话恢复策略")).toBeEnabled();
  });
});
