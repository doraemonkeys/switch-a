import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, act } from "@testing-library/react";
import { LiveRequestsPanel } from "./LiveRequestsPanel";
import type { ActiveRequest } from "../api/types";

// Test data constants
// eslint-disable-next-line sonarjs/no-hardcoded-ip -- safe for testing
const TEST_CLIENT_IP = "192.168.1.1";
const TEST_USER_ID = "user-123";

// Helper to create a mock active request
function createMockRequest(overrides?: Partial<ActiveRequest>): ActiveRequest {
  return {
    // eslint-disable-next-line sonarjs/pseudo-random -- safe for test data generation
    request_id: `req-${Math.random().toString(36).slice(2)}`,
    provider_id: "provider-1",
    model: "claude-3-opus",
    api_type: "claude",
    user_id: TEST_USER_ID,
    client_ip: TEST_CLIENT_IP,
    is_sse: false,
    started_at: new Date().toISOString(),
    ...overrides,
  };
}

describe("LiveRequestsPanel", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    // Set a fixed time for consistent testing
    vi.setSystemTime(new Date("2024-01-15T12:00:00.000Z"));
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  describe("error state", () => {
    it("renders error message when error is provided", () => {
      const error = new Error("Network connection failed");
      render(<LiveRequestsPanel requests={[]} loading={false} error={error} />);

      expect(
        screen.getByText(/Failed to load active requests/),
      ).toBeInTheDocument();
      expect(screen.getByText(/Network connection failed/)).toBeInTheDocument();
    });

    it("prioritizes error state over loading state", () => {
      const error = new Error("Server error");
      render(<LiveRequestsPanel requests={[]} loading={true} error={error} />);

      expect(
        screen.getByText(/Failed to load active requests/),
      ).toBeInTheDocument();
      expect(
        screen.queryByText(/Loading active requests/),
      ).not.toBeInTheDocument();
    });
  });

  describe("loading state", () => {
    it("shows loading spinner when loading with no requests", () => {
      render(<LiveRequestsPanel requests={[]} loading={true} error={null} />);

      expect(screen.getByText(/Loading active requests/)).toBeInTheDocument();
    });

    it("does not show loading spinner when loading with existing requests", () => {
      const requests = [createMockRequest()];
      render(
        <LiveRequestsPanel requests={requests} loading={true} error={null} />,
      );

      expect(
        screen.queryByText(/Loading active requests/),
      ).not.toBeInTheDocument();
      expect(screen.getByText("claude-3-opus")).toBeInTheDocument();
    });
  });

  describe("empty state", () => {
    it("shows empty state message when no requests and not loading", () => {
      render(<LiveRequestsPanel requests={[]} loading={false} error={null} />);

      expect(screen.getByText("No active requests")).toBeInTheDocument();
      expect(screen.getByText(/Requests will appear here/)).toBeInTheDocument();
    });
  });

  describe("request rendering", () => {
    it("renders request with all details", () => {
      const requests = [
        createMockRequest({
          request_id: "req-1",
          model: "claude-3-opus",
          api_type: "claude",
          user_id: TEST_USER_ID,
          client_ip: TEST_CLIENT_IP,
        }),
      ];

      render(
        <LiveRequestsPanel requests={requests} loading={false} error={null} />,
      );

      expect(screen.getByText("claude-3-opus")).toBeInTheDocument();
      expect(screen.getByText("claude")).toBeInTheDocument();
      expect(screen.getByText(TEST_USER_ID)).toBeInTheDocument();
      expect(screen.getByText(TEST_CLIENT_IP)).toBeInTheDocument();
    });

    it("displays SSE badge when request is SSE", () => {
      const requests = [createMockRequest({ is_sse: true })];
      render(
        <LiveRequestsPanel requests={requests} loading={false} error={null} />,
      );

      expect(screen.getByText("SSE")).toBeInTheDocument();
    });

    it("does not display SSE badge when request is not SSE", () => {
      const requests = [createMockRequest({ is_sse: false })];
      render(
        <LiveRequestsPanel requests={requests} loading={false} error={null} />,
      );

      expect(screen.queryByText("SSE")).not.toBeInTheDocument();
    });

    it("shows provider name when provided in providerNames map", () => {
      const requests = [createMockRequest({ provider_id: "provider-abc" })];
      const providerNames = new Map([["provider-abc", "My Provider"]]);

      render(
        <LiveRequestsPanel
          requests={requests}
          loading={false}
          error={null}
          providerNames={providerNames}
        />,
      );

      expect(screen.getByText("My Provider")).toBeInTheDocument();
    });

    it("shows provider_id when not in providerNames map", () => {
      const requests = [createMockRequest({ provider_id: "provider-xyz" })];
      const providerNames = new Map([["provider-abc", "My Provider"]]);

      render(
        <LiveRequestsPanel
          requests={requests}
          loading={false}
          error={null}
          providerNames={providerNames}
        />,
      );

      expect(screen.getByText("provider-xyz")).toBeInTheDocument();
    });

    it("shows Unknown model when model is empty", () => {
      const requests = [createMockRequest({ model: "" })];
      render(
        <LiveRequestsPanel requests={requests} loading={false} error={null} />,
      );

      expect(screen.getByText("Unknown model")).toBeInTheDocument();
    });

    it("has accessible article role on request rows", () => {
      const requests = [createMockRequest()];
      render(
        <LiveRequestsPanel requests={requests} loading={false} error={null} />,
      );

      expect(screen.getByRole("article")).toBeInTheDocument();
    });
  });

  describe("request sorting", () => {
    it("sorts requests by started_at (oldest first)", () => {
      const requests = [
        createMockRequest({
          request_id: "req-2",
          model: "model-newer",
          started_at: "2024-01-15T12:00:00Z",
        }),
        createMockRequest({
          request_id: "req-1",
          model: "model-older",
          started_at: "2024-01-15T11:00:00Z",
        }),
      ];

      render(
        <LiveRequestsPanel requests={requests} loading={false} error={null} />,
      );

      const articles = screen.getAllByRole("article");
      expect(articles).toHaveLength(2);
      // Oldest first
      expect(articles[0]).toHaveAccessibleName(/model-older/);
      expect(articles[1]).toHaveAccessibleName(/model-newer/);
    });
  });

  describe("long-running request highlighting", () => {
    it("highlights request when duration exceeds threshold", () => {
      // Started 31 seconds ago (> 30s default threshold)
      const thirtyOneSecondsAgo = new Date(Date.now() - 31000).toISOString();
      const requests = [createMockRequest({ started_at: thirtyOneSecondsAgo })];

      render(
        <LiveRequestsPanel requests={requests} loading={false} error={null} />,
      );

      expect(screen.getByText("Long running")).toBeInTheDocument();
    });

    it("does not highlight request when duration is under threshold", () => {
      // Started 5 seconds ago (< 30s default threshold)
      const fiveSecondsAgo = new Date(Date.now() - 5000).toISOString();
      const requests = [createMockRequest({ started_at: fiveSecondsAgo })];

      render(
        <LiveRequestsPanel requests={requests} loading={false} error={null} />,
      );

      expect(screen.queryByText("Long running")).not.toBeInTheDocument();
    });

    it("uses custom longRunningThreshold prop", () => {
      // Started 11 seconds ago with 10s threshold
      const elevenSecondsAgo = new Date(Date.now() - 11000).toISOString();
      const requests = [createMockRequest({ started_at: elevenSecondsAgo })];

      render(
        <LiveRequestsPanel
          requests={requests}
          loading={false}
          error={null}
          longRunningThreshold={10000}
        />,
      );

      expect(screen.getByText("Long running")).toBeInTheDocument();
    });
  });

  describe("duration formatting", () => {
    it("shows seconds for durations under 60 seconds", () => {
      const thirtySecondsAgo = new Date(Date.now() - 30000).toISOString();
      const requests = [createMockRequest({ started_at: thirtySecondsAgo })];

      render(
        <LiveRequestsPanel requests={requests} loading={false} error={null} />,
      );

      expect(screen.getByText("30s")).toBeInTheDocument();
    });

    it("shows minutes and seconds for durations under 1 hour", () => {
      const ninetySecondsAgo = new Date(Date.now() - 90000).toISOString();
      const requests = [createMockRequest({ started_at: ninetySecondsAgo })];

      render(
        <LiveRequestsPanel requests={requests} loading={false} error={null} />,
      );

      expect(screen.getByText("1m 30s")).toBeInTheDocument();
    });

    it("shows hours and minutes for durations over 1 hour", () => {
      const ninetyMinutesAgo = new Date(
        Date.now() - 90 * 60 * 1000,
      ).toISOString();
      const requests = [createMockRequest({ started_at: ninetyMinutesAgo })];

      render(
        <LiveRequestsPanel requests={requests} loading={false} error={null} />,
      );

      expect(screen.getByText("1h 30m")).toBeInTheDocument();
    });

    it("shows 0s for requests just started", () => {
      const justNow = new Date(Date.now()).toISOString();
      const requests = [createMockRequest({ started_at: justNow })];

      render(
        <LiveRequestsPanel requests={requests} loading={false} error={null} />,
      );

      expect(screen.getByText("0s")).toBeInTheDocument();
    });
  });

  describe("real-time updates", () => {
    it("updates duration every second", () => {
      const fiveSecondsAgo = new Date(Date.now() - 5000).toISOString();
      const requests = [createMockRequest({ started_at: fiveSecondsAgo })];

      render(
        <LiveRequestsPanel requests={requests} loading={false} error={null} />,
      );

      expect(screen.getByText("5s")).toBeInTheDocument();

      // Advance time by 3 seconds and wrap in act() to flush state updates
      act(() => {
        vi.advanceTimersByTime(3000);
      });

      expect(screen.getByText("8s")).toBeInTheDocument();
    });
  });

  describe("user_id visibility", () => {
    it("shows user_id when present", () => {
      const requests = [createMockRequest({ user_id: "custom-user" })];
      render(
        <LiveRequestsPanel requests={requests} loading={false} error={null} />,
      );

      expect(screen.getByText("custom-user")).toBeInTheDocument();
    });

    it("does not show user_id section when empty", () => {
      const requests = [createMockRequest({ user_id: "" })];
      render(
        <LiveRequestsPanel requests={requests} loading={false} error={null} />,
      );

      // Should not have the bullet separator that comes before user_id
      const articles = screen.getAllByRole("article");
      // The user_id is rendered inside a span with font-mono text-xs class
      expect(articles[0].querySelectorAll(".font-mono.text-xs")).toHaveLength(
        1,
      ); // Only client_ip
    });
  });
});
