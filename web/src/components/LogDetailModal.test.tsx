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
    // Gateway terminal_message_snippet is now also surfaced by the shared
    // EvidenceSummaryLine beneath the status badges, so the same text renders
    // twice — once in the summary, once in the evidence panel.
    expect(
      screen.getAllByText("gateway timeout after upgrade").length,
    ).toBeGreaterThan(0);
    expect(screen.getByRole("note")).toHaveTextContent(
      "gateway timeout after upgrade",
    );
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

  it("renders v2 transport evidence with formatted summary, kind, signal, and stage", () => {
    // SSE idle timeout before payload visible — the v2 renderer must surface
    // the structured `{source} {kind} ({signal}) {stage-phrase}` summary even
    // when the raw `error` channel is empty.
    const log = createMockLog({
      is_websocket: false,
      is_sse: true,
      client_transport_status_code: 200,
      service_outcome: "interrupted",
      completion_state: "incomplete",
      termination_reason: "transport_error",
      session_evidence_json: JSON.stringify({
        v: 2,
        transport: {
          source: "upstream",
          stage: "pre_payload_visible",
          kind: "timeout",
          signal: "sse_idle_timeout",
          raw_error_snippet: "sse idle watchdog fired",
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

    // Summary line rendered with role="note" so assistive tech can attach it
    // to the status header.
    expect(screen.getByRole("note")).toHaveTextContent(
      "upstream timeout (sse_idle_timeout) before payload visible",
    );

    // Detail view carries the structured fields from the v2 renderer.
    expect(screen.getByText("Signal")).toBeInTheDocument();
    expect(screen.getByText("sse_idle_timeout")).toBeInTheDocument();
    expect(screen.getByText("Kind")).toBeInTheDocument();
    expect(screen.getByText("timeout")).toBeInTheDocument();
    expect(screen.getByText("Stage")).toBeInTheDocument();
    expect(screen.getByText("before payload visible")).toBeInTheDocument();
    expect(screen.getByText("sse idle watchdog fired")).toBeInTheDocument();

    // v2 schema must not surface the v1-only "Timeout" / "Client Cancel"
    // toggles — routing is by `evidence.v`, not heuristic field probing.
    expect(screen.queryByText("Timeout")).not.toBeInTheDocument();
    expect(screen.queryByText("Client Cancel")).not.toBeInTheDocument();
  });

  it("renders WebSocket v2 close evidence with close code and reason", () => {
    const log = createMockLog({
      session_evidence_json: JSON.stringify({
        v: 2,
        transport: {
          source: "upstream",
          stage: "post_payload_visible",
          kind: "disconnect",
          signal: "close_error",
          close_code: 1011,
          close_reason_snippet: "server overloaded",
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

    expect(screen.getByRole("note")).toHaveTextContent(
      "upstream disconnect (close_error) after payload visible",
    );
    expect(screen.getByText("Close Code")).toBeInTheDocument();
    expect(screen.getByText("1011")).toBeInTheDocument();
    expect(screen.getByText("server overloaded")).toBeInTheDocument();
  });

  it("routes v1 (schema-missing) payloads through the v1 renderer", () => {
    // This guards the schema-coexistence acceptance criterion: historical
    // evidence still renders correctly under the v1 path.
    const log = createMockLog({
      session_evidence_json: JSON.stringify({
        transport: {
          source: "gateway",
          message_snippet: "peer reset the connection",
          is_timeout: false,
          is_client_cancel: false,
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

    // Summary line + Transport message_snippet panel both render the text;
    // this also documents that v1 data flows through `getLogEvidenceSummary`'s
    // v1 branch (message_snippet takes precedence).
    expect(
      screen.getAllByText("peer reset the connection").length,
    ).toBeGreaterThan(0);
    expect(screen.getByRole("note")).toHaveTextContent(
      "peer reset the connection",
    );
    // v1 renderer shows the old boolean toggles.
    expect(screen.getByText("Timeout")).toBeInTheDocument();
    expect(screen.getByText("Client Cancel")).toBeInTheDocument();
    // v2-only labels must not appear on a v1 row.
    expect(screen.queryByText("Signal")).not.toBeInTheDocument();
    expect(screen.queryByText("Stage")).not.toBeInTheDocument();
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

  it("keeps reasoning observation, effort, mode, and budget distinct", () => {
    const log = createMockLog({
      reasoning_observation_state: "captured",
      reasoning_effort: "high",
      reasoning_mode: "enabled",
      reasoning_budget_tokens: 4096,
    });

    render(
      <LogDetailModal
        log={log}
        providerName="Provider One"
        onClose={mockOnClose}
      />,
    );

    expect(screen.getByText("Requested Reasoning")).toBeInTheDocument();
    expect(screen.getByText("Observation")).toBeInTheDocument();
    expect(screen.getByText("Captured")).toBeInTheDocument();
    expect(screen.getByText("Effort")).toBeInTheDocument();
    expect(screen.getByText("high")).toBeInTheDocument();
    expect(screen.getByText("Thinking Mode")).toBeInTheDocument();
    expect(screen.getByText("enabled")).toBeInTheDocument();
    expect(screen.getByText("Thinking Budget")).toBeInTheDocument();
    expect(screen.getByText("4096 tokens")).toBeInTheDocument();
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
