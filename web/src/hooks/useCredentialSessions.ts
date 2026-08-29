import type {
  CreateCredentialSessionInput,
  CredentialSession,
  RenameCredentialSessionInput,
  UpdateCredentialSessionInput,
} from "../api";
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

  const renameCredentialSession = async (
    id: string,
    input: RenameCredentialSessionInput,
  ): Promise<CredentialSession> => {
    const updated = await api.credentialSessions.rename(id, input);
    await query.refetch();
    return updated;
  };

  const updateCredentialSession = async (
    id: string,
    input: UpdateCredentialSessionInput,
  ): Promise<CredentialSession> => {
    const updated = await api.credentialSessions.update(id, input);
    await query.refetch();
    return updated;
  };

  const deleteCredentialSession = async (id: string): Promise<void> => {
    await api.credentialSessions.delete(id);
    await query.refetch();
  };

  return {
    credentialSessions: query.data ?? [],
    hasSnapshot: query.data !== null,
    loading: query.loading,
    error: query.error,
    refetch: query.refetch,
    createCredentialSession,
    renameCredentialSession,
    updateCredentialSession,
    deleteCredentialSession,
  };
}
