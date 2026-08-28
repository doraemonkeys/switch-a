import type { CreateCredentialSessionInput, CredentialSession } from "../api";
import { useApi } from "../api";
import { useQuery } from "./useQuery";

export function useCredentialSessions() {
  const api = useApi();
  const query = useQuery(() => api.credentialSessions.list(), {
    errorMessage: "Failed to fetch credential sessions",
  });

  const createCredentialSession = async (
    input: CreateCredentialSessionInput,
  ): Promise<CredentialSession> => {
    const created = await api.credentialSessions.create(input);
    await query.refetch();
    return created;
  };

  return {
    credentialSessions: query.data ?? [],
    hasSnapshot: query.data !== null,
    loading: query.loading,
    error: query.error,
    refetch: query.refetch,
    createCredentialSession,
  };
}
