import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { ReasoningBadge } from "./ReasoningBadge";

describe("ReasoningBadge", () => {
  it("shows pending observations while input is still arriving", () => {
    render(<ReasoningBadge observationState="pending" />);
    expect(screen.getByText("Pending")).toHaveAttribute(
      "title",
      "Requested reasoning observation is waiting for the complete input.",
    );
  });
  it("prefers effort and keeps every captured value in the exact title", () => {
    render(
      <ReasoningBadge
        observationState="captured"
        effort=" high "
        mode="enabled"
        budgetTokens={4096}
      />,
    );

    expect(
      screen.getByTitle(
        'Captured requested reasoning configuration. Effort: " high "; Thinking mode: "enabled"; Thinking budget: 4096 tokens.',
      ),
    ).toHaveTextContent("high");
  });

  it("falls back from effort to mode and then budget", () => {
    const { rerender } = render(
      <ReasoningBadge observationState="captured" mode="adaptive" />,
    );
    expect(screen.getByText("adaptive")).toBeInTheDocument();

    rerender(
      <ReasoningBadge observationState="captured" budgetTokens={2048} />,
    );
    expect(screen.getByText("2048 tokens")).toBeInTheDocument();
  });

  it("shows unknown future effort values without severity mapping", () => {
    render(
      <ReasoningBadge observationState="captured" effort="provider-future" />,
    );

    const badge = screen.getByText("provider-future");
    expect(badge).toHaveClass("bg-gray-100");
    expect(badge).not.toHaveClass("bg-yellow-100");
  });

  it("renders an empty string explicitly", () => {
    render(<ReasoningBadge observationState="captured" effort="" />);

    expect(screen.getByText('""')).toHaveAttribute(
      "title",
      'Captured requested reasoning configuration. Effort: "".',
    );
  });

  it("explains absent, unsupported, and legacy observations separately", () => {
    const { rerender } = render(<ReasoningBadge observationState="absent" />);
    expect(
      screen.getByTitle("No supported reasoning configuration was requested."),
    ).toHaveTextContent("—");

    rerender(<ReasoningBadge observationState="unsupported" />);
    expect(
      screen.getByTitle(
        "Requested reasoning configuration is not observed for this API type, endpoint, or transport.",
      ),
    ).toHaveTextContent("—");

    rerender(<ReasoningBadge observationState={null} />);
    expect(
      screen.getByTitle(
        "Reasoning observation is unavailable for this legacy log.",
      ),
    ).toHaveTextContent("—");
  });

  it("uses warning styling for invalid and ambiguous observations", () => {
    const { rerender } = render(
      <ReasoningBadge observationState="invalid" mode="enabled" />,
    );
    expect(
      screen.getByTitle(
        'Invalid requested reasoning configuration: at least one field could not be observed. Thinking mode: "enabled".',
      ),
    ).toHaveClass("bg-yellow-100");

    rerender(<ReasoningBadge observationState="ambiguous" effort="medium" />);
    expect(
      screen.getByTitle(
        'Ambiguous requested reasoning configuration: duplicate fields were present; showing the last successfully decoded values. Effort: "medium".',
      ),
    ).toHaveClass("bg-yellow-100");
  });
});
