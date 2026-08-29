import { useState } from "react";
import { ApiError, useApi, type Provider } from "../../api";
import { useToast } from "../../hooks/useToast";
import { downloadJsonFile } from "../../lib/jsonDownload";
import { resolveCodexAuthExportAvailability } from "../../lib/providerAuth";

function describeRouteTargets(routeTargetIDs: string[], providers: Provider[]) {
  return routeTargetIDs
    .map((id) => {
      const provider = providers.find((candidate) => candidate.id === id);
      return provider ? `"${provider.name}" (${id})` : id;
    })
    .join(", ");
}

function blockedMessage(
  credentialSessionID: string,
  blockingRouteTargetIDs: string[],
  providers: Provider[],
) {
  return `Pause every provider using credential session "${credentialSessionID}" before exporting: ${describeRouteTargets(blockingRouteTargetIDs, providers)}.`;
}

function errorMessage(error: unknown, providers: Provider[]) {
  if (
    error instanceof ApiError &&
    error.details?.blocking_route_target_ids?.length
  ) {
    return blockedMessage(
      error.details.credential_session_id ?? "unknown",
      error.details.blocking_route_target_ids,
      providers,
    );
  }
  return error instanceof Error
    ? error.message
    : "Failed to export Codex auth.json";
}

export function useCodexAuthExport(providers: Provider[]) {
  const api = useApi();
  const toast = useToast();
  const [exportingCredentialSessionId, setExportingCredentialSessionId] =
    useState<string | null>(null);

  const handleExportCodexAuth = async (provider: Provider) => {
    const availability = resolveCodexAuthExportAvailability(
      provider,
      providers,
    );
    if (!availability.session) {
      toast.error(`Provider "${provider.name}" has no GPT credential session`);
      return;
    }
    if (availability.kind === "unavailable") {
      toast.error(
        `Credential session "${availability.session.id}" does not have active GPT credentials to export`,
      );
      return;
    }
    if (availability.kind === "blocked") {
      toast.error(
        blockedMessage(
          availability.session.id,
          availability.blockingRouteTargets.map(({ id }) => id),
          providers,
        ),
      );
      return;
    }

    setExportingCredentialSessionId(availability.session.id);
    try {
      const authDocument = await api.credentialSessions.exportCodexAuth(
        availability.session.id,
      );
      downloadJsonFile("auth.json", authDocument);
      toast.success(
        `Codex auth.json exported for credential session "${availability.session.id}". Keep every referencing provider paused while the file is in use.`,
      );
    } catch (error) {
      toast.error(errorMessage(error, providers));
    } finally {
      setExportingCredentialSessionId(null);
    }
  };

  return { handleExportCodexAuth, exportingCredentialSessionId };
}
