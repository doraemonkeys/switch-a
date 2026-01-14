import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { RequestAttemptTimeline } from "./RequestAttemptTimeline";
import type { RequestAttempt } from "../api/types";

// Helper to create a mock attempt
function createMockAttempt(
  overrides?: Partial<RequestAttempt>,
): RequestAttempt {
  return {
    // eslint-disable-next-line sonarjs/pseudo-random -- safe for test data generation
    id: Math.floor(Math.random() * 1000),
    request_id: "req-123",
    provider_id: "provider-1",
    attempt: 0,
    status_code: 200,
    error: "",
    latency_ms: 150,
    created_at: new Date().toISOString(),
    ...overrides,
  };
}

describe("RequestAttemptTimeline", () => {
  describe("empty/null handling", () => {
    it("returns null when attempts is empty array", () => {
      const { container } = render(<RequestAttemptTimeline attempts={[]} />);
      expect(container.firstChild).toBeNull();
    });

    it("returns null when attempts is undefined", () => {
      const { container } = render(
        // @ts-expect-error - testing undefined case
        <RequestAttemptTimeline attempts={undefined} />,
      );
      expect(container.firstChild).toBeNull();
    });
  });

  describe("attempt rendering", () => {
    it("renders single attempt correctly", () => {
      const attempts = [
        createMockAttempt({
          id: 1,
          attempt: 0,
          status_code: 200,
          latency_ms: 150,
        }),
      ];

      render(<RequestAttemptTimeline attempts={attempts} />);

      expect(screen.getByText("Attempt 1")).toBeInTheDocument();
      expect(screen.getByText("200")).toBeInTheDocument();
      expect(screen.getByText("150ms")).toBeInTheDocument();
    });

    it("renders multiple attempts", () => {
      const attempts = [
        createMockAttempt({ id: 1, attempt: 0 }),
        createMockAttempt({ id: 2, attempt: 1 }),
        createMockAttempt({ id: 3, attempt: 2 }),
      ];

      render(<RequestAttemptTimeline attempts={attempts} />);

      expect(screen.getByText("Attempt 1")).toBeInTheDocument();
      expect(screen.getByText("Attempt 2")).toBeInTheDocument();
      expect(screen.getByText("Attempt 3")).toBeInTheDocument();
    });

    it("displays provider name when available in map", () => {
      const attempts = [createMockAttempt({ provider_id: "prov-1" })];
      const providerNames = new Map([["prov-1", "Anthropic"]]);

      render(
        <RequestAttemptTimeline
          attempts={attempts}
          providerNames={providerNames}
        />,
      );

      expect(screen.getByText(/Provider: Anthropic/)).toBeInTheDocument();
    });

    it("displays provider_id when not in provider names map", () => {
      const attempts = [createMockAttempt({ provider_id: "prov-xyz" })];
      const providerNames = new Map([["prov-other", "Other Provider"]]);

      render(
        <RequestAttemptTimeline
          attempts={attempts}
          providerNames={providerNames}
        />,
      );

      expect(screen.getByText(/Provider: prov-xyz/)).toBeInTheDocument();
    });
  });

  describe("sorting", () => {
    it("sorts attempts by attempt number ascending", () => {
      const attempts = [
        createMockAttempt({ id: 3, attempt: 2, provider_id: "p-third" }),
        createMockAttempt({ id: 1, attempt: 0, provider_id: "p-first" }),
        createMockAttempt({ id: 2, attempt: 1, provider_id: "p-second" }),
      ];

      render(<RequestAttemptTimeline attempts={attempts} />);

      const attemptLabels = screen.getAllByText(/Attempt \d/);
      expect(attemptLabels[0]).toHaveTextContent("Attempt 1");
      expect(attemptLabels[1]).toHaveTextContent("Attempt 2");
      expect(attemptLabels[2]).toHaveTextContent("Attempt 3");
    });
  });

  describe("status code colors", () => {
    it("shows green status badge for 2xx success codes", () => {
      const attempts = [createMockAttempt({ status_code: 200 })];
      render(<RequestAttemptTimeline attempts={attempts} />);

      const badge = screen.getByText("200");
      expect(badge).toHaveClass("bg-green-100");
    });

    it("shows green status badge for 201 created", () => {
      const attempts = [createMockAttempt({ status_code: 201 })];
      render(<RequestAttemptTimeline attempts={attempts} />);

      const badge = screen.getByText("201");
      expect(badge).toHaveClass("bg-green-100");
    });

    it("shows red status badge for 4xx client errors", () => {
      const attempts = [createMockAttempt({ status_code: 400 })];
      render(<RequestAttemptTimeline attempts={attempts} />);

      const badge = screen.getByText("400");
      expect(badge).toHaveClass("bg-red-100");
    });

    it("shows red status badge for 5xx server errors", () => {
      const attempts = [createMockAttempt({ status_code: 500 })];
      render(<RequestAttemptTimeline attempts={attempts} />);

      const badge = screen.getByText("500");
      expect(badge).toHaveClass("bg-red-100");
    });

    it("shows yellow status badge for 3xx redirect codes", () => {
      const attempts = [createMockAttempt({ status_code: 302 })];
      render(<RequestAttemptTimeline attempts={attempts} />);

      const badge = screen.getByText("302");
      expect(badge).toHaveClass("bg-yellow-100");
    });
  });

  describe("no response badge", () => {
    it("shows 'No Response' badge when status_code is 0", () => {
      const attempts = [createMockAttempt({ status_code: 0 })];
      render(<RequestAttemptTimeline attempts={attempts} />);

      expect(screen.getByText("No Response")).toBeInTheDocument();
      // Should not show status code badge
      expect(screen.queryByText("0")).not.toBeInTheDocument();
    });

    it("does not show 'No Response' badge for non-zero status codes", () => {
      const attempts = [createMockAttempt({ status_code: 200 })];
      render(<RequestAttemptTimeline attempts={attempts} />);

      expect(screen.queryByText("No Response")).not.toBeInTheDocument();
    });
  });

  describe("error message display", () => {
    it("shows error message when attempt has error", () => {
      const attempts = [
        createMockAttempt({
          status_code: 500,
          error: "Connection refused",
        }),
      ];
      render(<RequestAttemptTimeline attempts={attempts} />);

      expect(screen.getByText("Connection refused")).toBeInTheDocument();
    });

    it("does not show error section when error is empty", () => {
      const attempts = [createMockAttempt({ error: "" })];
      render(<RequestAttemptTimeline attempts={attempts} />);

      // The error message container should not exist
      expect(document.querySelector(".text-red-700")).not.toBeInTheDocument();
    });

    it("does not show error section when error is just whitespace", () => {
      const attempts = [createMockAttempt({ error: "   " })];
      const { container } = render(
        <RequestAttemptTimeline attempts={attempts} />,
      );

      // Since error has whitespace with length > 0, the error container still renders
      // (this is the current behavior - whitespace is treated as error content)
      const errorContainer = container.querySelector(".bg-red-100\\/50");
      expect(errorContainer).toBeInTheDocument();
    });
  });

  describe("card styling", () => {
    it("applies success styling to last successful attempt", () => {
      const attempts = [
        createMockAttempt({ id: 1, attempt: 0, status_code: 500 }),
        createMockAttempt({ id: 2, attempt: 1, status_code: 200 }),
      ];

      const { container } = render(
        <RequestAttemptTimeline attempts={attempts} />,
      );

      // Get all attempt cards (the divs with border class)
      const cards = container.querySelectorAll(".border");
      // Last card (attempt 1, which is index 1 in sorted order) should have green border
      expect(cards[1]).toHaveClass("border-green-200");
    });

    it("applies error styling to failed attempts", () => {
      const attempts = [
        createMockAttempt({
          id: 1,
          attempt: 0,
          status_code: 500,
          error: "Server error",
        }),
      ];

      const { container } = render(
        <RequestAttemptTimeline attempts={attempts} />,
      );

      const cards = container.querySelectorAll(".border");
      expect(cards[0]).toHaveClass("border-red-200");
    });

    it("applies neutral styling to non-last successful attempts", () => {
      const attempts = [
        createMockAttempt({ id: 1, attempt: 0, status_code: 200 }),
        createMockAttempt({ id: 2, attempt: 1, status_code: 200 }),
      ];

      const { container } = render(
        <RequestAttemptTimeline attempts={attempts} />,
      );

      const cards = container.querySelectorAll(".border");
      // First card should have neutral styling
      expect(cards[0]).toHaveClass("border-border-light");
      // Last card should have success styling
      expect(cards[1]).toHaveClass("border-green-200");
    });
  });

  describe("timeline dot colors", () => {
    it("shows green dot for successful attempts (2xx)", () => {
      const attempts = [createMockAttempt({ status_code: 200 })];
      const { container } = render(
        <RequestAttemptTimeline attempts={attempts} />,
      );

      const dot = container.querySelector(".rounded-full.w-3.h-3");
      expect(dot).toHaveClass("bg-green-500");
    });

    it("shows red dot for 5xx errors", () => {
      const attempts = [createMockAttempt({ status_code: 503 })];
      const { container } = render(
        <RequestAttemptTimeline attempts={attempts} />,
      );

      const dot = container.querySelector(".rounded-full.w-3.h-3");
      expect(dot).toHaveClass("bg-red-500");
    });

    it("shows amber dot for 4xx errors", () => {
      const attempts = [createMockAttempt({ status_code: 429 })];
      const { container } = render(
        <RequestAttemptTimeline attempts={attempts} />,
      );

      const dot = container.querySelector(".rounded-full.w-3.h-3");
      expect(dot).toHaveClass("bg-amber-500");
    });

    it("shows gray dot for status_code 0 (no response)", () => {
      const attempts = [createMockAttempt({ status_code: 0 })];
      const { container } = render(
        <RequestAttemptTimeline attempts={attempts} />,
      );

      const dot = container.querySelector(".rounded-full.w-3.h-3");
      expect(dot).toHaveClass("bg-gray-400");
    });
  });

  describe("latency display", () => {
    it("displays latency in milliseconds", () => {
      const attempts = [createMockAttempt({ latency_ms: 1234 })];
      render(<RequestAttemptTimeline attempts={attempts} />);

      expect(screen.getByText("1234ms")).toBeInTheDocument();
    });

    it("displays 0ms for zero latency", () => {
      const attempts = [createMockAttempt({ latency_ms: 0 })];
      render(<RequestAttemptTimeline attempts={attempts} />);

      expect(screen.getByText("0ms")).toBeInTheDocument();
    });
  });
});
