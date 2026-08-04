import { useApi } from "@/api";
import { useQuery } from "@/hooks/useQuery";

export function useCaptureRecord(sessionId: string, recordId: string | null) {
  const api = useApi();
  const query = useQuery(
    () => api.debugCapture.getRecord(sessionId, recordId ?? ""),
    {
      skip: !recordId,
      queryKey: `${sessionId}|${recordId ?? ""}`,
      errorMessage: "Failed to fetch record preview",
    },
  );

  return {
    detail: query.loading ? null : query.data,
    loading: query.loading,
    error: query.error,
    refresh: query.refetch,
  };
}
