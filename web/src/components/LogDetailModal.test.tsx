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
  });
});
