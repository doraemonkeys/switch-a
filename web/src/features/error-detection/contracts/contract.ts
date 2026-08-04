export type JsonRecord = Record<string, unknown>;

const CANONICAL_DECIMAL = /^(?:0|[1-9]\d*)$/;
const LOWERCASE_UUID_V4 =
  /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const RFC3339_UTC =
  /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.\d+)?Z$/;

export class InternalErrorContractError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "InternalErrorContractError";
  }
}

export function contractError(path: string, message: string): never {
  throw new InternalErrorContractError(`${path} ${message}`);
}

export function readRecord(value: unknown, path: string): JsonRecord {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    return contractError(path, "must be an object");
  }
  return value as JsonRecord;
}

export function assertExactKeys(
  value: JsonRecord,
  path: string,
  required: readonly string[],
  optional: readonly string[] = [],
): void {
  const allowed = new Set([...required, ...optional]);
  for (const key of Object.keys(value)) {
    if (!allowed.has(key)) {
      contractError(`${path}.${key}`, "is not allowed");
    }
  }
  for (const key of required) {
    if (!Object.prototype.hasOwnProperty.call(value, key)) {
      contractError(`${path}.${key}`, "is required");
    }
  }
}

export function readString(
  value: unknown,
  path: string,
  allowEmpty = false,
): string {
  if (typeof value !== "string") {
    return contractError(path, "must be a string");
  }
  if (!allowEmpty && value.length === 0) {
    return contractError(path, "must not be empty");
  }
  return value;
}

export function readBoolean(value: unknown, path: string): boolean {
  if (typeof value !== "boolean") {
    return contractError(path, "must be a boolean");
  }
  return value;
}

export function readFiniteNumber(value: unknown, path: string): number {
  if (typeof value !== "number" || !Number.isFinite(value)) {
    return contractError(path, "must be a finite number");
  }
  return value;
}

export function readInteger(
  value: unknown,
  path: string,
  minimum = Number.MIN_SAFE_INTEGER,
  maximum = Number.MAX_SAFE_INTEGER,
): number {
  if (
    typeof value !== "number" ||
    !Number.isSafeInteger(value) ||
    value < minimum ||
    value > maximum
  ) {
    return contractError(
      path,
      `must be a safe integer between ${minimum} and ${maximum}`,
    );
  }
  return value;
}

export function readArray(value: unknown, path: string): readonly unknown[] {
  if (!Array.isArray(value)) {
    return contractError(path, "must be an array");
  }
  return value;
}

export function readEnum<const T extends readonly string[]>(
  value: unknown,
  allowed: T,
  path: string,
): T[number] {
  if (typeof value !== "string" || !allowed.includes(value)) {
    return contractError(path, `must be one of: ${allowed.join(", ")}`);
  }
  return value;
}

export function readCanonicalDecimal(value: unknown, path: string): string {
  if (typeof value !== "string" || !CANONICAL_DECIMAL.test(value)) {
    return contractError(path, "must be a canonical unsigned decimal string");
  }
  return value;
}

export function readUUID(value: unknown, path: string): string {
  if (typeof value !== "string" || !LOWERCASE_UUID_V4.test(value)) {
    return contractError(path, "must be a lowercase UUIDv4");
  }
  return value;
}

export function readUTCInstant(value: unknown, path: string): string {
  if (typeof value !== "string") {
    return contractError(path, "must be an RFC3339 UTC timestamp");
  }
  const match = RFC3339_UTC.exec(value);
  if (!match) {
    return contractError(path, "must be an RFC3339 UTC timestamp");
  }

  const [, yearText, monthText, dayText, hourText, minuteText, secondText] =
    match;
  const year = Number(yearText);
  const month = Number(monthText);
  const day = Number(dayText);
  const hour = Number(hourText);
  const minute = Number(minuteText);
  const second = Number(secondText);
  const instant = new Date(0);
  instant.setUTCFullYear(year, month - 1, day);
  instant.setUTCHours(hour, minute, second, 0);
  if (
    month < 1 ||
    month > 12 ||
    hour > 23 ||
    minute > 59 ||
    second > 59 ||
    instant.getUTCFullYear() !== year ||
    instant.getUTCMonth() !== month - 1 ||
    instant.getUTCDate() !== day
  ) {
    return contractError(path, "must be a valid RFC3339 UTC timestamp");
  }
  return value;
}

export function utf8ByteLength(value: string): number {
  return new TextEncoder().encode(value).byteLength;
}
