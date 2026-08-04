import type {
  RequestEvidenceGateway,
  RequestEvidenceTransport,
  RequestEvidenceTransportV2,
  RequestEvidenceUpstreamEvent,
  RequestEvidenceUpstreamHandshake,
  RequestEvidenceV1,
  RequestEvidenceV2,
} from "../evidence-types";
import {
  assertExactKeys,
  readBoolean,
  readEnum,
  readInteger,
  readRecord,
  readString,
  type JsonRecord,
} from "@/features/error-detection/contracts/contract";
import { parseSemanticError } from "./semantic-decoder";

const TRANSPORT_SOURCES = ["upstream", "client"] as const;
const TRANSPORT_STAGES = [
  "pre_connection_visible",
  "pre_payload_visible",
  "post_payload_visible",
] as const;
const TRANSPORT_KINDS = [
  "timeout",
  "disconnect",
  "protocol_error",
  "local_error",
] as const;
const TRANSPORT_SIGNALS = [
  "sse_idle_timeout",
  "upstream_read_error",
  "client_write_error",
  "eof",
  "unexpected_eof",
  "close_without_status",
  "close_error",
  "timeout",
  "canceled",
  "unknown_transport",
] as const;

function own(value: JsonRecord, key: string): boolean {
  return Object.prototype.hasOwnProperty.call(value, key);
}

function optionalString(
  value: JsonRecord,
  key: string,
  path: string,
): string | undefined {
  return own(value, key)
    ? readString(value[key], `${path}.${key}`, true)
    : undefined;
}

function optionalBoolean(
  value: JsonRecord,
  key: string,
  path: string,
): boolean | undefined {
  return own(value, key)
    ? readBoolean(value[key], `${path}.${key}`)
    : undefined;
}

function optionalInteger(
  value: JsonRecord,
  key: string,
  path: string,
): number | undefined {
  return own(value, key)
    ? readInteger(value[key], `${path}.${key}`, 0)
    : undefined;
}

function parseNullable<T>(
  value: unknown,
  path: string,
  parse: (candidate: unknown, candidatePath: string) => T,
): T | null | undefined {
  if (value === undefined || value === null) {
    return value;
  }
  return parse(value, path);
}

function parseGateway(
  value: unknown,
  path: string,
  exact: boolean,
): RequestEvidenceGateway {
  const gateway = readRecord(value, path);
  if (exact) {
    assertExactKeys(
      gateway,
      path,
      [],
      [
        "terminal_status_code",
        "terminal_error_code",
        "terminal_message_snippet",
      ],
    );
  }
  return Object.freeze({
    terminal_status_code: optionalInteger(
      gateway,
      "terminal_status_code",
      path,
    ),
    terminal_error_code: optionalString(gateway, "terminal_error_code", path),
    terminal_message_snippet: optionalString(
      gateway,
      "terminal_message_snippet",
      path,
    ),
  });
}

function parseHandshake(
  value: unknown,
  path: string,
  exact: boolean,
): RequestEvidenceUpstreamHandshake {
  const handshake = readRecord(value, path);
  if (exact) {
    assertExactKeys(handshake, path, [], ["status_code", "body_snippet"]);
  }
  return Object.freeze({
    status_code: optionalInteger(handshake, "status_code", path),
    body_snippet: optionalString(handshake, "body_snippet", path),
  });
}

function parseUpstreamEvent(
  value: unknown,
  path: string,
  exact: boolean,
): RequestEvidenceUpstreamEvent {
  const event = readRecord(value, path);
  if (exact) {
    assertExactKeys(
      event,
      path,
      [],
      [
        "envelope_type",
        "provider_error_type",
        "provider_error_code",
        "status_code",
        "message_snippet",
        "raw_payload_snippet",
      ],
    );
  }
  return Object.freeze({
    envelope_type: optionalString(event, "envelope_type", path),
    provider_error_type: optionalString(event, "provider_error_type", path),
    provider_error_code: optionalString(event, "provider_error_code", path),
    status_code: optionalInteger(event, "status_code", path),
    message_snippet: optionalString(event, "message_snippet", path),
    raw_payload_snippet: optionalString(event, "raw_payload_snippet", path),
  });
}

function parseTransportV1(
  value: unknown,
  path: string,
): RequestEvidenceTransport {
  const transport = readRecord(value, path);
  return Object.freeze({
    source: optionalString(transport, "source", path),
    message_snippet: optionalString(transport, "message_snippet", path),
    is_timeout: optionalBoolean(transport, "is_timeout", path),
    is_client_cancel: optionalBoolean(transport, "is_client_cancel", path),
    raw_error_snippet: optionalString(transport, "raw_error_snippet", path),
  });
}

function optionalTransportEnum<const T extends readonly string[]>(
  value: JsonRecord,
  key: string,
  allowed: T,
  path: string,
): T[number] | undefined {
  return own(value, key)
    ? readEnum(value[key], allowed, `${path}.${key}`)
    : undefined;
}

function parseTransportV2(
  value: unknown,
  path: string,
): RequestEvidenceTransportV2 {
  const transport = readRecord(value, path);
  assertExactKeys(
    transport,
    path,
    [],
    [
      "source",
      "stage",
      "kind",
      "signal",
      "raw_error_snippet",
      "close_code",
      "close_reason_snippet",
    ],
  );
  return Object.freeze({
    source: optionalTransportEnum(transport, "source", TRANSPORT_SOURCES, path),
    stage: optionalTransportEnum(transport, "stage", TRANSPORT_STAGES, path),
    kind: optionalTransportEnum(transport, "kind", TRANSPORT_KINDS, path),
    signal: optionalTransportEnum(transport, "signal", TRANSPORT_SIGNALS, path),
    raw_error_snippet: optionalString(transport, "raw_error_snippet", path),
    close_code: own(transport, "close_code")
      ? readInteger(transport.close_code, `${path}.close_code`)
      : undefined,
    close_reason_snippet: optionalString(
      transport,
      "close_reason_snippet",
      path,
    ),
  });
}

export function parseEvidenceV1(envelope: JsonRecord): RequestEvidenceV1 {
  const path = "request evidence";
  return Object.freeze({
    ...(envelope.v === 1 ? { v: 1 as const } : {}),
    gateway: parseNullable(
      envelope.gateway,
      `${path}.gateway`,
      (value, itemPath) => parseGateway(value, itemPath, false),
    ),
    upstream_handshake: parseNullable(
      envelope.upstream_handshake,
      `${path}.upstream_handshake`,
      (value, itemPath) => parseHandshake(value, itemPath, false),
    ),
    transport: parseNullable(
      envelope.transport,
      `${path}.transport`,
      parseTransportV1,
    ),
    upstream_event: parseNullable(
      envelope.upstream_event,
      `${path}.upstream_event`,
      (value, itemPath) => parseUpstreamEvent(value, itemPath, false),
    ),
  });
}

export function parseEvidenceV2(envelope: JsonRecord): RequestEvidenceV2 {
  const path = "request evidence";
  return Object.freeze({
    v: 2,
    gateway: parseNullable(
      envelope.gateway,
      `${path}.gateway`,
      (value, itemPath) => parseGateway(value, itemPath, true),
    ),
    upstream_handshake: parseNullable(
      envelope.upstream_handshake,
      `${path}.upstream_handshake`,
      (value, itemPath) => parseHandshake(value, itemPath, true),
    ),
    transport: parseNullable(
      envelope.transport,
      `${path}.transport`,
      parseTransportV2,
    ),
    upstream_event: parseNullable(
      envelope.upstream_event,
      `${path}.upstream_event`,
      (value, itemPath) => parseUpstreamEvent(value, itemPath, true),
    ),
    semantic_error: parseNullable(
      envelope.semantic_error,
      `${path}.semantic_error`,
      parseSemanticError,
    ),
  });
}
