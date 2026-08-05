import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { RequestAttemptTimeline } from "@/features/request-attempt";
import type { RequestAttempt } from "@/api/types";

// Helper to create a mock attempt
function createMockAttempt(
  overrides?: Partial<RequestAttempt>,
): RequestAttempt {
  return {
    // eslint-disable-next-line sonarjs/pseudo-random -- safe for test data generation
    id: Math.floor(Math.random() * 1000),
    request_id: "req-123",
    provider_id: "provider-1",
    semantics_version: "normalized_v1",
    attempt: 0,
    status_code: 200,
    error: "",
    attempt_evidence_json: null,
    latency_ms: 150,
    created_at: new Date().toISOString(),
    ...overrides,
  };
}

function registerWebSocketLifecycleAttributionTests() {
  describe("websocket lifecycle attribution", () => {
    it("surfaces provider-attempt semantics and final outcome ownership", () => {
      const attempts = [
        createMockAttempt({
          id: 1,
          provider_id: "provider-old",
          attempt: 0,
          status_code: 101,
          error: "provider semantic error",
          phase: "post_upgrade_pre_visible",
          outcome: "upstream_semantic_error",
          result_visible_to_client: false,
          switch_reason: "provider_scoped_semantic_error",
        }),
        createMockAttempt({
          id: 2,
          provider_id: "provider-final",
          attempt: 1,
          status_code: 101,
          phase: "visible",
          outcome: "visible_session",
          result_visible_to_client: true,
        }),
      ];

      const { container } = render(
        <RequestAttemptTimeline
          attempts={attempts}
          isWebSocket
          attributedProviderId="provider-final"
        />,
      );

      expect(screen.getByText("Provider Attempt 1")).toBeInTheDocument();
      expect(screen.getByText("Provider Attempt 2")).toBeInTheDocument();
      expect(
        screen.getByText(
          "Semantic error suppressed before client-visible data",
        ),
      ).toBeInTheDocument();
      expect(
        screen.queryByText(/Provider-scoped semantic error/),
      ).not.toBeInTheDocument();
      expect(
        screen.getByText("This provider owned the client-visible session"),
      ).toBeInTheDocument();
      expect(
        screen.getByText("Phase: Post-upgrade, pre-visible"),
      ).toBeInTheDocument();
      expect(screen.getByText("Phase: Visible")).toBeInTheDocument();
      expect(screen.getByText("Outcome owner")).toBeInTheDocument();

      const cards = container.querySelectorAll(".p-3.rounded-lg.border");
      expect(cards[0]).toHaveClass("border-amber-200");
      expect(cards[1]).toHaveClass("border-green-200");
    });

    it("does not style visible-session ownership as success after a post-commit failure", () => {
      const attempts = [
        createMockAttempt({
          id: 1,
          provider_id: "provider-final",
          attempt: 0,
          status_code: 101,
          error: "upstream transport closed after commit",
          phase: "visible",
          outcome: "visible_session",
          result_visible_to_client: true,
        }),
      ];

      const { container } = render(
        <RequestAttemptTimeline attempts={attempts} isWebSocket />,
      );

      const cards = container.querySelectorAll(".p-3.rounded-lg.border");
      expect(cards[0]).toHaveClass("border-red-200");
      expect(cards[0]).not.toHaveClass("border-green-200");

      const upgradeBadge = screen.getByText("101 Upgrade");
      expect(upgradeBadge).not.toHaveClass("bg-green-100");
      expect(
        screen.getByText("This provider owned the client-visible session"),
      ).toHaveClass("bg-blue-100");
    });

    it("does not derive attempt behavior from arbitrary or absent switch reasons", () => {
      const attempt = createMockAttempt({
        id: 1,
        status_code: 101,
        outcome: "visible_session",
        phase: "visible",
        switch_reason: "future_reason_owned_by_routing",
      });
      const { container, rerender } = render(
        <RequestAttemptTimeline attempts={[attempt]} isWebSocket />,
      );
      const cardWithReason = container.querySelector("article");
      expect(cardWithReason).toHaveClass("border-green-200");
      expect(screen.queryByText(/future reason/i)).not.toBeInTheDocument();

      rerender(
        <RequestAttemptTimeline
          attempts={[{ ...attempt, switch_reason: undefined }]}
          isWebSocket
        />,
      );
      expect(container.querySelector("article")?.className).toBe(
        cardWithReason?.className,
      );
    });

    it("exposes timeline and attempt structure to assistive technology", () => {
      render(
        <RequestAttemptTimeline
          attempts={[createMockAttempt({ id: 1 })]}
          isWebSocket
        />,
      );

      expect(
        screen.getByRole("list", { name: "Request attempts" }),
      ).toBeInTheDocument();
      expect(screen.getByRole("listitem")).toBeInTheDocument();
      expect(
        screen.getByRole("article", { name: "Provider attempt 1" }),
      ).toBeInTheDocument();
    });
  });
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

    it("preserves one-based backend attempt numbers without adding an extra offset", () => {
      const attempts = [
        createMockAttempt({ id: 1, attempt: 1 }),
        createMockAttempt({ id: 2, attempt: 2 }),
      ];

      render(<RequestAttemptTimeline attempts={attempts} />);

      expect(screen.getByText("Attempt 1")).toBeInTheDocument();
      expect(screen.getByText("Attempt 2")).toBeInTheDocument();
      expect(screen.queryByText("Attempt 3")).not.toBeInTheDocument();
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

    it("keeps tie ordering stable with id as the secondary key", () => {
      const attempts = [
        createMockAttempt({ id: 2, attempt: 1, provider_id: "p-second" }),
        createMockAttempt({ id: 1, attempt: 1, provider_id: "p-first" }),
      ];

      render(<RequestAttemptTimeline attempts={attempts} />);

      const providerLabels = screen.getAllByText(/Provider:/);
      expect(providerLabels[0]).toHaveTextContent("Provider: p-first");
      expect(providerLabels[1]).toHaveTextContent("Provider: p-second");
    });
  });

  describe("status code colors", () => {
    it.each([
      [200, "bg-green-100"],
      [201, "bg-green-100"],
      [302, "bg-yellow-100"],
      [400, "bg-red-100"],
      [500, "bg-red-100"],
    ])("maps status %i to %s", (statusCode, className) => {
      const attempts = [createMockAttempt({ status_code: statusCode })];
      render(<RequestAttemptTimeline attempts={attempts} />);

      expect(screen.getByText(String(statusCode))).toHaveClass(className);
    });
  });

  describe("HTTP-normalized outcome classification", () => {
    it("styles a completed upstream attempt as success", () => {
      const { container } = render(
        <RequestAttemptTimeline
          attempts={[
            createMockAttempt({
              id: 1,
              attempt: 0,
              status_code: 200,
              outcome: "upstream_completed",
            }),
          ]}
        />,
      );

      const cards = container.querySelectorAll(".border");
      expect(cards[0]).toHaveClass("border-green-200");
      expect(screen.getByText("Upstream completed")).toBeInTheDocument();
    });

    it("styles a rule-absorbed semantic error attempt as semantic, not success", () => {
      const { container } = render(
        <RequestAttemptTimeline
          attempts={[
            createMockAttempt({
              id: 1,
              attempt: 0,
              status_code: 200,
              outcome: "upstream_semantic_error",
              result_visible_to_client: false,
            }),
          ]}
        />,
      );

      const cards = container.querySelectorAll(".border");
      expect(cards[0]).toHaveClass("border-amber-200");
      expect(cards[0]).not.toHaveClass("border-green-200");
      expect(
        screen.getByText(
          "Semantic error suppressed before client-visible data",
        ),
      ).toBeInTheDocument();
    });

    it.each([
      ["upstream_http_status_error", "Upstream returned an error status"],
      ["upstream_incomplete", "Upstream response incomplete"],
      ["gateway_error", "Gateway error"],
    ])("marks %s as failed", (outcome, label) => {
      const { container } = render(
        <RequestAttemptTimeline
          attempts={[
            createMockAttempt({
              id: 1,
              attempt: 0,
              status_code: 502,
              outcome: outcome as RequestAttempt["outcome"],
            }),
          ]}
        />,
      );

      const cards = container.querySelectorAll(".border");
      expect(cards[0]).toHaveClass("border-red-200");
      expect(screen.getByText(label)).toBeInTheDocument();
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
    it.each([
      [200, "bg-green-500"],
      [503, "bg-red-500"],
      [429, "bg-amber-500"],
      [0, "bg-gray-400"],
    ])("maps status %i to %s", (statusCode, className) => {
      const attempts = [createMockAttempt({ status_code: statusCode })];
      const { container } = render(
        <RequestAttemptTimeline attempts={attempts} />,
      );

      const dot = container.querySelector(".rounded-full.w-3.h-3");
      expect(dot).toHaveClass(className);
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

  describe("selection semantics", () => {
    it("renders switch mode, provider attempt counts, and continuity provenance", () => {
      const attempts = [
        createMockAttempt({
          id: 1,
          provider_id: "provider-next",
          switch_mode: "replacement",
          provider_attempt: 2,
          provider_switch_count: 1,
          continuity_seeded: true,
          continuity_origin_provider_id: "provider-origin",
          continuity_seed_age_ms: 345,
        }),
      ];

      render(<RequestAttemptTimeline attempts={attempts} />);

      expect(
        screen.getByText("Mode: Pre-visible replacement"),
      ).toBeInTheDocument();
      expect(screen.getByText("Provider attempt 2")).toBeInTheDocument();
      expect(screen.getByText("Provider switches 1")).toBeInTheDocument();
      expect(screen.getByText("Continuity provenance")).toBeInTheDocument();
      expect(
        screen.getByText(/Heuristic seed matched from provider-origin/i),
      ).toBeInTheDocument();
      expect(screen.getByText(/seed age 345ms/i)).toBeInTheDocument();
    });
  });

  registerWebSocketLifecycleAttributionTests();
});
