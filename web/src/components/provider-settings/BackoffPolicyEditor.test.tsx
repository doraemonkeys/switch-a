import { fireEvent, render, screen, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { BackoffPolicyEditor } from "./BackoffPolicyEditor";

const DEFAULT_BACKOFF = {
  initial_delay: "100ms",
  max_delay: "5s",
  multiplier: 2,
  jitter: false,
};

describe("BackoffPolicyEditor", () => {
  it("bounds the visual preview even when an invalid retry count is typed", () => {
    render(
      <BackoffPolicyEditor
        backoff={DEFAULT_BACKOFF}
        maxRetries={1_000_000}
        expanded
        onToggle={vi.fn()}
        onChange={vi.fn()}
      />,
    );

    const preview = screen.getByRole("list", { name: "Retry delay preview" });
    expect(within(preview).getAllByRole("listitem")).toHaveLength(10);
  });

  it("replaces misleading calculations with guidance while timing is invalid", () => {
    render(
      <BackoffPolicyEditor
        backoff={{ ...DEFAULT_BACKOFF, initial_delay: "later" }}
        maxRetries={3}
        expanded
        onToggle={vi.fn()}
        onChange={vi.fn()}
      />,
    );

    expect(screen.getByRole("status")).toHaveTextContent(
      "Enter valid retry timing values to see the delay preview.",
    );
    expect(
      screen.queryByRole("list", { name: "Retry delay preview" }),
    ).not.toBeInTheDocument();
  });

  it("treats multiplier zero as the backend default", () => {
    render(
      <BackoffPolicyEditor
        backoff={{ ...DEFAULT_BACKOFF, multiplier: 0 }}
        maxRetries={3}
        expanded
        onToggle={vi.fn()}
        onChange={vi.fn()}
      />,
    );

    const multiplier = screen.getByRole("spinbutton", { name: "Multiplier" });
    expect(multiplier).toHaveAttribute("min", "0");
    expect(multiplier).not.toHaveAttribute("max");
    expect(screen.queryByText("Custom")).not.toBeInTheDocument();
    expect(
      within(
        screen.getByRole("list", { name: "Retry delay preview" }),
      ).getAllByRole("listitem"),
    ).toHaveLength(3);
    expect(screen.getByText("400ms")).toBeInTheDocument();
  });

  it("emits an explicit zero multiplier without substituting it in the draft", () => {
    const onChange = vi.fn();
    render(
      <BackoffPolicyEditor
        backoff={DEFAULT_BACKOFF}
        maxRetries={2}
        expanded
        onToggle={vi.fn()}
        onChange={onChange}
      />,
    );

    const multiplier = screen.getByRole("spinbutton", { name: "Multiplier" });
    fireEvent.change(multiplier, {
      target: { value: "0", valueAsNumber: 0 },
    });

    expect(onChange).toHaveBeenLastCalledWith({
      ...DEFAULT_BACKOFF,
      multiplier: 0,
    });
  });
});
