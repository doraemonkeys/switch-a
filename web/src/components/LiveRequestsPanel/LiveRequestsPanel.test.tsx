import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, act, within, fireEvent } from "@testing-library/react";
import { LiveRequestsPanel } from "./LiveRequestsPanel";
import type { ActiveRequest } from "../../api/types";

// Test data constants
// eslint-disable-next-line sonarjs/no-hardcoded-ip -- safe for testing
const TEST_CLIENT_IP = "192.168.1.1";
// eslint-disable-next-line sonarjs/no-hardcoded-ip -- safe for testing
const TEST_CLIENT_IP_2 = "192.168.1.2";
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

// Helper to switch to All List view
function switchToAllListView() {
  fireEvent.click(screen.getByText("All List"));
}

describe("LiveRequestsPanel", () => {
  beforeEach(() => {
    vi.useFakeTimers();
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
      expect(screen.getByText(TEST_CLIENT_IP)).toBeInTheDocument();
    });
  });

  describe("empty state", () => {
    it("shows empty state message when no requests and not loading", () => {
      render(<LiveRequestsPanel requests={[]} loading={false} error={null} />);
      expect(screen.getByText("No active requests")).toBeInTheDocument();
      expect(screen.getByText(/Requests will appear here/)).toBeInTheDocument();
    });
  });
});

describe("LiveRequestsPanel - View Modes", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2024-01-15T12:00:00.000Z"));
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("renders view mode tabs", () => {
    const requests = [createMockRequest()];
    render(
      <LiveRequestsPanel requests={requests} loading={false} error={null} />,
    );
    expect(screen.getByText("By IP")).toBeInTheDocument();
    expect(screen.getByText("By API")).toBeInTheDocument();
    expect(screen.getByText("By Model")).toBeInTheDocument();
    expect(screen.getByText("All List")).toBeInTheDocument();
  });

  it("defaults to By IP view", () => {
    const requests = [createMockRequest()];
    render(
      <LiveRequestsPanel requests={requests} loading={false} error={null} />,
    );
    expect(screen.getByText(TEST_CLIENT_IP)).toBeInTheDocument();
  });

  it("switches to All List view when clicked", () => {
    const requests = [createMockRequest()];
    render(
      <LiveRequestsPanel requests={requests} loading={false} error={null} />,
    );
    switchToAllListView();
    expect(
      screen.getByRole("button", { name: /Active request for model/ }),
    ).toBeInTheDocument();
  });

  it("groups requests by IP address in By IP view", () => {
    const requests = [
      createMockRequest({ client_ip: TEST_CLIENT_IP }),
      createMockRequest({ client_ip: TEST_CLIENT_IP }),
      createMockRequest({ client_ip: TEST_CLIENT_IP_2 }),
    ];
    render(
      <LiveRequestsPanel requests={requests} loading={false} error={null} />,
    );
    expect(screen.getByText(TEST_CLIENT_IP)).toBeInTheDocument();
    expect(screen.getByText(TEST_CLIENT_IP_2)).toBeInTheDocument();
    expect(screen.getByText("2 requests")).toBeInTheDocument();
    expect(screen.getByText("1 request")).toBeInTheDocument();
  });

  it("expands group when clicked", () => {
    const requests = [
      createMockRequest({ client_ip: TEST_CLIENT_IP, model: "claude-3-opus" }),
    ];
    render(
      <LiveRequestsPanel requests={requests} loading={false} error={null} />,
    );
    fireEvent.click(screen.getByText(TEST_CLIENT_IP));
    expect(screen.getByText("claude-3-opus")).toBeInTheDocument();
  });
});

describe("LiveRequestsPanel - Search", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2024-01-15T12:00:00.000Z"));
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("renders search input", () => {
    const requests = [createMockRequest()];
    render(
      <LiveRequestsPanel requests={requests} loading={false} error={null} />,
    );
    expect(
      screen.getByPlaceholderText("Search IP, model, user..."),
    ).toBeInTheDocument();
  });

  it("filters requests by search query", () => {
    const requests = [
      createMockRequest({ client_ip: TEST_CLIENT_IP, model: "claude-3-opus" }),
      createMockRequest({ client_ip: TEST_CLIENT_IP_2, model: "gpt-4" }),
    ];
    render(
      <LiveRequestsPanel requests={requests} loading={false} error={null} />,
    );
    const searchInput = screen.getByPlaceholderText(
      "Search IP, model, user...",
    );
    fireEvent.change(searchInput, { target: { value: "gpt" } });
    expect(screen.queryByText(TEST_CLIENT_IP)).not.toBeInTheDocument();
    expect(screen.getByText(TEST_CLIENT_IP_2)).toBeInTheDocument();
  });

  it("shows filtered count when search is active", () => {
    const requests = [
      createMockRequest({ client_ip: TEST_CLIENT_IP }),
      createMockRequest({ client_ip: TEST_CLIENT_IP_2 }),
    ];
    render(
      <LiveRequestsPanel requests={requests} loading={false} error={null} />,
    );
    const searchInput = screen.getByPlaceholderText(
      "Search IP, model, user...",
    );
    fireEvent.change(searchInput, { target: { value: TEST_CLIENT_IP_2 } });
    // Scope query to the header section containing the count and "Active Requests" text
    const headerSection = screen.getByText("Active Requests").parentElement!;
    expect(within(headerSection).getByText("1")).toBeInTheDocument();
    expect(screen.getByText("(filtered from 2)")).toBeInTheDocument();
  });
});

describe("LiveRequestsPanel - Request Rendering", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2024-01-15T12:00:00.000Z"));
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("renders request with all details in All List view", () => {
    const requests = [
      createMockRequest({
        model: "claude-3-opus",
        api_type: "claude",
        user_id: TEST_USER_ID,
        client_ip: TEST_CLIENT_IP,
      }),
    ];
    render(
      <LiveRequestsPanel requests={requests} loading={false} error={null} />,
    );
    switchToAllListView();
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
    switchToAllListView();
    expect(screen.getByText("SSE")).toBeInTheDocument();
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
    switchToAllListView();
    expect(screen.getByText("My Provider")).toBeInTheDocument();
  });

  it("shows Unknown model when model is empty", () => {
    const requests = [createMockRequest({ model: "" })];
    render(
      <LiveRequestsPanel requests={requests} loading={false} error={null} />,
    );
    switchToAllListView();
    expect(screen.getByText("Unknown model")).toBeInTheDocument();
  });

  it("has accessible button role on request rows", () => {
    const requests = [createMockRequest()];
    render(
      <LiveRequestsPanel requests={requests} loading={false} error={null} />,
    );
    switchToAllListView();
    expect(
      screen.getByRole("button", { name: /Active request for model/ }),
    ).toBeInTheDocument();
  });
});

describe("LiveRequestsPanel - Long Running", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2024-01-15T12:00:00.000Z"));
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("highlights group with long-running requests", () => {
    const thirtyOneSecondsAgo = new Date(Date.now() - 31000).toISOString();
    const requests = [createMockRequest({ started_at: thirtyOneSecondsAgo })];
    const { container } = render(
      <LiveRequestsPanel
        requests={requests}
        loading={false}
        error={null}
        longRunningThreshold={30000}
      />,
    );
    // Warning indicator uses amber color class for long-running requests
    const warningIndicator = container.querySelector(".text-amber-500");
    expect(warningIndicator).toBeInTheDocument();
  });

  it("shows long running text in All List view", () => {
    const thirtyOneSecondsAgo = new Date(Date.now() - 31000).toISOString();
    const requests = [createMockRequest({ started_at: thirtyOneSecondsAgo })];
    render(
      <LiveRequestsPanel
        requests={requests}
        loading={false}
        error={null}
        longRunningThreshold={30000}
      />,
    );
    switchToAllListView();
    expect(screen.getByText("Long running")).toBeInTheDocument();
  });

  it("uses custom longRunningThreshold prop", () => {
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
    switchToAllListView();
    expect(screen.getByText("Long running")).toBeInTheDocument();
  });
});

describe("LiveRequestsPanel - Duration Formatting", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2024-01-15T12:00:00.000Z"));
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("shows seconds for durations under 60 seconds", () => {
    const thirtySecondsAgo = new Date(Date.now() - 30000).toISOString();
    const requests = [createMockRequest({ started_at: thirtySecondsAgo })];
    render(
      <LiveRequestsPanel requests={requests} loading={false} error={null} />,
    );
    switchToAllListView();
    expect(screen.getByText("30s")).toBeInTheDocument();
  });

  it("shows minutes and seconds for durations under 1 hour", () => {
    const ninetySecondsAgo = new Date(Date.now() - 90000).toISOString();
    const requests = [createMockRequest({ started_at: ninetySecondsAgo })];
    render(
      <LiveRequestsPanel requests={requests} loading={false} error={null} />,
    );
    switchToAllListView();
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
    switchToAllListView();
    expect(screen.getByText("1h 30m")).toBeInTheDocument();
  });

  it("updates duration every second", () => {
    const fiveSecondsAgo = new Date(Date.now() - 5000).toISOString();
    const requests = [createMockRequest({ started_at: fiveSecondsAgo })];
    render(
      <LiveRequestsPanel requests={requests} loading={false} error={null} />,
    );
    switchToAllListView();
    expect(screen.getByText("5s")).toBeInTheDocument();
    act(() => {
      vi.advanceTimersByTime(3000);
    });
    expect(screen.getByText("8s")).toBeInTheDocument();
  });
});

describe("LiveRequestsPanel - Sort and Load More", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2024-01-15T12:00:00.000Z"));
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("shows sort controls in All List view", () => {
    const requests = [createMockRequest()];
    render(
      <LiveRequestsPanel requests={requests} loading={false} error={null} />,
    );
    switchToAllListView();
    expect(screen.getByText("Duration")).toBeInTheDocument();
    expect(screen.getByText("Started")).toBeInTheDocument();
    expect(screen.getByText("Model")).toBeInTheDocument();
  });

  it("sorts by duration by default (longest first)", () => {
    const requests = [
      createMockRequest({
        model: "short-request",
        started_at: new Date(Date.now() - 5000).toISOString(),
      }),
      createMockRequest({
        model: "long-request",
        started_at: new Date(Date.now() - 60000).toISOString(),
      }),
    ];
    render(
      <LiveRequestsPanel requests={requests} loading={false} error={null} />,
    );
    switchToAllListView();
    const requestButtons = screen.getAllByRole("button", {
      name: /Active request for model/,
    });
    expect(
      within(requestButtons[0]).getByText("long-request"),
    ).toBeInTheDocument();
    expect(
      within(requestButtons[1]).getByText("short-request"),
    ).toBeInTheDocument();
  });

  it("shows Load more button when there are many requests", () => {
    const requests = Array.from({ length: 15 }, (_, i) =>
      createMockRequest({ request_id: `req-${i}`, model: `model-${i}` }),
    );
    render(
      <LiveRequestsPanel requests={requests} loading={false} error={null} />,
    );
    switchToAllListView();
    expect(screen.getByText("Showing 10 of 15")).toBeInTheDocument();
    expect(screen.getByText(/Load \d+ more/)).toBeInTheDocument();
    expect(screen.getByText("Show all 15")).toBeInTheDocument();
  });

  it("loads more requests when clicked", () => {
    const requests = Array.from({ length: 15 }, (_, i) =>
      createMockRequest({ request_id: `req-${i}`, model: `model-${i}` }),
    );
    render(
      <LiveRequestsPanel requests={requests} loading={false} error={null} />,
    );
    switchToAllListView();
    fireEvent.click(screen.getByText(/Load \d+ more/));
    expect(screen.getByText("Showing 15 of 15")).toBeInTheDocument();
  });
});
