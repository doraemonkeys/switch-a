import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { LogDetailModal } from "./LogDetailModal";
import type { RequestLog } from "../api/types";

// Test data constant
// eslint-disable-next-line sonarjs/no-hardcoded-ip -- safe for testing
const TEST_CLIENT_IP = "192.168.1.1";

// Helper to create a mock log
function createMockLog(overrides?: Partial<RequestLog>): RequestLog {
  return {
    id: 1,
    request_id: "test-request-id-1",
    provider_id: "provider-1",
    api_type: "claude",
    model: "claude-3-opus",
    client_ip: TEST_CLIENT_IP,
    user_id: "user-123",
    status_code: 200,
    latency_ms: 150,
    success: true,
    is_sse: false,
    is_websocket: false,
    error_msg: null,
    created_at: "2024-01-15T10:30:00Z",
    retry_count: 0,
    is_sticky: false,
    ...overrides,
  };
}

// eslint-disable-next-line max-lines-per-function -- comprehensive test suite for modal component
describe("LogDetailModal", () => {
  const mockOnClose = vi.fn();

  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("returns null when log is null", () => {
    const { container } = render(
      <LogDetailModal
        log={null}
        providerName="Test Provider"
        onClose={mockOnClose}
      />,
    );
    expect(container.firstChild).toBeNull();
  });

  it("renders log details when log is provided", () => {
    const log = createMockLog();
    render(
      <LogDetailModal
        log={log}
        providerName="Test Provider"
        onClose={mockOnClose}
      />,
    );

    // Title shows "Request Details" when no request_method/request_path
    expect(screen.getByText("Request Details")).toBeInTheDocument();
    expect(screen.getByText("#1")).toBeInTheDocument();
    expect(screen.getByText("Test Provider")).toBeInTheDocument();
    expect(screen.getByText("claude-3-opus")).toBeInTheDocument();
    expect(screen.getByText("claude")).toBeInTheDocument();
    expect(screen.getByText("200")).toBeInTheDocument();
    expect(screen.getByText("150ms")).toBeInTheDocument();
    expect(screen.getByText(TEST_CLIENT_IP)).toBeInTheDocument();
    expect(screen.getByText("user-123")).toBeInTheDocument();
  });

  it("shows METHOD /path title when request_method and request_path are provided", () => {
    const log = createMockLog({
      request_method: "POST",
      request_path: "/v1/messages",
    });
    render(
      <LogDetailModal
        log={log}
        providerName="Test Provider"
        onClose={mockOnClose}
      />,
    );

    expect(screen.getByText("POST")).toBeInTheDocument();
    expect(screen.getByText("/v1/messages")).toBeInTheDocument();
  });

  it("shows success badge for successful logs", () => {
    const log = createMockLog({ success: true });
    render(
      <LogDetailModal
        log={log}
        providerName="Test Provider"
        onClose={mockOnClose}
      />,
    );

    expect(screen.getByText("✅ Success")).toBeInTheDocument();
  });

  it("shows failed badge for failed logs", () => {
    const log = createMockLog({ success: false });
    render(
      <LogDetailModal
        log={log}
        providerName="Test Provider"
        onClose={mockOnClose}
      />,
    );

    expect(screen.getByText("❌ Failed")).toBeInTheDocument();
  });

  it("shows websocket lifecycle badges for committed client disconnects", () => {
    const log = createMockLog({
      is_websocket: true,
      success: false,
      status_code: 101,
      session_committed: true,
      probe_outcome: "observed_usable_model",
      terminal_cause: "client_disconnect",
      commit_source: "semantic_event",
      sticky_written: true,
      error_msg: "websocket: close 1006",
    });
    render(
      <LogDetailModal
        log={log}
        providerName="Test Provider"
        onClose={mockOnClose}
      />,
    );

    expect(screen.getAllByText("Client disconnect").length).toBeGreaterThan(0);
    expect(screen.getAllByText("Committed").length).toBeGreaterThan(0);
    expect(screen.getByText("WebSocket Lifecycle")).toBeInTheDocument();
    expect(
      screen.getByText("Committed session ended on client disconnect"),
    ).toBeInTheDocument();
    expect(screen.getByText("Semantic event")).toBeInTheDocument();
    expect(screen.getByText("Observed usable model")).toBeInTheDocument();
    expect(screen.getByText("Written")).toBeInTheDocument();
    expect(screen.getByText("Connection Note")).toBeInTheDocument();
    expect(screen.queryByText("Error Details")).not.toBeInTheDocument();
  });

  it("keeps uncommitted websocket semantic errors in the error section", () => {
    const log = createMockLog({
      is_websocket: true,
      success: false,
      status_code: 502,
      session_committed: false,
      terminal_cause: "upstream_semantic_error",
      error_msg: '{"error":"provider failed"}',
    });
    render(
      <LogDetailModal
        log={log}
        providerName="Test Provider"
        onClose={mockOnClose}
      />,
    );

    expect(screen.getByText("No service")).toBeInTheDocument();
    expect(screen.getAllByText("Uncommitted")).toHaveLength(2);
    expect(screen.getAllByText("Upstream semantic error")).toHaveLength(2);
    expect(screen.getByText("Error Details")).toBeInTheDocument();
  });

  it("shows visibility and reconnect guidance for client-visible lifecycle failures", () => {
    const log = createMockLog({
      is_websocket: true,
      success: false,
      status_code: 101,
      session_committed: false,
      client_visible: true,
      terminal_cause: "upstream_semantic_error",
      recovery_action: "reconnect_required",
      error_msg: '{"error":"gateway reconnect required"}',
    });
    render(
      <LogDetailModal
        log={log}
        providerName="Test Provider"
        onClose={mockOnClose}
      />,
    );

    expect(screen.getAllByText("Reconnect required").length).toBeGreaterThan(0);
    expect(screen.getByText("Client Visibility")).toBeInTheDocument();
    expect(screen.getAllByText("Visible").length).toBeGreaterThan(0);
    expect(screen.getByText("Recovery Action")).toBeInTheDocument();
    expect(screen.getByText("Error Details")).toBeInTheDocument();
  });

  it("suppresses no-op recovery action details for healthy websocket sessions", () => {
    const log = createMockLog({
      is_websocket: true,
      success: true,
      status_code: 101,
      session_committed: true,
      client_visible: true,
      terminal_cause: "clean_close",
      recovery_action: "none",
    });
    render(
      <LogDetailModal
        log={log}
        providerName="Test Provider"
        onClose={mockOnClose}
      />,
    );

    expect(screen.queryByText("Recovery Action")).not.toBeInTheDocument();
    expect(screen.queryByText("None")).not.toBeInTheDocument();
  });

  it("separates websocket provider attempts from final lifecycle attribution", () => {
    const log = createMockLog({
      is_websocket: true,
      success: false,
      status_code: 101,
      session_committed: true,
      terminal_cause: "client_disconnect",
      provider_id: "provider-final",
      attempts: [
        {
          id: 1,
          request_id: "test-request-id-1",
          provider_id: "provider-old",
          attempt: 1,
          status_code: 503,
          error: "provider unavailable",
          latency_ms: 80,
          created_at: "2024-01-15T10:30:00Z",
        },
        {
          id: 2,
          request_id: "test-request-id-1",
          provider_id: "provider-final",
          attempt: 2,
          status_code: 101,
          error: "",
          latency_ms: 120,
          created_at: "2024-01-15T10:30:01Z",
        },
      ],
    });

    render(
      <LogDetailModal
        log={log}
        providerName="Final Provider"
        onClose={mockOnClose}
      />,
    );

    expect(screen.getByText("Provider Attempts")).toBeInTheDocument();
    expect(screen.getAllByText("Outcome Provider").length).toBeGreaterThan(0);
    expect(screen.getAllByText("Final Provider").length).toBeGreaterThan(0);
    expect(
      screen.getByText(
        "RequestLog defines the final WebSocket lifecycle attribution. These rows show provider-attempt detail only.",
      ),
    ).toBeInTheDocument();
    expect(screen.getByText("Outcome owner")).toBeInTheDocument();
    expect(screen.queryByText("Provider Chain")).not.toBeInTheDocument();
    expect(screen.getByText("Upgrade Status")).toBeInTheDocument();
    expect(screen.getAllByText("101 Upgrade").length).toBeGreaterThan(0);
  });

  it("shows error details section for failed logs with error message", () => {
    const log = createMockLog({
      success: false,
      error_msg: "Rate limit exceeded",
    });
    render(
      <LogDetailModal
        log={log}
        providerName="Test Provider"
        onClose={mockOnClose}
      />,
    );

    expect(screen.getByText("Error Details")).toBeInTheDocument();
    expect(screen.getByText("Rate limit exceeded")).toBeInTheDocument();
  });

  it("does not show error details section for successful logs", () => {
    const log = createMockLog({ success: true });
    render(
      <LogDetailModal
        log={log}
        providerName="Test Provider"
        onClose={mockOnClose}
      />,
    );

    expect(screen.queryByText("Error Details")).not.toBeInTheDocument();
  });

  it("does not show error details for failed logs without error message", () => {
    const log = createMockLog({ success: false, error_msg: null });
    render(
      <LogDetailModal
        log={log}
        providerName="Test Provider"
        onClose={mockOnClose}
      />,
    );

    expect(screen.queryByText("Error Details")).not.toBeInTheDocument();
  });

  it("closes when clicking the close button", () => {
    const log = createMockLog();
    render(
      <LogDetailModal
        log={log}
        providerName="Test Provider"
        onClose={mockOnClose}
      />,
    );

    fireEvent.click(screen.getByLabelText("Close"));
    expect(mockOnClose).toHaveBeenCalledTimes(1);
  });

  it("closes when clicking the Close button in footer", () => {
    const log = createMockLog();
    render(
      <LogDetailModal
        log={log}
        providerName="Test Provider"
        onClose={mockOnClose}
      />,
    );

    // Get all buttons with "Close" and click the one in the footer (last one)
    const closeButtons = screen.getAllByRole("button");
    const footerButton = closeButtons.find(
      (btn) => btn.textContent === "Close",
    );
    fireEvent.click(footerButton!);
    expect(mockOnClose).toHaveBeenCalledTimes(1);
  });

  it("closes when clicking the backdrop", () => {
    const log = createMockLog();
    render(
      <LogDetailModal
        log={log}
        providerName="Test Provider"
        onClose={mockOnClose}
      />,
    );

    // Click on the backdrop (the outer div with bg-black/50)
    const backdrop = screen.getByText("Request Details").closest(".fixed");
    fireEvent.click(backdrop!);
    expect(mockOnClose).toHaveBeenCalledTimes(1);
  });

  it("does not close when clicking inside the modal content", () => {
    const log = createMockLog();
    render(
      <LogDetailModal
        log={log}
        providerName="Test Provider"
        onClose={mockOnClose}
      />,
    );

    // Click on the modal content
    fireEvent.click(screen.getByText("Request Details"));
    expect(mockOnClose).not.toHaveBeenCalled();
  });

  it("closes when pressing Escape key", () => {
    const log = createMockLog();
    render(
      <LogDetailModal
        log={log}
        providerName="Test Provider"
        onClose={mockOnClose}
      />,
    );

    fireEvent.keyDown(document, { key: "Escape" });
    expect(mockOnClose).toHaveBeenCalledTimes(1);
  });

  it("does not add event listener when log is null", () => {
    const addEventListenerSpy = vi.spyOn(document, "addEventListener");
    render(
      <LogDetailModal
        log={null}
        providerName="Test Provider"
        onClose={mockOnClose}
      />,
    );

    expect(addEventListenerSpy).not.toHaveBeenCalledWith(
      "keydown",
      expect.any(Function),
    );
  });

  it("shows green status code badge for 2xx status", () => {
    const log = createMockLog({ status_code: 200 });
    render(
      <LogDetailModal
        log={log}
        providerName="Test Provider"
        onClose={mockOnClose}
      />,
    );

    const badge = screen.getByText("200");
    expect(badge).toHaveClass("bg-green-100");
  });

  it("shows red status code badge for 4xx status", () => {
    const log = createMockLog({ status_code: 400 });
    render(
      <LogDetailModal
        log={log}
        providerName="Test Provider"
        onClose={mockOnClose}
      />,
    );

    const badge = screen.getByText("400");
    expect(badge).toHaveClass("bg-red-100");
  });

  it("shows red status code badge for 5xx status", () => {
    const log = createMockLog({ status_code: 500 });
    render(
      <LogDetailModal
        log={log}
        providerName="Test Provider"
        onClose={mockOnClose}
      />,
    );

    const badge = screen.getByText("500");
    expect(badge).toHaveClass("bg-red-100");
  });

  it("shows yellow status code badge for 3xx status", () => {
    const log = createMockLog({ status_code: 302 });
    render(
      <LogDetailModal
        log={log}
        providerName="Test Provider"
        onClose={mockOnClose}
      />,
    );

    const badge = screen.getByText("302");
    expect(badge).toHaveClass("bg-yellow-100");
  });

  it("does not show user ID row when user_id is empty", () => {
    const log = createMockLog({ user_id: "" });
    render(
      <LogDetailModal
        log={log}
        providerName="Test Provider"
        onClose={mockOnClose}
      />,
    );

    expect(screen.queryByText("User ID")).not.toBeInTheDocument();
  });

  it("uses provider_id as fallback when providerName is empty", () => {
    const log = createMockLog({ provider_id: "provider-abc" });
    render(<LogDetailModal log={log} providerName="" onClose={mockOnClose} />);

    expect(screen.getByText("provider-abc")).toBeInTheDocument();
  });

  it("removes keydown event listener on unmount", () => {
    const removeEventListenerSpy = vi.spyOn(document, "removeEventListener");
    const log = createMockLog();
    const { unmount } = render(
      <LogDetailModal
        log={log}
        providerName="Test Provider"
        onClose={mockOnClose}
      />,
    );

    unmount();
    expect(removeEventListenerSpy).toHaveBeenCalledWith(
      "keydown",
      expect.any(Function),
    );
  });

  describe("sticky session badge", () => {
    it("shows sticky session badge when is_sticky is true", () => {
      const log = createMockLog({ is_sticky: true });
      render(
        <LogDetailModal
          log={log}
          providerName="Test Provider"
          onClose={mockOnClose}
        />,
      );

      expect(screen.getByText(/Sticky Session/)).toBeInTheDocument();
    });

    it("does not show sticky session badge when is_sticky is false", () => {
      const log = createMockLog({ is_sticky: false });
      render(
        <LogDetailModal
          log={log}
          providerName="Test Provider"
          onClose={mockOnClose}
        />,
      );

      expect(screen.queryByText(/Sticky Session/)).not.toBeInTheDocument();
    });
  });

  describe("retry count badge", () => {
    it("shows retry badge with singular text for 1 retry", () => {
      const log = createMockLog({ retry_count: 1 });
      render(
        <LogDetailModal
          log={log}
          providerName="Test Provider"
          onClose={mockOnClose}
        />,
      );

      expect(screen.getByText(/1 retry/)).toBeInTheDocument();
    });

    it("shows retry badge with plural text for multiple retries", () => {
      const log = createMockLog({ retry_count: 3 });
      render(
        <LogDetailModal
          log={log}
          providerName="Test Provider"
          onClose={mockOnClose}
        />,
      );

      expect(screen.getByText(/3 retries/)).toBeInTheDocument();
    });

    it("does not show retry badge when retry_count is 0", () => {
      const log = createMockLog({ retry_count: 0 });
      render(
        <LogDetailModal
          log={log}
          providerName="Test Provider"
          onClose={mockOnClose}
        />,
      );

      expect(screen.queryByText(/retry/)).not.toBeInTheDocument();
    });
  });

  describe("request type display", () => {
    it("shows SSE Stream badge when is_sse is true", () => {
      const log = createMockLog({ is_sse: true });
      render(
        <LogDetailModal
          log={log}
          providerName="Test Provider"
          onClose={mockOnClose}
        />,
      );

      expect(screen.getByText("SSE Stream")).toBeInTheDocument();
    });

    it("shows Regular badge when is_sse is false", () => {
      const log = createMockLog({ is_sse: false });
      render(
        <LogDetailModal
          log={log}
          providerName="Test Provider"
          onClose={mockOnClose}
        />,
      );

      expect(screen.getByText("Regular")).toBeInTheDocument();
    });
  });

  describe("request attempts timeline", () => {
    it("renders RequestAttemptTimeline when attempts array is non-empty", () => {
      const log = createMockLog({
        attempts: [
          {
            id: 1,
            request_id: "test-request-id-1",
            provider_id: "provider-1",
            attempt: 0,
            status_code: 500,
            error: "Connection timeout",
            latency_ms: 30000,
            created_at: "2024-01-15T10:29:30Z",
          },
          {
            id: 2,
            request_id: "test-request-id-1",
            provider_id: "provider-2",
            attempt: 1,
            status_code: 200,
            error: "",
            latency_ms: 150,
            created_at: "2024-01-15T10:30:00Z",
          },
        ],
      });

      render(
        <LogDetailModal
          log={log}
          providerName="Test Provider"
          onClose={mockOnClose}
        />,
      );

      expect(screen.getByText("Request Attempts")).toBeInTheDocument();
      expect(screen.getByText("Attempt 1")).toBeInTheDocument();
      expect(screen.getByText("Attempt 2")).toBeInTheDocument();
    });

    it("does not show Request Attempts section when attempts is empty", () => {
      const log = createMockLog({ attempts: [] });
      render(
        <LogDetailModal
          log={log}
          providerName="Test Provider"
          onClose={mockOnClose}
        />,
      );

      expect(screen.queryByText("Request Attempts")).not.toBeInTheDocument();
    });

    it("does not show Request Attempts section when attempts is undefined", () => {
      const log = createMockLog();
      // Explicitly set attempts to undefined
      delete (log as Partial<typeof log>).attempts;
      render(
        <LogDetailModal
          log={log}
          providerName="Test Provider"
          onClose={mockOnClose}
        />,
      );

      expect(screen.queryByText("Request Attempts")).not.toBeInTheDocument();
    });

    it("passes providerNames to RequestAttemptTimeline", () => {
      const log = createMockLog({
        attempts: [
          {
            id: 1,
            request_id: "test-request-id-1",
            provider_id: "provider-abc",
            attempt: 0,
            status_code: 200,
            error: "",
            latency_ms: 100,
            created_at: "2024-01-15T10:30:00Z",
          },
        ],
      });
      const providerNames = new Map([["provider-abc", "My Provider"]]);

      render(
        <LogDetailModal
          log={log}
          providerName="Test Provider"
          providerNames={providerNames}
          onClose={mockOnClose}
        />,
      );

      expect(screen.getByText(/Provider: My Provider/)).toBeInTheDocument();
    });

    it("keeps websocket outcome attribution on RequestLog while timeline rows stay provider-scoped", () => {
      const log = createMockLog({
        is_websocket: true,
        status_code: 101,
        provider_id: "provider-final",
        attempts: [
          {
            id: 1,
            request_id: "test-request-id-1",
            provider_id: "provider-old",
            attempt: 0,
            status_code: 101,
            error: "semantic error",
            phase: "post_upgrade_pre_visible",
            outcome: "upstream_semantic_error",
            result_visible_to_client: false,
            latency_ms: 90,
            created_at: "2024-01-15T10:29:30Z",
          },
          {
            id: 2,
            request_id: "test-request-id-1",
            provider_id: "provider-final",
            attempt: 1,
            status_code: 101,
            error: "",
            phase: "visible",
            outcome: "visible_session",
            result_visible_to_client: true,
            latency_ms: 150,
            created_at: "2024-01-15T10:30:00Z",
          },
        ],
      });
      const providerNames = new Map([
        ["provider-old", "Old Provider"],
        ["provider-final", "Final Provider"],
      ]);

      render(
        <LogDetailModal
          log={log}
          providerName="Final Provider"
          providerNames={providerNames}
          onClose={mockOnClose}
        />,
      );

      expect(screen.getAllByText("Outcome Provider").length).toBeGreaterThan(0);
      expect(screen.getByText("Provider Attempts")).toBeInTheDocument();
      expect(
        screen.getByText(
          "RequestLog defines the final WebSocket lifecycle attribution. These rows show provider-attempt detail only.",
        ),
      ).toBeInTheDocument();
      expect(screen.getByText("Outcome owner")).toBeInTheDocument();
      expect(
        screen.getByText(
          "Semantic error suppressed before client-visible data",
        ),
      ).toBeInTheDocument();
    });
  });
});
