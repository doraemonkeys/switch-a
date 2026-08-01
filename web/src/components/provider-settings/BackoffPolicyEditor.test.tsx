import { render, screen, within } from "@testing-library/react";
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
});
