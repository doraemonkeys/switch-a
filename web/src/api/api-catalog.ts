export const API_CATALOG_SCHEMA_VERSION = 1;

const INVALID_CUSTOM_PATH_SEGMENTS = new Set([
  ".",
  "..",
]) as ReadonlySet<string>;

export interface APICatalogEntry {
  readonly api_type: string;
  readonly label: string;
  readonly description: string;
  readonly display_order: number;
  readonly semantic_error_supported: boolean;
  readonly response_protocol_ids: readonly string[];
}

export interface APICatalog {
  readonly schema_version: typeof API_CATALOG_SCHEMA_VERSION;
  readonly custom_api_type_prefix: string;
  readonly api_types: readonly APICatalogEntry[];
}

export class APICatalogContractError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "APICatalogContractError";
  }
}

type JsonRecord = Record<string, unknown>;

function isRecord(value: unknown): value is JsonRecord {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function contractError(path: string, message: string): never {
  throw new APICatalogContractError(`${path} ${message}`);
}

function readString(
  value: JsonRecord,
  key: string,
  path: string,
  allowEmpty = false,
): string {
  const candidate = value[key];
  if (typeof candidate !== "string") {
    return contractError(`${path}.${key}`, "must be a string");
  }
  if (!allowEmpty && candidate.length === 0) {
    return contractError(`${path}.${key}`, "must not be empty");
  }
  return candidate;
}

function readDisplayOrder(value: JsonRecord, path: string): number {
  const candidate = value.display_order;
  if (
    typeof candidate !== "number" ||
    !Number.isSafeInteger(candidate) ||
    candidate < 0
  ) {
    return contractError(
      `${path}.display_order`,
      "must be a non-negative safe integer",
    );
  }
  return candidate;
}

function readSemanticSupport(value: JsonRecord, path: string): boolean {
  const candidate = value.semantic_error_supported;
  if (typeof candidate !== "boolean") {
    return contractError(
      `${path}.semantic_error_supported`,
      "must be a boolean",
    );
  }
  return candidate;
}

function readProtocolIDs(value: JsonRecord, path: string): readonly string[] {
  const candidate = value.response_protocol_ids;
  if (!Array.isArray(candidate)) {
    return contractError(`${path}.response_protocol_ids`, "must be an array");
  }

  const seen = new Set<string>();
  const protocolIDs = candidate.map((protocolID, index) => {
    if (typeof protocolID !== "string" || protocolID.length === 0) {
      return contractError(
        `${path}.response_protocol_ids[${index}]`,
        "must be a non-empty string",
      );
    }
    if (seen.has(protocolID)) {
      return contractError(
        `${path}.response_protocol_ids[${index}]`,
        `duplicates protocol ID ${JSON.stringify(protocolID)}`,
      );
    }
    seen.add(protocolID);
    return protocolID;
  });

  return Object.freeze(protocolIDs);
}

function parseCatalogEntry(
  value: unknown,
  index: number,
  customPrefix: string,
): APICatalogEntry {
  const path = `api catalog.api_types[${index}]`;
  if (!isRecord(value)) {
    return contractError(path, "must be an object");
  }

  const apiType = readString(value, "api_type", path);
  if (
    apiType.includes("/") ||
    INVALID_CUSTOM_PATH_SEGMENTS.has(apiType) ||
    apiType.startsWith(customPrefix)
  ) {
    return contractError(
      `${path}.api_type`,
      "must identify a routable built-in API type",
    );
  }

  const semanticErrorSupported = readSemanticSupport(value, path);
  const responseProtocolIDs = readProtocolIDs(value, path);
  if (semanticErrorSupported && responseProtocolIDs.length === 0) {
    return contractError(
      `${path}.response_protocol_ids`,
      "must describe at least one protocol when semantic errors are supported",
    );
  }

  return Object.freeze({
    api_type: apiType,
    label: readString(value, "label", path),
    description: readString(value, "description", path, true),
    display_order: readDisplayOrder(value, path),
    semantic_error_supported: semanticErrorSupported,
    response_protocol_ids: responseProtocolIDs,
  });
}

/**
 * Decodes the versioned server projection from unknown at the trust boundary.
 * Display order is data, not array-position convention, so consumers receive a
 * deterministic projection even if an intermediary reorders JSON elements.
 */
export function parseAPICatalog(value: unknown): APICatalog {
  if (!isRecord(value)) {
    return contractError("api catalog", "must be an object");
  }

  if (value.schema_version !== API_CATALOG_SCHEMA_VERSION) {
    return contractError(
      "api catalog.schema_version",
      `must equal ${API_CATALOG_SCHEMA_VERSION}`,
    );
  }

  const customPrefix = readString(
    value,
    "custom_api_type_prefix",
    "api catalog",
  );
  if (customPrefix.includes("/")) {
    return contractError(
      "api catalog.custom_api_type_prefix",
      "must not contain a path separator",
    );
  }

  if (!Array.isArray(value.api_types) || value.api_types.length === 0) {
    return contractError("api catalog.api_types", "must be a non-empty array");
  }

  const entries = value.api_types.map((entry, index) =>
    parseCatalogEntry(entry, index, customPrefix),
  );
  const apiTypes = new Set<string>();
  const displayOrders = new Set<number>();
  for (const entry of entries) {
    if (apiTypes.has(entry.api_type)) {
      return contractError(
        "api catalog.api_types",
        `duplicates API type ${JSON.stringify(entry.api_type)}`,
      );
    }
    if (displayOrders.has(entry.display_order)) {
      return contractError(
        "api catalog.api_types",
        `duplicates display order ${entry.display_order}`,
      );
    }
    apiTypes.add(entry.api_type);
    displayOrders.add(entry.display_order);
  }

  entries.sort((left, right) => left.display_order - right.display_order);
  return Object.freeze({
    schema_version: API_CATALOG_SCHEMA_VERSION,
    custom_api_type_prefix: customPrefix,
    api_types: Object.freeze(entries),
  });
}

export function findBuiltInAPIType(
  catalog: APICatalog,
  apiType: string,
): APICatalogEntry | undefined {
  return catalog.api_types.find((entry) => entry.api_type === apiType);
}

export function isValidCustomAPIType(
  catalog: APICatalog,
  apiType: string,
): boolean {
  if (!apiType.startsWith(catalog.custom_api_type_prefix)) {
    return false;
  }

  const suffix = apiType.slice(catalog.custom_api_type_prefix.length);
  return (
    suffix.length > 0 &&
    !suffix.includes("/") &&
    !INVALID_CUSTOM_PATH_SEGMENTS.has(suffix)
  );
}

/** Built-in membership is always evaluated against the fetched projection. */
export function isValidAPIType(catalog: APICatalog, apiType: string): boolean {
  return (
    findBuiltInAPIType(catalog, apiType) !== undefined ||
    isValidCustomAPIType(catalog, apiType)
  );
}
