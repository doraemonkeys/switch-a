import { describe, expect, it } from "vitest";
import { getLogLifecyclePresentation } from "./diagnostics";
import type { LegacyRequestLog, NormalizedRequestLog } from "../../api/types";

const baseLog: NormalizedRequestLog = {
  id: 1,
  request_id: "req-1",
  provider_id: "provider-1",
  api_type: "codex",
  model: "gpt-5",
  client_ip: "127.0.0.1",
  user_id: "user-1",
  semantics_version: "normalized_v1",
  client_transport_status_code: 101,
  completion_state: "completed",
  service_outcome: "completed",
  termination_actor: null,
  termination_reason: null,
  client_action: "none",
  session_evidence_json: null,
  latency_ms: 120,
  is_sse: false,
  is_websocket: true,
  retry_count: 0,
  is_sticky: false,
  created_at: "2026-03-28T00:00:00Z",
};

const legacyLog: LegacyRequestLog = {
  ...baseLog,
  semantics_version: "legacy_pre_assessment",
  client_transport_status_code: null,
  completion_state: null,
  service_outcome: null,
  termination_actor: null,
  termination_reason: null,
  client_action: null,
  session_evidence_json: null,
};

describe("getLogLifecyclePresentation", () => {
  it("surfaces normalized service outcome, client action, and termination reason", () => {
    const lifecycle = getLogLifecyclePresentation({
      ...baseLog,
      service_outcome: "interrupted",
      client_action: "reconnect_required",
      termination_actor: "upstream",
      termination_reason: "usage_limit_reached",
      client_visible: true,
      session_committed: true,
      commit_source: "semantic_event",
    });

    expect(lifecycle.shortOutcomeLabel).toBe("Interrupted");
    expect(lifecycle.clientActionLabel).toBe("Reconnect Required");
    expect(lifecycle.terminationReasonLabel).toBe("Usage Limit Reached");
    expect(lifecycle.terminationActorLabel).toBe("Upstream");
    expect(lifecycle.clientVisibilityLabel).toBe("Visible");
    expect(lifecycle.commitmentLabel).toBe("Committed");
    expect(lifecycle.transportStatusLabel).toBe("101 Upgrade");
  });

  it("keeps non-websocket rows on normalized outcome semantics", () => {
    const lifecycle = getLogLifecyclePresentation({
      ...baseLog,
      is_websocket: false,
      client_transport_status_code: 502,
      service_outcome: "never_started",
      completion_state: "incomplete",
      termination_reason: "provider_unavailable",
    });

    expect(lifecycle.showLifecycle).toBe(false);
    expect(lifecycle.shortOutcomeLabel).toBe("Never started");
    expect(lifecycle.completionStateLabel).toBe("Incomplete");
    expect(lifecycle.terminationReasonLabel).toBe("Provider Unavailable");
    expect(lifecycle.transportStatusLabel).toBe("502");
  });

  it("renders legacy rows explicitly without normalized remapping", () => {
    const lifecycle = getLogLifecyclePresentation({
      ...legacyLog,
    });

    expect(lifecycle.isLegacy).toBe(true);
    expect(lifecycle.shortOutcomeLabel).toBe("Legacy");
    expect(lifecycle.legacyNote).toContain("without remapping");
    expect(lifecycle.clientActionLabel).toBeNull();
  });
});
