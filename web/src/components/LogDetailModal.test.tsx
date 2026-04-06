import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { LegacyRequestLog, NormalizedRequestLog } from "../api/types";
import { LogDetailModal } from "./LogDetailModal";

const TEST_CLIENT_IP = ["198", "51", "100", "10"].join(".");

function createMockLog(
  overrides?: Partial<NormalizedRequestLog>,
): NormalizedRequestLog {
  return {
    id: 1,
    request_id: "request-1",
    provider_id: "provider-1",
    api_type: "claude",
    model: "claude-3-7-sonnet",
    client_ip: TEST_CLIENT_IP,
    user_id: "user-1",
    semantics_version: "normalized_v1",
    client_transport_status_code: 101,
    completion_state: "completed",
    service_outcome: "completed",
    termination_actor: null,
    termination_reason: null,
    client_action: "none",
    session_evidence_json: null,
    latency_ms: 150,
    is_sse: false,
    is_websocket: true,
    retry_count: 0,
    is_sticky: false,
    created_at: "2026-04-05T10:30:00Z",
    ...overrides,
  };
}

function createLegacyMockLog(
  overrides?: Partial<LegacyRequestLog>,
): LegacyRequestLog {
  return {
    ...createMockLog(),
    semantics_version: "legacy_pre_assessment",
    client_transport_status_code: null,
    completion_state: null,
    service_outcome: null,
    termination_actor: null,
    termination_reason: null,
    client_action: null,
    session_evidence_json: null,
    ...overrides,
  };
}

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
        providerName="Provider One"
        onClose={mockOnClose}
      />,
    );

    expect(container.firstChild).toBeNull();
  });

  it("renders normalized service outcome, client action, termination reason, and transport code badges", () => {
    const log = createMockLog({
      service_outcome: "interrupted",
      client_action: "reconnect_required",
      termination_actor: "upstream",
      termination_reason: "usage_limit_reached",
      completion_state: "incomplete",
      client_visible: true,
      session_committed: true,
      commit_source: "semantic_event",
    });

    render(
      <LogDetailModal
        log={log}
        providerName="Provider One"
        onClose={mockOnClose}
      />,
    );

    expect(screen.getByText("Interrupted")).toBeInTheDocument();
    expect(screen.getAllByText("Reconnect Required").length).toBeGreaterThan(0);
    expect(screen.getAllByText("Usage Limit Reached").length).toBeGreaterThan(
      0,
    );
    expect(screen.getAllByText("101 Upgrade").length).toBeGreaterThan(0);
    expect(screen.getByText("Semantics Version")).toBeInTheDocument();
    expect(screen.getByText("Termination Actor")).toBeInTheDocument();
    expect(screen.getByText("Upstream")).toBeInTheDocument();
    expect(screen.queryByText("Sticky Write")).not.toBeInTheDocument();
  });

  it("renders structured session evidence from session_evidence_json", () => {
    const log = createMockLog({
      session_evidence_json: JSON.stringify({
        gateway: {
          terminal_status_code: 502,
          terminal_error_code: "gateway_timeout",
          terminal_message_snippet: "gateway timeout after upgrade",
        },
        transport: {
          source: "gateway",
          message_snippet: "connection closed",
          is_timeout: true,
        },
        upstream_event: {
          provider_error_code: "usage_limit_reached",
          message_snippet: "provider quota exhausted",
        },
      }),
    });

    render(
      <LogDetailModal
        log={log}
        providerName="Provider One"
        onClose={mockOnClose}
      />,
    );

    expect(screen.getByText("Session Evidence")).toBeInTheDocument();
    expect(screen.getByText("Gateway")).toBeInTheDocument();
    expect(screen.getByText("gateway_timeout")).toBeInTheDocument();
    expect(
      screen.getByText("gateway timeout after upgrade"),
    ).toBeInTheDocument();
    expect(screen.getByText("Transport")).toBeInTheDocument();
    expect(screen.getByText("connection closed")).toBeInTheDocument();
    expect(screen.getByText("Upstream Event")).toBeInTheDocument();
    expect(screen.getByText("provider quota exhausted")).toBeInTheDocument();
  });

  it("renders attempt evidence in the timeline", () => {
    const log = createMockLog({
      is_websocket: false,
      client_transport_status_code: 502,
      service_outcome: "never_started",
      completion_state: "incomplete",
      termination_reason: "provider_unavailable",
      attempts: [
        {
          id: 1,
          request_id: "request-1",
          provider_id: "provider-1",
          semantics_version: "normalized_v1",
          attempt: 0,
          status_code: 502,
          error: "provider unavailable",
          attempt_evidence_json: JSON.stringify({
            upstream_handshake: {
              status_code: 503,
              body_snippet: "upstream unavailable",
            },
          }),
          latency_ms: 80,
          created_at: "2026-04-05T10:30:00Z",
        },
      ],
    });

    render(
      <LogDetailModal
        log={log}
        providerName="Provider One"
        onClose={mockOnClose}
      />,
    );

    expect(screen.getByText("Request Attempts")).toBeInTheDocument();
    expect(screen.getByText("Structured Evidence")).toBeInTheDocument();
    expect(screen.getByText("Upstream Handshake")).toBeInTheDocument();
    expect(screen.getByText("upstream unavailable")).toBeInTheDocument();
  });

  it("renders legacy rows explicitly without normalized remapping", () => {
    const log = createLegacyMockLog();

    render(
      <LogDetailModal
        log={log}
        providerName="Provider One"
        onClose={mockOnClose}
      />,
    );

    expect(screen.getAllByText("Legacy").length).toBeGreaterThan(0);
    expect(
      screen.getByText(/rendered as legacy without remapping/i),
    ).toBeInTheDocument();
    expect(screen.queryByText("Reconnect Required")).not.toBeInTheDocument();
  });

  it("closes on escape", () => {
    render(
      <LogDetailModal
        log={createMockLog()}
        providerName="Provider One"
        onClose={mockOnClose}
      />,
    );

    fireEvent.keyDown(document, { key: "Escape" });

    expect(mockOnClose).toHaveBeenCalledTimes(1);
  });
});
