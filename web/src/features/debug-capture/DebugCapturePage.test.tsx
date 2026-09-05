import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { ApiContext } from "@/api/context";
import type {
  ApiClient,
  DebugCaptureRecordsPage,
  DebugCaptureStatus,
} from "@/api";
import { createMockApiClient } from "@/hooks/test-utils";
import { CaptureRecordDialog } from "./CaptureRecordDialog";
import { DebugCapturePage } from "./DebugCapturePage";
import {
  MIB,
  EXPORT_ID,
  EXPORT_DOWNLOAD_PATH,
  EXPORT_DOWNLOAD_URL,
  sessionInfo,
  stoppedStatus,
  activeStatus,
  sessionBStatus,
  provider,
  recordsPage,
  recordDetail,
  websocketRecordDetail,
} from "./DebugCapturePage.fixtures";
import { DebugCaptureProvider } from "./DebugCaptureProvider";
import { DebugCaptureContext } from "./context";

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
    download_url: `${EXPORT_DOWNLOAD_PATH}?download_token=${unsafeToken}`,
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
      download_url: EXPORT_DOWNLOAD_URL,
    });

    renderDebugPage(api);

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

    const downloadLink = await screen.findByRole("link", {
      name: "Download NDJSON",
    });
    expect(downloadLink).toHaveAttribute("href", EXPORT_DOWNLOAD_URL);
    expect(downloadLink).toHaveAttribute(
      "download",
      `switch-a-debug-capture-${EXPORT_ID}.ndjson`,
    );
    expect(downloadLink).toHaveAttribute("referrerpolicy", "no-referrer");

    await user.click(
      screen.getByRole("button", { name: "View record record-a" }),
    );
    const dialog = await screen.findByRole("dialog", {
      name: "Captured exchange preview",
    });
    expect(within(dialog).getByText("Snapshot: Active Partial")).toBeVisible();
    expect(within(dialog).getByText('{"hello":"world"}')).toBeVisible();
    expect(within(dialog).getByText('{"ok":true}')).toBeVisible();
    const input = within(dialog).getByRole("region", {
      name: "Original client input",
    });
    expect(input).toHaveTextContent("Pending");
    expect(input).toHaveTextContent("Unknown");
    expect(input).toHaveTextContent("chunked");
    expect(input).toHaveTextContent("X-End");
  });

  it("never renders a token from an invalid runtime grant", async () => {
    await expectInvalidGrantTokenNotRendered();
  });
});

describe("Captured exchange details", () => {
  it("keeps input completion visible beside a later replay failure", () => {
    const detail = structuredClone(recordDetail);
    const ingress = detail.http!.request.ingress!;
    ingress.state = "complete";
    ingress.source_failure = { kind: "storage", reason: "disk read failed" };
    render(
      <CaptureRecordDialog
        recordId="record-a"
        detail={detail}
        loading={false}
        error={null}
        onClose={vi.fn()}
        onRefresh={vi.fn()}
      />,
    );
    const input = screen.getByRole("region", { name: "Original client input" });
    expect(input).toHaveTextContent("complete");
    expect(input).toHaveTextContent("Source / replay failure");
    expect(input).toHaveTextContent("storage");
    expect(input).toHaveTextContent("disk read failed");
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
});

describe("DebugCapturePage session history", () => {
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
      download_url: EXPORT_DOWNLOAD_URL,
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
      await screen.findByRole("link", { name: "Download NDJSON" }),
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
      screen.queryByRole("link", { name: "Download NDJSON" }),
    ).not.toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Prepare selected (0)" }),
    ).toBeDisabled();
  });
});

describe("DebugCapturePage exports", () => {
  it("bundles two selected records into one retryable download", async () => {
    const user = userEvent.setup();
    const api = createMockApiClient();
    const firstTrace = recordsPage.gateway_traces[0];
    const firstRecordEntry = firstTrace.entries.find(
      (entry) => entry.kind === "record",
    )!;
    vi.mocked(api.debugCapture.status).mockResolvedValue(activeStatus);
    vi.mocked(api.debugCapture.listRecords).mockResolvedValue({
      ...recordsPage,
      records: [
        recordsPage.records[0],
        {
          ...recordsPage.records[0],
          record_id: "record-b",
          record_sequence: 2,
        },
      ],
      gateway_traces: [
        {
          ...firstTrace,
          entries: [
            ...firstTrace.entries,
            {
              ...firstRecordEntry,
              entry_id: "record-entry-b",
              sequence: 3,
              record_id: "record-b",
            },
          ],
        },
      ],
    });
    vi.mocked(api.debugCapture.createExport).mockResolvedValue({
      export_id: EXPORT_ID,
      session_id: "session-a",
      record_count: 2,
      expires_at: "2099-08-01T00:05:00Z",
      download_url: EXPORT_DOWNLOAD_URL,
    });

    renderDebugPage(api);
    await user.click(
      await screen.findByRole("checkbox", {
        name: "Select record record-a",
      }),
    );
    await user.click(
      screen.getByRole("checkbox", { name: "Select record record-b" }),
    );
    await user.click(
      screen.getByRole("button", { name: "Prepare selected (2)" }),
    );

    await waitFor(() =>
      expect(api.debugCapture.createExport).toHaveBeenCalledWith("session-a", {
        scope: "records",
        record_ids: ["record-a", "record-b"],
      }),
    );
    expect(
      screen.getAllByRole("link", { name: "Download NDJSON" }),
    ).toHaveLength(1);
    expect(
      screen.getByText(/Ready to download 2 selected records/),
    ).toBeVisible();
  });
});
