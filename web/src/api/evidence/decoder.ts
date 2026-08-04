import type {
  RequestEvidence,
  RequestEvidenceDecodeResult,
  RequestEvidenceUnavailableReason,
} from "../evidence-types";
import {
  readRecord,
  type JsonRecord,
} from "@/features/error-detection/contracts/contract";
import { parseEvidenceV1, parseEvidenceV2 } from "./sections-decoder";

const EVIDENCE_SCHEMA_V1 = 1 as const;
const EVIDENCE_SCHEMA_V2 = 2 as const;

function unavailable(
  reason: RequestEvidenceUnavailableReason,
  detail: string,
): RequestEvidenceDecodeResult {
  return Object.freeze({ state: "unavailable", reason, detail });
}

function decodeEnvelope(envelope: JsonRecord): RequestEvidenceDecodeResult {
  if (
    envelope.v !== undefined &&
    envelope.v !== EVIDENCE_SCHEMA_V1 &&
    envelope.v !== EVIDENCE_SCHEMA_V2
  ) {
    return unavailable(
      "unsupported_version",
      "request evidence.v must be 1, 2, or absent",
    );
  }
  try {
    const evidence: RequestEvidence =
      envelope.v === EVIDENCE_SCHEMA_V2
        ? parseEvidenceV2(envelope)
        : parseEvidenceV1(envelope);
    return Object.freeze({ state: "available", evidence });
  } catch (error) {
    return unavailable(
      "invalid_schema",
      error instanceof Error ? error.message : "request evidence is invalid",
    );
  }
}

export function decodeRequestEvidence(
  value: unknown,
): RequestEvidenceDecodeResult {
  if (value === undefined || value === null) {
    return Object.freeze({ state: "absent" });
  }
  try {
    return decodeEnvelope(readRecord(value, "request evidence"));
  } catch (error) {
    return unavailable(
      "invalid_schema",
      error instanceof Error ? error.message : "request evidence is invalid",
    );
  }
}

export function parseRequestEvidenceJson(
  evidenceJson?: string | null,
): RequestEvidenceDecodeResult {
  if (!evidenceJson || evidenceJson.trim() === "") {
    return Object.freeze({ state: "absent" });
  }
  try {
    return decodeRequestEvidence(JSON.parse(evidenceJson) as unknown);
  } catch {
    return unavailable("malformed_json", "request evidence is not valid JSON");
  }
}
