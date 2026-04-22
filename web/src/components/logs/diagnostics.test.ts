import { describe, expect, it } from "vitest";
import {
  getLogEvidenceSummary,
  getLogLifecyclePresentation,
  getLogTransportSummary,
} from "./diagnostics";
import {
  formatTransportSummary,
  getTransportStagePhrase,
  isV2Evidence,
  parseRequestEvidence,
} from "./evidence-utils";
import type {
  LegacyRequestLog,
  NormalizedRequestLog,
  RequestEvidenceTransportV2,
} from "../../api/types";

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

describe("formatTransportSummary (v2)", () => {
  const build = (
    overrides: Partial<RequestEvidenceTransportV2> = {},
  ): RequestEvidenceTransportV2 => ({
    source: "upstream",
    stage: "pre_payload_visible",
    kind: "timeout",
    signal: "sse_idle_timeout",
    ...overrides,
  });

  it("formats SSE idle timeout before payload visible", () => {
    expect(formatTransportSummary(build())).toBe(
      "upstream timeout (sse_idle_timeout) before payload visible",
    );
  });

  it("formats WS close_error after payload visible", () => {
    expect(
      formatTransportSummary(
        build({
          stage: "post_payload_visible",
          kind: "disconnect",
          signal: "close_error",
          close_code: 1011,
        }),
      ),
    ).toBe("upstream disconnect (close_error) after payload visible");
  });

  it("formats pre_connection_visible SSE timeout", () => {
    expect(
      formatTransportSummary(
        build({
          stage: "pre_connection_visible",
          signal: "sse_idle_timeout",
        }),
      ),
    ).toBe("upstream timeout (sse_idle_timeout) before connection");
  });

  it("omits the stage phrase when stage is missing", () => {
    expect(
      formatTransportSummary(
        build({ stage: undefined, signal: "upstream_read_error", kind: "protocol_error" }),
      ),
    ).toBe("upstream protocol_error (upstream_read_error)");
  });

  it("returns null when the signal is excluded from list summaries", () => {
    // close_without_status stays in the detail view but must never be a
    // first-choice list summary.
    expect(
      formatTransportSummary(
        build({
          kind: "disconnect",
          signal: "close_without_status",
          stage: "post_payload_visible",
        }),
      ),
    ).toBeNull();
  });

  it("returns null when core fields are missing", () => {
    expect(formatTransportSummary(null)).toBeNull();
    expect(formatTransportSummary({})).toBeNull();
    expect(formatTransportSummary({ source: "upstream" })).toBeNull();
  });
});

describe("getTransportStagePhrase", () => {
  it("maps each of the three states", () => {
    expect(getTransportStagePhrase("pre_connection_visible")).toBe(
      "before connection",
    );
    expect(getTransportStagePhrase("pre_payload_visible")).toBe(
      "before payload visible",
    );
    expect(getTransportStagePhrase("post_payload_visible")).toBe(
      "after payload visible",
    );
  });

  it("returns null for missing stage", () => {
    expect(getTransportStagePhrase(undefined)).toBeNull();
  });
});

describe("isV2Evidence + getLogEvidenceSummary routing", () => {
  const withEvidence = (
    evidenceJson: string,
  ): NormalizedRequestLog => ({
    ...baseLog,
    session_evidence_json: evidenceJson,
  });

  it("detects v:2 envelopes and lets the transport summary flow through", () => {
    const evidenceJson = JSON.stringify({
      v: 2,
      transport: {
        source: "upstream",
        stage: "post_payload_visible",
        kind: "disconnect",
        signal: "close_error",
      },
    });

    expect(isV2Evidence(parseRequestEvidence(evidenceJson))).toBe(true);
    expect(getLogEvidenceSummary(withEvidence(evidenceJson))).toBe(
      "upstream disconnect (close_error) after payload visible",
    );
    expect(getLogTransportSummary(withEvidence(evidenceJson))).toBe(
      "upstream disconnect (close_error) after payload visible",
    );
  });

  it("prefers gateway terminal text over the transport-formatted summary", () => {
    const evidenceJson = JSON.stringify({
      v: 2,
      gateway: { terminal_message_snippet: "gateway cut the request" },
      transport: {
        source: "upstream",
        stage: "pre_payload_visible",
        kind: "timeout",
        signal: "sse_idle_timeout",
      },
    });

    expect(getLogEvidenceSummary(withEvidence(evidenceJson))).toBe(
      "gateway cut the request",
    );
  });

  it("falls back to raw_error_snippet when the signal is list-suppressed", () => {
    const evidenceJson = JSON.stringify({
      v: 2,
      transport: {
        source: "client",
        stage: "post_payload_visible",
        kind: "disconnect",
        signal: "close_without_status",
        raw_error_snippet: "client closed without status",
      },
    });

    expect(getLogEvidenceSummary(withEvidence(evidenceJson))).toBe(
      "client closed without status",
    );
    expect(getLogTransportSummary(withEvidence(evidenceJson))).toBeNull();
  });

  it("renders v1 transport message_snippet through the v1 path unchanged", () => {
    const evidenceJson = JSON.stringify({
      transport: {
        source: "upstream",
        message_snippet: "connection reset by peer",
        is_timeout: false,
      },
    });

    expect(isV2Evidence(parseRequestEvidence(evidenceJson))).toBe(false);
    expect(getLogEvidenceSummary(withEvidence(evidenceJson))).toBe(
      "connection reset by peer",
    );
    // v1 payloads must not leak into the v2-only transport-summary helper.
    expect(getLogTransportSummary(withEvidence(evidenceJson))).toBeNull();
  });
});
