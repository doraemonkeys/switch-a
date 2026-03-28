import { describe, expect, it } from "vitest";
import { getLogLifecyclePresentation } from "./diagnostics";
import type { RequestLog } from "../../api/types";

const baseLog: RequestLog = {
  id: 1,
  request_id: "req-1",
  provider_id: "provider-1",
  api_type: "codex",
  model: "gpt-5",
  client_ip: "127.0.0.1",
  user_id: "user-1",
  status_code: 101,
  latency_ms: 120,
  success: false,
  is_sse: false,
  is_websocket: true,
  retry_count: 0,
  is_sticky: false,
  error_msg: "websocket closed",
  created_at: "2026-03-28T00:00:00Z",
};

describe("getLogLifecyclePresentation", () => {
  it("surfaces probe outcome labels for websocket logs", () => {
    const lifecycle = getLogLifecyclePresentation({
      ...baseLog,
      session_committed: false,
      probe_outcome: "transport_failed",
      terminal_cause: "upstream_transport_error",
    });

    expect(lifecycle.probeOutcome).toBe("transport_failed");
    expect(lifecycle.probeOutcomeLabel).toBe("Transport failed");
    expect(lifecycle.tableDetailLabel).toContain("Transport failed");
  });

  it("prioritizes reconnect-required visibility semantics over commit heuristics", () => {
    const lifecycle = getLogLifecyclePresentation({
      ...baseLog,
      session_committed: false,
      client_visible: true,
      terminal_cause: "upstream_semantic_error",
      recovery_action: "reconnect_required",
    });

    expect(lifecycle.shortOutcomeLabel).toBe("Reconnect required");
    expect(lifecycle.outcomeLabel).toBe(
      "Client-visible session ended with reconnect required",
    );
    expect(lifecycle.clientVisibilityLabel).toBe("Visible");
    expect(lifecycle.recoveryActionLabel).toBe("Reconnect Required");
    expect(lifecycle.tableDetailLabel).toContain("Visible");
    expect(lifecycle.tableDetailLabel).toContain("Reconnect Required");
  });

  it("keeps probe outcome hidden for non-websocket logs", () => {
    const lifecycle = getLogLifecyclePresentation({
      ...baseLog,
      is_websocket: false,
      success: true,
      error_msg: null,
      probe_outcome: "unsupported",
    });

    expect(lifecycle.showLifecycle).toBe(false);
    expect(lifecycle.probeOutcomeLabel).toBeNull();
  });
});
