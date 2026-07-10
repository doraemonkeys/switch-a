import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { NormalizedRequestLog } from "../../api/types";
import { LogsTable } from "./LogsTable";

function makeLog(
  overrides: Partial<NormalizedRequestLog> = {},
): NormalizedRequestLog {
  return {
    id: 1,
    request_id: "request-1",
    provider_id: "provider-1",
    api_type: "codex",
    model: "gpt-5",
    client_ip: "127.0.0.1",
    user_id: "user-1",
    semantics_version: "normalized_v1",
    client_transport_status_code: 200,
    completion_state: "completed",
    service_outcome: "completed",
    termination_actor: null,
    termination_reason: null,
    client_action: "none",
    session_evidence_json: null,
    latency_ms: 125,
    is_sse: false,
    is_websocket: false,
    retry_count: 0,
    is_sticky: false,
    created_at: "2026-07-10T10:00:00Z",
    ...overrides,
  };
}

function renderTable(logs: NormalizedRequestLog[], loading = false) {
  return render(
    <LogsTable
      logs={logs}
      loading={loading}
      sortBy="created_at"
      sortOrder="desc"
      hasActiveFilters={false}
      providerNames={new Map([["provider-1", "Provider One"]])}
      onSort={vi.fn()}
      onSelectLog={vi.fn()}
      onClearFilters={vi.fn()}
    />,
  );
}

describe("LogsTable reasoning observation", () => {
  it("renders the requested configuration column and cell", () => {
    renderTable([
      makeLog({
        reasoning_observation_state: "captured",
        reasoning_effort: "high",
      }),
    ]);

    expect(
      screen.getByRole("columnheader", { name: /Effort/i }),
    ).toBeInTheDocument();
    expect(
      screen.getByText(
        "Requested reasoning configuration sent by the client, not reasoning tokens consumed by the provider.",
      ),
    ).toBeInTheDocument();
    expect(
      screen.getByTitle(
        'Captured requested reasoning configuration. Effort: "high".',
      ),
    ).toHaveTextContent("high");
  });

  it("spans all ten columns while loading", () => {
    renderTable([], true);

    expect(screen.getByText("Loading logs...").closest("td")).toHaveAttribute(
      "colspan",
      "10",
    );
  });

  it("spans all ten columns for the empty state", () => {
    renderTable([]);

    expect(
      screen.getByText("No logs recorded yet").closest("td"),
    ).toHaveAttribute("colspan", "10");
  });
});
