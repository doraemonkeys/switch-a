import { useState, type ReactNode } from "react";
import { useApi } from "@/api";
import { usePollingQuery } from "@/hooks/usePollingQuery";
import { DebugCaptureContext, type DebugCaptureOperation } from "./context";

const STATUS_POLL_INTERVAL_MS = 5_000;

interface DebugCaptureProviderProps {
  children: ReactNode;
  pollIntervalMs?: number;
}

export function DebugCaptureProvider({
  children,
  pollIntervalMs = STATUS_POLL_INTERVAL_MS,
}: DebugCaptureProviderProps) {
  const api = useApi();
  const [operation, setOperation] = useState<DebugCaptureOperation>(null);
  const query = usePollingQuery(() => api.debugCapture.status(), {
    intervalMs: pollIntervalMs,
    errorMessage: "Failed to fetch Debug Capture status",
  });

  const startCapture = async (
    input: Parameters<typeof api.debugCapture.start>[0],
  ): Promise<void> => {
    setOperation("start");
    try {
      const session = await api.debugCapture.start(input);
      query.updateData((current) =>
        current
          ? {
              ...current,
              state: "active",
              session: {
                ...session,
                retained_bytes: 0,
                active_record_count: 0,
                completed_record_count: 0,
                gateway_trace_count: 0,
                evicted_record_count: 0,
                overflowed_record_count: 0,
                history_truncated_trace_count: 0,
                dropped_trace_count: 0,
                dropped_exchange_count: 0,
                dropped_transition_count: 0,
              },
              pending_export_count: 0,
              active_download_count: 0,
            }
          : current,
      );
    } catch (reason) {
      await query.refetch();
      throw reason;
    } finally {
      setOperation(null);
    }
  };

  const stopCapture = async (sessionId: string): Promise<void> => {
    setOperation("stop");
    try {
      await api.debugCapture.stop(sessionId);
      query.updateData((current) =>
        current?.session?.session_id === sessionId
          ? {
              ...current,
              state: "stopped",
              session: null,
              pending_export_count: 0,
              active_download_count: 0,
            }
          : current,
      );
    } finally {
      setOperation(null);
    }
  };

  return (
    <DebugCaptureContext.Provider
      value={{
        status: query.data,
        loading: query.loading,
        error: query.error,
        operation,
        refreshStatus: query.refetch,
        startCapture,
        stopCapture,
      }}
    >
      {children}
    </DebugCaptureContext.Provider>
  );
}
