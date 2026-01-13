import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { RefreshIntervalSelect } from "./RefreshIntervalSelect";
import { REFRESH_INTERVALS } from "./refreshIntervalConstants";

describe("RefreshIntervalSelect", () => {
  it("renders select with current value", () => {
    render(<RefreshIntervalSelect value={5000} onChange={vi.fn()} />);
    const select = screen.getByRole("combobox");
    expect(select).toHaveValue("5000");
  });

  it("renders all refresh interval options", () => {
    render(<RefreshIntervalSelect value={0} onChange={vi.fn()} />);
    const options = screen.getAllByRole("option");
    expect(options).toHaveLength(REFRESH_INTERVALS.length);
  });

  it("calls onChange with number value when selection changes", () => {
    const onChange = vi.fn();
    render(<RefreshIntervalSelect value={0} onChange={onChange} />);

    const select = screen.getByRole("combobox");
    fireEvent.change(select, { target: { value: "10000" } });

    expect(onChange).toHaveBeenCalledWith(10000);
  });

  it("applies custom className", () => {
    render(
      <RefreshIntervalSelect
        value={0}
        onChange={vi.fn()}
        className="custom-class"
      />,
    );
    const select = screen.getByRole("combobox");
    expect(select).toHaveClass("custom-class");
  });

  it("shows 'Auto-refresh: Off' label when showLabel is true and value is 0", () => {
    render(
      <RefreshIntervalSelect value={0} onChange={vi.fn()} showLabel={true} />,
    );
    expect(screen.getByText("Auto-refresh: Off")).toBeInTheDocument();
  });

  it("shows Auto-refresh: Off in options when showLabel is true", () => {
    render(
      <RefreshIntervalSelect
        value={5000}
        onChange={vi.fn()}
        showLabel={true}
      />,
    );
    // Even when value is 5000, the option for 0 should show "Auto-refresh: Off"
    expect(screen.getByText("Auto-refresh: Off")).toBeInTheDocument();
    // But the selected value should be 5000
    const select = screen.getByRole("combobox");
    expect(select).toHaveValue("5000");
  });

  it("shows normal label when showLabel is false", () => {
    render(
      <RefreshIntervalSelect value={0} onChange={vi.fn()} showLabel={false} />,
    );
    // The option for 0 should show its normal label, not "Auto-refresh: Off"
    expect(screen.queryByText("Auto-refresh: Off")).not.toBeInTheDocument();
  });
});
