import type { RequestEvidence } from "../../api/types";

export function parseRequestEvidence(
  evidenceJson?: string | null,
): RequestEvidence | null {
  if (!evidenceJson || evidenceJson.trim() === "") {
    return null;
  }

  try {
    const parsed = JSON.parse(evidenceJson) as RequestEvidence;
    return parsed && typeof parsed === "object" ? parsed : null;
  } catch {
    return null;
  }
}

export function getRequestEvidenceSummary(
  evidenceJson?: string | null,
): string | null {
  const evidence = parseRequestEvidence(evidenceJson);
  if (!evidence) {
    return null;
  }

  return (
    evidence.gateway?.terminal_message_snippet ||
    evidence.transport?.message_snippet ||
    evidence.transport?.raw_error_snippet ||
    evidence.upstream_event?.message_snippet ||
    evidence.upstream_handshake?.body_snippet ||
    evidence.upstream_event?.raw_payload_snippet ||
    null
  );
}
