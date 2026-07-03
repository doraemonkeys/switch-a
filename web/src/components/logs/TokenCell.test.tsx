import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { NormalizedRequestLog } from "../../api/types";
import { TokenCell } from "./TokenCell";

function makeLog(
  overrides: Partial<NormalizedRequestLog> = {},
): NormalizedRequestLog {
  return {
    id: 1,
    request_id: "req-1",
    provider_id: "provider-1",
    api_type: "openai",
    model: "gpt-5",
    client_ip: "127.0.0.1",
    user_id: "user-1",
    latency_ms: 100,
    is_sse: false,
    is_websocket: false,
    retry_count: 0,
    is_sticky: false,
    created_at: "2026-07-03T00:00:00Z",
    semantics_version: "normalized_v1",
    client_transport_status_code: 200,
    completion_state: "completed",
    service_outcome: "completed",
    termination_actor: null,
    termination_reason: null,
    client_action: "none",
    session_evidence_json: null,
    prompt_tokens: 1200,
    completion_tokens: 300,
    total_tokens: 1500,
    ...overrides,
  };
}

describe("TokenCell", () => {
  it("renders reasoning tokens when reported", () => {
    render(<TokenCell log={makeLog({ reasoning_tokens: 42 })} />);

    expect(
      screen.getByTitle("Reasoning tokens included in output tokens"),
    ).toHaveTextContent("R:42");
  });

  it("keeps the reasoning slot visible when token data exists but reasoning is unknown", () => {
    render(<TokenCell log={makeLog({ reasoning_tokens: null })} />);

    expect(
      screen.getByTitle("Reasoning tokens included in output tokens"),
    ).toHaveTextContent("R:—");
  });
});
