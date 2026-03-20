import { describe, expect, it } from "vitest";
import type { RequestLog } from "../../api/types";
import { getLogLifecyclePresentation } from "./diagnostics";

function createMockLog(overrides?: Partial<RequestLog>): RequestLog {
  return {
    id: 1,
    request_id: "request-1",
    provider_id: "provider-1",
    api_type: "codex",
    model: "gpt-4.1",
    client_ip: "127.0.0.1",
    user_id: "user-1",
    status_code: 101,
    latency_ms: 250,
    success: true,
    is_sse: false,
    is_websocket: true,
    retry_count: 0,
    is_sticky: false,
    error_msg: null,
    created_at: "2024-01-15T10:30:00Z",
    ...overrides,
  };
}

describe("getLogLifecyclePresentation", () => {
  it("treats committed client disconnect as lifecycle noise instead of a failure badge", () => {
    const presentation = getLogLifecyclePresentation(
      createMockLog({
        success: false,
        session_committed: true,
        terminal_cause: "client_disconnect",
        error_msg: "websocket: close 1006",
      }),
    );

    expect(presentation.outcomeTone).toBe("info");
    expect(presentation.shortOutcomeLabel).toBe("Client disconnect");
    expect(presentation.commitmentLabel).toBe("Committed");
    expect(presentation.shouldShowErrorDetails).toBe(false);
  });

  it("keeps uncommitted upstream semantic errors as provider failures", () => {
    const presentation = getLogLifecyclePresentation(
      createMockLog({
        success: false,
        session_committed: false,
        terminal_cause: "upstream_semantic_error",
        error_msg: '{"error":"provider failed"}',
      }),
    );

    expect(presentation.outcomeTone).toBe("danger");
    expect(presentation.shortOutcomeLabel).toBe("No service");
    expect(presentation.commitmentLabel).toBe("Uncommitted");
    expect(presentation.terminalCauseLabel).toBe("Upstream semantic error");
    expect(presentation.shouldShowErrorDetails).toBe(true);
  });

  it("surfaces post-commit transport errors without collapsing them into failure", () => {
    const presentation = getLogLifecyclePresentation(
      createMockLog({
        session_committed: true,
        terminal_cause: "upstream_transport_error",
        commit_source: "upstream_message",
        sticky_written: true,
        error_msg: "connection reset by peer",
      }),
    );

    expect(presentation.outcomeTone).toBe("warning");
    expect(presentation.shortOutcomeLabel).toBe("Transport error");
    expect(presentation.commitSourceLabel).toBe("First upstream message");
    expect(presentation.stickyWrittenLabel).toBe("Written");
    expect(presentation.shouldShowErrorDetails).toBe(false);
  });

  it("falls back to legacy success semantics for non-websocket logs", () => {
    const presentation = getLogLifecyclePresentation(
      createMockLog({
        is_websocket: false,
        status_code: 500,
        success: false,
        error_msg: "request failed",
      }),
    );

    expect(presentation.showLifecycle).toBe(false);
    expect(presentation.outcomeTone).toBe("danger");
    expect(presentation.shortOutcomeLabel).toBe("Failed");
    expect(presentation.tableDetailLabel).toBe("Code 500");
    expect(presentation.shouldShowErrorDetails).toBe(true);
  });
});
