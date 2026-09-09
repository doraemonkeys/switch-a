import type {
  RequestEvidenceCompletion,
  RequestEvidenceResponse,
  RequestEvidenceResponseProgress,
  RequestEvidenceWrite,
} from "../evidence-types";
import {
  readInteger,
  readRecord,
  readString,
} from "@/features/error-detection/contracts/contract";

export function parseCompletion(
  value: unknown,
  path: string,
): RequestEvidenceCompletion {
  const fact = readRecord(value, path);
  return Object.freeze({
    event_type: readString(fact.event_type, `${path}.event_type`),
    observed_at: readString(fact.observed_at, `${path}.observed_at`),
  });
}

function parseResponse(value: unknown, path: string): RequestEvidenceResponse {
  const fact = readRecord(value, path);
  const optional = (key: string) =>
    fact[key] === undefined
      ? undefined
      : readString(fact[key], `${path}.${key}`, true);
  return Object.freeze({
    round: readInteger(fact.round, `${path}.round`, 0),
    response_id: optional("response_id"),
    event_type: optional("event_type"),
    status: optional("status"),
    observed_at: optional("observed_at"),
  });
}

export function parseResponseProgress(
  value: unknown,
  path: string,
): RequestEvidenceResponseProgress {
  const fact = readRecord(value, path);
  return Object.freeze({
    current: parseResponse(fact.current, `${path}.current`),
    last_completed:
      fact.last_completed == null
        ? undefined
        : parseResponse(fact.last_completed, `${path}.last_completed`),
    completed_responses: readInteger(
      fact.completed_responses,
      `${path}.completed_responses`,
      0,
    ),
  });
}

export function parseWrite(value: unknown, path: string): RequestEvidenceWrite {
  const fact = readRecord(value, path);
  return Object.freeze({
    calls: readInteger(fact.calls, `${path}.calls`, 0),
    successful_calls: readInteger(
      fact.successful_calls,
      `${path}.successful_calls`,
      0,
    ),
    failed_calls: readInteger(fact.failed_calls, `${path}.failed_calls`, 0),
    confirmed_bytes: readInteger(
      fact.confirmed_bytes,
      `${path}.confirmed_bytes`,
      0,
    ),
    last_error:
      fact.last_error === undefined
        ? undefined
        : readString(fact.last_error, `${path}.last_error`, true),
  });
}
