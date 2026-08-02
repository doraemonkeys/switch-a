import type {
  CreateDebugCaptureExportRequest,
  DebugCaptureDownloadGrant,
  DebugCaptureRecordDetail,
  DebugCaptureRecordsPage,
  DebugCaptureRecordsQuery,
  DebugCaptureSessionInfo,
  DebugCaptureStatus,
  StartDebugCaptureRequest,
} from "./debug-capture-types";

export type AuthenticatedRequest = <T>(
  endpoint: string,
  options?: RequestInit,
) => Promise<T>;

const DEBUG_CAPTURE_BASE_PATH = "/debug-capture";

export function buildDebugCaptureRecordsQuery(
  query: DebugCaptureRecordsQuery = {},
): string {
  const search = new URLSearchParams();
  if (query.limit !== undefined) {
    search.set("limit", String(query.limit));
  }
  if (query.cursor) {
    search.set("cursor", query.cursor);
  }
  if (query.snapshot_watermark) {
    search.set("snapshot_watermark", query.snapshot_watermark);
  }
  return search.toString();
}

/**
 * Keeps the capability-token download outside the JSON fetch transport so the
 * browser, rather than JavaScript memory, owns the streamed response.
 */
export function createDebugCaptureApi(request: AuthenticatedRequest) {
  return {
    status: () =>
      request<DebugCaptureStatus>(`${DEBUG_CAPTURE_BASE_PATH}/status`),
    start: (input: StartDebugCaptureRequest) =>
      request<DebugCaptureSessionInfo>(`${DEBUG_CAPTURE_BASE_PATH}/sessions`, {
        method: "POST",
        body: JSON.stringify(input),
      }),
    stop: (sessionId: string) =>
      request<void>(
        `${DEBUG_CAPTURE_BASE_PATH}/sessions/${encodeURIComponent(sessionId)}`,
        { method: "DELETE" },
      ),
    listRecords: (sessionId: string, query: DebugCaptureRecordsQuery = {}) => {
      const search = buildDebugCaptureRecordsQuery(query);
      const path =
        `${DEBUG_CAPTURE_BASE_PATH}/sessions/${encodeURIComponent(sessionId)}/records` +
        (search ? `?${search}` : "");
      return request<DebugCaptureRecordsPage>(path);
    },
    getRecord: (sessionId: string, recordId: string) =>
      request<DebugCaptureRecordDetail>(
        `${DEBUG_CAPTURE_BASE_PATH}/sessions/${encodeURIComponent(sessionId)}/records/${encodeURIComponent(recordId)}`,
      ),
    createExport: (sessionId: string, input: CreateDebugCaptureExportRequest) =>
      request<DebugCaptureDownloadGrant>(
        `${DEBUG_CAPTURE_BASE_PATH}/sessions/${encodeURIComponent(sessionId)}/exports`,
        {
          method: "POST",
          body: JSON.stringify(input),
        },
      ),
  };
}

export type DebugCaptureApi = ReturnType<typeof createDebugCaptureApi>;
