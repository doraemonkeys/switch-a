import { useState } from "react";
import { useApi, type DebugCaptureRecordsQuery } from "@/api";
import { usePollingQuery } from "@/hooks/usePollingQuery";

const RECORDS_POLL_INTERVAL_MS = 3_000;
const RECORDS_PAGE_SIZE = 50;

export function useCaptureRecords(sessionId: string) {
  const api = useApi();
  const [pageStack, setPageStack] = useState<DebugCaptureRecordsQuery[]>([
    { limit: RECORDS_PAGE_SIZE },
  ]);
  const pageRequest = pageStack.at(-1)!;
  const isLatestPage = pageStack.length === 1;

  const query = usePollingQuery(
    () => api.debugCapture.listRecords(sessionId, pageRequest),
    {
      intervalMs: isLatestPage ? RECORDS_POLL_INTERVAL_MS : 0,
      enabled: Boolean(sessionId),
      queryKey: pageRequest,
      errorMessage: "Failed to fetch captured records",
    },
  );

  const showNextPage = () => {
    const cursor = query.data?.next_cursor;
    const snapshotWatermark = query.data?.snapshot_watermark;
    if (!cursor || !snapshotWatermark) return;
    setPageStack((current) => [
      ...current,
      {
        limit: RECORDS_PAGE_SIZE,
        cursor,
        snapshot_watermark: snapshotWatermark,
      },
    ]);
  };

  const showPreviousPage = () => {
    setPageStack((current) =>
      current.length > 1 ? current.slice(0, -1) : current,
    );
  };

  const showLatestPage = () => {
    setPageStack([{ limit: RECORDS_PAGE_SIZE }]);
  };

  return {
    ...query,
    page: query.data,
    pageNumber: pageStack.length,
    isLatestPage,
    showNextPage,
    showPreviousPage,
    showLatestPage,
  };
}
