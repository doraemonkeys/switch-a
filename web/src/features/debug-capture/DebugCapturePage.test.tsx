import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { ApiContext } from "@/api/context";
import type {
  ApiClient,
  DebugCaptureFailureObservation,
  DebugCaptureRecordDetail,
  DebugCaptureRecordsPage,
  DebugCaptureSessionInfo,
  DebugCaptureStatus,
  Provider,
} from "@/api";
import { createMockApiClient } from "@/hooks/test-utils";
import { CaptureRecordDialog } from "./CaptureRecordDialog";
import { DebugCapturePage } from "./DebugCapturePage";
import { DebugCaptureProvider } from "./DebugCaptureProvider";
import { DebugCaptureContext } from "./context";

const MIB = 1_024 * 1_024;
const EXPORT_ID = "ce_AAAAAAAAAAAAAAAAAAAAAAAA";
const EXPORT_DOWNLOAD_PATH =
  "/admin/api/debug-capture/exports/ce_AAAAAAAAAAAAAAAAAAAAAAAA/download";
const DOWNLOAD_TOKEN = "A".repeat(43);

const emptyFailureObservation: DebugCaptureFailureObservation = {
  primary: {
    site: "unknown",
    peer: "unknown",
    class: "unknown",
    code: "unknown",
  },
  secondary: {
    site: "unknown",
    peer: "unknown",
    class: "unknown",
    code: "unknown",
  },
  has_secondary: false,
  truncated: false,
};

const sessionInfo: DebugCaptureSessionInfo = {
  session_id: "session-a",
  generation: 1,
  started_at: "2026-08-01T00:00:00Z",
  providers: [{ id: "provider-a", name: "Provider A" }],
  provider_ids: ["provider-a"],
  completed_records_per_provider: 10,
  retained_bytes_limit: 256 * MIB,
};

const stoppedStatus: DebugCaptureStatus = {
  state: "stopped",
  process_memory: {
    ceiling_bytes: 512 * MIB,
    charged_bytes: 0,
    retained_bytes: 0,
    pinned_bytes: 0,
    releasing_bytes: 0,
    temporary_bytes: 0,
  },
  pending_export_count: 0,
  active_download_count: 0,
  session: null,
};

const activeStatus: DebugCaptureStatus = {
  ...stoppedStatus,
  state: "active",
  process_memory: {
    ...stoppedStatus.process_memory,
    charged_bytes: 2 * MIB,
    retained_bytes: MIB,
  },
  session: {
    ...sessionInfo,
    retained_bytes: MIB,
    active_record_count: 1,
    completed_record_count: 1,
    gateway_trace_count: 1,
    evicted_record_count: 0,
    overflowed_record_count: 0,
    history_truncated_trace_count: 0,
    dropped_trace_count: 0,
    dropped_exchange_count: 0,
    dropped_transition_count: 0,
  },
};

const sessionBStatus: DebugCaptureStatus = {
  ...activeStatus,
  session: {
    ...activeStatus.session!,
    session_id: "session-b",
    generation: 2,
  },
};

const provider: Provider = {
  id: "provider-a",
  name: "Provider A",
  api_key: "",
  api_types: [],
  auth_mode: "auto",
  credential_type: "api_key",
  group_id: null,
  weight: 1,
  priority: 0,
  concurrency: 10,
  max_retries: 0,
  vendor: "",
  failover_scope: "any",
  accept_failover: "any",
  enabled: true,
  created_at: "2026-08-01T00:00:00Z",
  updated_at: "2026-08-01T00:00:00Z",
};

const recordsPage: DebugCaptureRecordsPage = {
  session_id: "session-a",
  snapshot_watermark: "watermark-a",
  records: [
    {
      session_id: "session-a",
      record_id: "record-a",
      gateway_trace_id: "gateway-trace-a",
      gateway_request_id: "gateway-a",
      exchange_index: 0,
      record_sequence: 1,
      provider: {
        id: "provider-a",
        name: "Provider A",
        api_type: "claude",
        target_url: "https://example.test/v1/messages",
      },
      protocol: "http",
      selection_mode: "initial",
      selection_source: "strategy",
      provider_attempt_index: 0,
      credential_phase: "initial",
      lifecycle_state: "active",
      source_completion: "pending",
      capture_completion: "overflowed",
      started_at: "2026-08-01T00:00:01Z",
      has_failure: false,
      failure: emptyFailureObservation,
      upstream_observed_bytes: 12,
      application_write_confirmed_bytes: 4,
    },
  ],
  gateway_traces: [
    {
      gateway_trace_id: "gateway-trace-a",
      gateway_request_id: "gateway-a",
      history_truncated_before: false,
      history_truncated_after: false,
      entries: [
        {
          kind: "transition",
          entry_id: "transition-a",
          sequence: 1,
          provider: {
            id: "provider-b",
            name: "Provider B",
            api_type: "claude",
            target_url: "https://provider-b.test",
          },
          provider_attempt_index: 0,
          selection_mode: "replacement",
          selection_source: "strategy",
          credential_phase: "initial",
          termination_reason: "capture_fault",
          has_failure: true,
          failure: {
            primary: {
              site: "transport",
              peer: "provider",
              class: "transport",
              code: "connection",
              system_error_code: 10_061,
              message:
                "provider\u0000 connection failed after partial metadata capture",
            },
            secondary: {
              site: "response_drain",
              peer: "upstream",
              class: "read",
              code: "drain_read",
              http_status_code: 502,
            },
            has_secondary: true,
            truncated: true,
          },
          metadata_truncated: true,
        },
        {
          kind: "record",
          entry_id: "record-entry-a",
          sequence: 2,
          record_id: "record-a",
          provider: {
            id: "provider-a",
            name: "Provider A",
            api_type: "claude",
            target_url: "https://example.test/v1/messages",
          },
          provider_attempt_index: 0,
          selection_mode: "initial",
          selection_source: "strategy",
          credential_phase: "initial",
          has_failure: false,
          failure: emptyFailureObservation,
          metadata_truncated: false,
        },
      ],
    },
  ],
  eviction_gap: { detected: false, record_count: 0 },
};

const recordDetail: DebugCaptureRecordDetail = {
  summary: recordsPage.records[0],
  snapshot_state: "active_partial",
  gateway_trace: recordsPage.gateway_traces[0],
  http: {
    request: {
      method: "POST",
      url: "https://example.test/v1/messages",
      host: "example.test",
      headers: { "Content-Type": ["application/json"] },
      content_length: 17,
    },
    request_body: {
      data_base64: btoa('{"hello":"world"}'),
      preview_bytes: 17,
      captured_bytes: 17,
      truncated: false,
      checksum_sha256: "request-checksum",
    },
    response: {
      status_code: 200,
      protocol: "HTTP/2.0",
      headers: { "Content-Type": ["application/json"] },
      content_length: 11,
    },
    response_body: {
      data_base64: btoa('{"ok":true}'),
      preview_bytes: 11,
      captured_bytes: 11,
      truncated: false,
      checksum_sha256: "response-checksum",
    },
  },
};

const websocketRecordDetail: DebugCaptureRecordDetail = {
  summary: {
    ...recordsPage.records[0],
    protocol: "websocket",
    lifecycle_state: "completed",
    source_completion: "partial",
    capture_completion: "complete",
    termination_reason: "websocket_relay_error",
    has_failure: true,
    failure: {
      primary: {
        site: "websocket_relay",
        peer: "client",
        class: "write",
        code: "relay_write",
        message: "relay failed after the close frame",
      },
      secondary: {
        site: "websocket_close",
        peer: "upstream",
        class: "websocket_close",
        code: "websocket_close",
        websocket_close_code: 1011,
      },
      has_secondary: true,
      truncated: true,
    },
  },
  snapshot_state: "final",
  gateway_trace: null,
  websocket: {
    request: {
      method: "GET",
      url: "wss://example.test/v1/socket",
      host: "example.test",
      headers: { Upgrade: ["websocket"] },
      content_length: 0,
    },
    request_body: {
      data_base64: "",
      preview_bytes: 0,
      captured_bytes: 0,
      truncated: false,
      checksum_sha256: "request-checksum",
    },
    handshake: {
      status_code: 101,
      protocol: "HTTP/1.1",
      headers: { Upgrade: ["websocket"] },
    },
    handshake_body: {
      data_base64: "",
      preview_bytes: 0,
      captured_bytes: 0,
      truncated: false,
      checksum_sha256: "handshake-checksum",
    },
    messages: [
      {
        message_id: "message-a",
        sequence: 1,
        relative_millis: 42,
        direction: "upstream_to_client",
        message_type: "text",
        source: "live",
        disposition: "write_failed",
        client_visible: false,
        has_failure: true,
        failure: {
          primary: {
            site: "websocket_message",
            peer: "client",
            class: "write",
            code: "message_write",
            system_error_code: 10_054,
            message: "client write failed",
          },
          secondary: emptyFailureObservation.secondary,
          has_secondary: false,
          truncated: true,
        },
        payload: {
          data_base64: btoa("partial payload"),
          preview_bytes: 15,
          captured_bytes: 15,
          truncated: false,
          checksum_sha256: "message-checksum",
        },
      },
    ],
    events_truncated: false,
    close: {
      direction: "upstream_to_client",
      code: 1011,
      reason: "upstream failure",
      reason_truncated: true,
      clean: false,
    },
  },
};

function renderDebugPage(api: ApiClient) {
  return render(
    <ApiContext.Provider value={api}>
      <DebugCaptureProvider pollIntervalMs={0}>
        <DebugCapturePage />
      </DebugCaptureProvider>
    </ApiContext.Provider>,
  );
}

async function expectInvalidGrantTokenNotRendered() {
  const user = userEvent.setup();
  const api = createMockApiClient();
  const unsafeToken = "unsafe-token-canary";
  vi.mocked(api.debugCapture.status).mockResolvedValue(activeStatus);
  vi.mocked(api.debugCapture.listRecords).mockResolvedValue(recordsPage);
  vi.mocked(api.debugCapture.createExport).mockResolvedValue({
    export_id: EXPORT_ID,
    session_id: "session-a",
    record_count: 1,
    expires_at: "2099-08-01T00:05:00Z",
    download_path: EXPORT_DOWNLOAD_PATH,
    download_token: unsafeToken,
  });

  const { container } = renderDebugPage(api);
  await user.click(
    await screen.findByRole("checkbox", {
      name: "Select record record-a",
    }),
  );
  await user.click(
    screen.getByRole("button", { name: "Prepare selected (1)" }),
  );

  expect(
    await screen.findByText("The server returned an invalid download grant."),
  ).toBeVisible();
  expect(container.querySelector('input[name="download_token"]')).toBeNull();
  expect(container.innerHTML).not.toContain(unsafeToken);
}

describe("DebugCapturePage", () => {
  it("requires scope and risk acknowledgement before starting", async () => {
    const user = userEvent.setup();
    const api = createMockApiClient();
    vi.mocked(api.debugCapture.status).mockResolvedValue(stoppedStatus);
    vi.mocked(api.providers.list).mockResolvedValue([provider]);
    vi.mocked(api.debugCapture.start).mockResolvedValue(sessionInfo);

    renderDebugPage(api);
    await screen.findByRole("heading", { name: "Debug Capture" });

    await user.click(
      screen.getByRole("button", { name: "Start Debug Capture" }),
    );
    expect(screen.getByRole("alert", { name: "" })).toHaveTextContent(
      "Select at least one Provider",
    );

    await user.click(screen.getByRole("checkbox", { name: /Provider A/i }));
    await user.click(
      screen.getByRole("button", { name: "Start Debug Capture" }),
    );
    expect(screen.getByRole("alert", { name: "" })).toHaveTextContent(
      "Acknowledge the raw payload risk",
    );

    await user.click(
      screen.getByRole("checkbox", {
        name: /I understand and accept the raw payload risk/i,
      }),
    );
    await user.click(
      screen.getByRole("button", { name: "Start Debug Capture" }),
    );

    await waitFor(() =>
      expect(api.debugCapture.start).toHaveBeenCalledWith({
        provider_ids: ["provider-a"],
        completed_records_per_provider: 10,
        retained_bytes_limit: 256 * MIB,
        acknowledge_raw_payload_risk: true,
      }),
    );
  });

  it("shows orthogonal completeness, bounded previews, and native download", async () => {
    const user = userEvent.setup();
    const api = createMockApiClient();
    vi.mocked(api.debugCapture.status).mockResolvedValue(activeStatus);
    vi.mocked(api.debugCapture.listRecords).mockResolvedValue(recordsPage);
    vi.mocked(api.debugCapture.getRecord).mockResolvedValue(recordDetail);
    vi.mocked(api.debugCapture.createExport).mockResolvedValue({
      export_id: EXPORT_ID,
      session_id: "session-a",
      record_count: 1,
      expires_at: "2099-08-01T00:05:00Z",
      download_path: EXPORT_DOWNLOAD_PATH,
      download_token: DOWNLOAD_TOKEN,
    });

    const { container } = renderDebugPage(api);

    expect(await screen.findByText("Capture active")).toBeInTheDocument();
    expect(await screen.findByText("Source: Pending")).toBeInTheDocument();
    expect(screen.getByText("Capture: Overflowed")).toBeInTheDocument();
    expect(screen.getByText("Transition only")).toBeInTheDocument();
    const transitionFailure = screen.getByRole("note", {
      name: "Transition failure context",
    });
    expect(transitionFailure).toHaveTextContent("Capture Fault");
    expect(transitionFailure).toHaveTextContent("Transport");
    expect(transitionFailure).toHaveTextContent("Provider");
    expect(transitionFailure).toHaveTextContent("Connection");
    expect(transitionFailure).toHaveTextContent("10061");
    expect(transitionFailure).toHaveTextContent(
      "provider connection failed after partial metadata capture",
    );
    expect(transitionFailure).toHaveTextContent("Secondary");
    expect(transitionFailure).toHaveTextContent("Drain Read");
    expect(transitionFailure).toHaveTextContent("502");
    expect(transitionFailure).toHaveTextContent("Failure details truncated");
    expect(transitionFailure).toHaveTextContent("Metadata truncated");

    await user.click(
      screen.getByRole("checkbox", { name: "Select record record-a" }),
    );
    await user.click(
      screen.getByRole("button", { name: "Prepare selected (1)" }),
    );

    const downloadButton = await screen.findByRole("button", {
      name: "Download NDJSON",
    });
    const form = downloadButton.closest("form");
    expect(form).not.toBeNull();
    expect(form).toHaveAttribute("method", "post");
    expect(form).toHaveAttribute("action", EXPORT_DOWNLOAD_PATH);
    const tokenInput = container.querySelector('input[name="download_token"]');
    expect(tokenInput).toHaveAttribute("value", DOWNLOAD_TOKEN);
    expect(container.querySelector(`a[href*="${DOWNLOAD_TOKEN}"]`)).toBeNull();
    const submitSpy = vi
      .spyOn(HTMLFormElement.prototype, "submit")
      .mockImplementation(() => undefined);
    expect(fireEvent.submit(form!)).toBe(false);
    expect(submitSpy).toHaveBeenCalledTimes(1);
    submitSpy.mockRestore();
    await waitFor(() => {
      expect(
        screen.queryByRole("button", { name: "Download NDJSON" }),
      ).not.toBeInTheDocument();
    });

    await user.click(
      screen.getByRole("button", { name: "View record record-a" }),
    );
    const dialog = await screen.findByRole("dialog", {
      name: "Captured exchange preview",
    });
    expect(within(dialog).getByText("Snapshot: Active Partial")).toBeVisible();
    expect(within(dialog).getByText('{"hello":"world"}')).toBeVisible();
    expect(within(dialog).getByText('{"ok":true}')).toBeVisible();
  });

  it("never renders a token from an invalid runtime grant", async () => {
    await expectInvalidGrantTokenNotRendered();
  });

  it("exposes WebSocket close and truncated failure diagnostics accessibly", () => {
    render(
      <CaptureRecordDialog
        recordId="record-a"
        detail={websocketRecordDetail}
        loading={false}
        error={null}
        onClose={vi.fn()}
        onRefresh={vi.fn()}
      />,
    );

    const dialog = screen.getByRole("dialog", {
      name: "Captured exchange preview",
    });
    const recordFailure = within(dialog).getByRole("note", {
      name: "Record failure context",
    });
    expect(recordFailure).toHaveTextContent("Websocket Relay Error");
    expect(recordFailure).toHaveTextContent(
      "relay failed after the close frame",
    );
    expect(recordFailure).toHaveTextContent("Websocket Relay");
    expect(recordFailure).toHaveTextContent("Relay Write");
    expect(recordFailure).toHaveTextContent("WebSocket close code:1011");
    expect(recordFailure).toHaveTextContent("Failure details truncated");

    const messageFailure = within(dialog).getByRole("note", {
      name: "Message message-a failure context",
    });
    expect(messageFailure).toHaveTextContent("client write failed");
    expect(messageFailure).toHaveTextContent("Message Write");
    expect(messageFailure).toHaveTextContent("10054");
    expect(messageFailure).toHaveTextContent("Failure details truncated");
    expect(within(dialog).getByText("Not client visible")).toBeVisible();

    const close = within(dialog).getByRole("region", {
      name: "WebSocket close",
    });
    expect(close).toHaveTextContent("1011");
    expect(close).toHaveTextContent("upstream failure");
    expect(close).toHaveTextContent("Reason truncated");
    expect(close).toHaveTextContent("CleanNo");
  });

  it("uses the internal trace identity when gateway request IDs repeat", async () => {
    const api = createMockApiClient();
    const trace = recordsPage.gateway_traces[0];
    const repeatedRequestPage: DebugCaptureRecordsPage = {
      ...recordsPage,
      gateway_traces: [
        trace,
        {
          ...trace,
          gateway_trace_id: "gateway-trace-b",
          entries: [],
        },
      ],
    };
    vi.mocked(api.debugCapture.status).mockResolvedValue(activeStatus);
    vi.mocked(api.debugCapture.listRecords).mockResolvedValue(
      repeatedRequestPage,
    );

    renderDebugPage(api);

    const firstTrace = await screen.findByRole("region", {
      name: "Gateway trace gateway-trace-a",
    });
    const secondTrace = screen.getByRole("region", {
      name: "Gateway trace gateway-trace-b",
    });
    expect(
      within(firstTrace).getByText("gateway-a", { selector: "span" }),
    ).toBeVisible();
    expect(
      within(secondTrace).getByText("gateway-a", { selector: "span" }),
    ).toBeVisible();
  });

  it("does not confuse a cross-page trace reference with eviction", async () => {
    const api = createMockApiClient();
    const trace = recordsPage.gateway_traces[0];
    const recordEntry = trace.entries.find((entry) => entry.kind === "record")!;
    const crossPageRecords: DebugCaptureRecordsPage = {
      ...recordsPage,
      gateway_traces: [
        {
          ...trace,
          entries: [
            ...trace.entries,
            {
              ...recordEntry,
              entry_id: "record-entry-next-page",
              sequence: 3,
              record_id: "record-next-page",
            },
          ],
        },
      ],
    };
    vi.mocked(api.debugCapture.status).mockResolvedValue(activeStatus);
    vi.mocked(api.debugCapture.listRecords).mockResolvedValue(crossPageRecords);

    renderDebugPage(api);

    expect(await screen.findByText("Record outside this page")).toBeVisible();
    expect(screen.getByText("record-next-page")).toBeVisible();
    expect(
      screen.getByText(/summary is not included in the current page/i),
    ).toBeVisible();
    expect(
      screen.queryByText(
        "Record record-next-page was evicted from this snapshot.",
      ),
    ).not.toBeInTheDocument();
  });

  it("uses the visible session ID and explains destructive Stop behavior", async () => {
    const user = userEvent.setup();
    const api = createMockApiClient();
    vi.mocked(api.debugCapture.status).mockResolvedValue(activeStatus);
    vi.mocked(api.debugCapture.listRecords).mockResolvedValue(recordsPage);
    vi.mocked(api.debugCapture.stop).mockResolvedValue();

    renderDebugPage(api);
    await screen.findByText("Capture active");
    await user.click(screen.getByRole("button", { name: "Stop" }));

    const dialog = screen.getByRole("dialog", {
      name: "Stop and clear Debug Capture?",
    });
    expect(dialog).toHaveTextContent(
      "All records from this session become immediately unavailable",
    );
    expect(dialog).toHaveTextContent(
      "Pending exports and active downloads are canceled",
    );
    expect(dialog).toHaveTextContent(
      "Requests already passing through the proxy continue normally",
    );

    await user.click(
      within(dialog).getByRole("button", { name: "Stop and clear" }),
    );
    await waitFor(() =>
      expect(api.debugCapture.stop).toHaveBeenCalledWith("session-a"),
    );
    expect(
      await screen.findByRole("button", { name: "Start Debug Capture" }),
    ).toBeInTheDocument();
  });

  it("does not let a completed Stop for session A clear visible session B", async () => {
    const user = userEvent.setup();
    const api = createMockApiClient();
    let completeStop: (() => void) | undefined;
    const pendingStop = new Promise<void>((resolve) => {
      completeStop = resolve;
    });
    vi.mocked(api.debugCapture.status)
      .mockResolvedValueOnce(activeStatus)
      .mockResolvedValue(sessionBStatus);
    vi.mocked(api.debugCapture.listRecords).mockImplementation(
      async (sessionId) => ({ ...recordsPage, session_id: sessionId }),
    );
    vi.mocked(api.debugCapture.stop).mockReturnValue(pendingStop);

    renderDebugPage(api);
    await screen.findByText("session-a");
    await user.click(screen.getByRole("button", { name: "Stop" }));
    await user.click(screen.getByRole("button", { name: "Stop and clear" }));
    await waitFor(() =>
      expect(api.debugCapture.stop).toHaveBeenCalledWith("session-a"),
    );

    await user.click(screen.getByRole("button", { name: "Refresh status" }));
    await screen.findByText("session-b");

    await act(async () => {
      completeStop?.();
      await pendingStop;
    });

    expect(screen.getByText("session-b")).toBeVisible();
    expect(
      screen.queryByRole("button", { name: "Start Debug Capture" }),
    ).not.toBeInTheDocument();
  });

  it("discards page-local capabilities when the session identity changes", async () => {
    const user = userEvent.setup();
    const api = createMockApiClient();
    vi.mocked(api.debugCapture.listRecords).mockResolvedValue(recordsPage);
    vi.mocked(api.debugCapture.createExport).mockResolvedValue({
      export_id: EXPORT_ID,
      session_id: "session-a",
      record_count: 1,
      expires_at: "2099-08-01T00:05:00Z",
      download_path: EXPORT_DOWNLOAD_PATH,
      download_token: DOWNLOAD_TOKEN,
    });
    const contextValue = (status: DebugCaptureStatus) => ({
      status,
      loading: false,
      error: null,
      operation: null,
      refreshStatus: vi.fn(),
      startCapture: vi.fn(),
      stopCapture: vi.fn(),
    });
    const view = (status: DebugCaptureStatus) => (
      <ApiContext.Provider value={api}>
        <DebugCaptureContext.Provider value={contextValue(status)}>
          <DebugCapturePage />
        </DebugCaptureContext.Provider>
      </ApiContext.Provider>
    );
    const { rerender } = render(view(activeStatus));

    await user.click(
      await screen.findByRole("checkbox", {
        name: "Select record record-a",
      }),
    );
    await user.click(
      screen.getByRole("button", { name: "Prepare selected (1)" }),
    );
    expect(
      await screen.findByRole("button", { name: "Download NDJSON" }),
    ).toBeInTheDocument();

    rerender(view(sessionBStatus));
    await waitFor(() =>
      expect(api.debugCapture.listRecords).toHaveBeenCalledWith("session-b", {
        limit: 50,
      }),
    );
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Refresh" })).toBeEnabled(),
    );

    expect(
      screen.queryByRole("button", { name: "Download NDJSON" }),
    ).not.toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Prepare selected (0)" }),
    ).toBeDisabled();
  });
});
