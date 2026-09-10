import { describe, it, expect, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { Config } from "../../pages/Config";
import { ToastProvider } from "../../components/Toast";
import { useConfig } from "../../hooks/useConfig";
import { effectiveConfig } from "./model";

vi.mock("../../hooks/useConfig", () => ({ useConfig: vi.fn() }));
vi.mock("../../hooks/useConfigExport", () => ({
  useConfigExport: () => ({ exporting: false }),
}));
vi.mock("../../components/ConfigImportModal", () => ({
  ConfigImportModal: () => null,
}));

function queryResult() {
  const defaults = effectiveConfig({}, {});
  return {
    defaults,
    values: {},
    config: defaults,
    loading: false,
    saving: false,
    error: null,
    refetch: vi.fn(),
    updateConfig: vi.fn(),
    isModified: vi.fn(),
  };
}
const page = () => (
  <ToastProvider>
    <Config />
  </ToastProvider>
);

describe("configuration page refresh", () => {
  it("keeps the active category and draft mounted while reloading the server snapshot", () => {
    const query = queryResult();
    vi.mocked(useConfig).mockReturnValue(query);
    const { rerender } = render(page());
    fireEvent.click(screen.getByRole("button", { name: /超时与重试/ }));
    fireEvent.change(screen.getByLabelText("连接超时"), {
      target: { value: "45" },
    });
    vi.mocked(useConfig).mockReturnValue({ ...query, loading: true });
    rerender(page());
    expect(screen.getByLabelText("连接超时")).toHaveValue(45);
    vi.mocked(useConfig).mockReturnValue({
      ...query,
      config: { ...query.config },
    });
    rerender(page());
    expect(screen.getByLabelText("连接超时")).toHaveValue(45);
    expect(screen.getByText("1 项修改未保存")).toBeInTheDocument();
  });

  it("offers a retry instead of editing fallback values when the first load fails", () => {
    const query = {
      ...queryResult(),
      config: {},
      defaults: {},
      error: new Error("加载失败"),
    };
    vi.mocked(useConfig).mockReturnValue(query);
    render(page());
    expect(screen.getByRole("alert")).toHaveTextContent("加载失败");
    expect(screen.queryByRole("form")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "重新加载" }));
    expect(query.refetch).toHaveBeenCalledOnce();
  });
});
