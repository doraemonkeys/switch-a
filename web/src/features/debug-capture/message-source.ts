import { useContext, useState } from "react";
import type {
  DebugCaptureHeaders,
  DebugCaptureRecordDetail,
  DebugCaptureRecordSummary,
} from "@/api";
import type { TestMessageBody } from "@/features/error-detection/contracts";
import { useCaptureRecord } from "./useCaptureRecord";
import { useCaptureRecords } from "./useCaptureRecords";
import {
  presentBlobPreview,
  getContentType,
  isTextualContentType,
} from "./presentation";
import { DebugCaptureContext } from "./context";

export interface DebugCaptureMessageSource {
  readonly record_id: string;
  readonly session_id: string;
  readonly provider_id: string;
  readonly provider_name: string;
  readonly api_type: string;
  readonly content_type: string;
  readonly content_encoding: string;
  readonly body: TestMessageBody;
  readonly response_status: number | null;
  readonly preview_truncated: boolean;
  readonly preview_bytes: number;
  readonly captured_bytes: number;
}

export interface DebugCaptureMessageSourceState {
  readonly session_id: string | null;
  readonly records: readonly DebugCaptureRecordSummary[];
  readonly loading: boolean;
  readonly error: Error | null;
  readonly selected_record_id: string | null;
  readonly selected_source: DebugCaptureMessageSource | null;
  readonly selected_loading: boolean;
  readonly selected_error: Error | null;
  readonly select_record: (recordId: string | null) => void;
  readonly refresh: () => Promise<void>;
}

function getHeaderValue(
  headers: DebugCaptureHeaders | undefined,
  name: string,
): string {
  const entry = Object.entries(headers ?? {}).find(
    ([headerName]) => headerName.toLowerCase() === name,
  );
  return entry?.[1]?.[0] ?? "";
}

export function toDebugCaptureMessageSource(
  detail: DebugCaptureRecordDetail,
): DebugCaptureMessageSource | null {
  if (!detail.http) return null;

  const exchange = detail.http;
  const contentType = getContentType(exchange.response?.headers ?? {});
  const contentEncoding = (
    getHeaderValue(exchange.response?.headers, "content-encoding") || "identity"
  )
    .trim()
    .toLowerCase();
  // Debug Capture retains wire bytes. Text is safe to decode only when the
  // transport was not compressed; compressed payloads must stay base64 so the
  // analyzer can apply the declared content encoding itself.
  const useTextBody =
    contentEncoding === "identity" && isTextualContentType(contentType);
  const body: TestMessageBody = useTextBody
    ? {
        encoding: "utf8",
        value: presentBlobPreview(exchange.response_body, true),
      }
    : { encoding: "base64", value: exchange.response_body.data_base64 };

  return {
    record_id: detail.summary.record_id,
    session_id: detail.summary.session_id,
    provider_id: detail.summary.provider.id,
    provider_name: detail.summary.provider.name,
    api_type: detail.summary.provider.api_type,
    content_type: contentType || "application/octet-stream",
    content_encoding: contentEncoding,
    body,
    response_status: exchange.response?.status_code ?? null,
    preview_truncated: exchange.response_body.truncated,
    preview_bytes: exchange.response_body.preview_bytes,
    captured_bytes: exchange.response_body.captured_bytes,
  };
}

export function useDebugCaptureMessageSource(): DebugCaptureMessageSourceState {
  const debugCapture = useContext(DebugCaptureContext);
  const status = debugCapture?.status ?? null;
  const sessionId = status?.session?.session_id ?? null;
  const recordsQuery = useCaptureRecords(sessionId ?? "");
  const [selectedRecordId, setSelectedRecordId] = useState<string | null>(null);
  const selectedRecord = useCaptureRecord(sessionId ?? "", selectedRecordId);

  return {
    session_id: sessionId,
    records: (recordsQuery.page?.records ?? []).filter(
      (record) => record.protocol === "http",
    ),
    loading: recordsQuery.loading,
    error: recordsQuery.error,
    selected_record_id: selectedRecordId,
    selected_source: selectedRecord.detail
      ? toDebugCaptureMessageSource(selectedRecord.detail)
      : null,
    selected_loading: selectedRecord.loading,
    selected_error: selectedRecord.error,
    select_record: setSelectedRecordId,
    refresh: recordsQuery.refetch,
  };
}
