const EXPORT_ID_PATTERN = /^ce_[A-Za-z0-9_-]{24}$/;
// A 32-byte raw-base64url value has 43 characters and only four significant
// bits in its final character; constraining those padding bits rejects aliases.
const DOWNLOAD_TOKEN_PATTERN = /^[A-Za-z0-9_-]{42}[AEIMQUYcgkosw048]$/;
const RFC3339_TIMESTAMP_PATTERN =
  /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?(?:Z|[+-]\d{2}:\d{2})$/;
const EXPORT_DOWNLOAD_ROUTE_PREFIX = "/admin/api/debug-capture/exports/";

export interface ValidatedDebugCaptureDownloadGrant {
  export_id: string;
  session_id: string;
  record_count: number;
  expires_at: string;
  download_url: string;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

export function validatedDebugCaptureDownloadGrant(
  value: unknown,
  expectedSessionId: string,
  nowMilliseconds: number = Date.now(),
): ValidatedDebugCaptureDownloadGrant | null {
  if (!isRecord(value)) {
    return null;
  }

  try {
    const {
      export_id: exportId,
      session_id: sessionId,
      record_count: recordCount,
      expires_at: expiresAt,
      download_url: downloadURL,
    } = value;
    if (
      typeof exportId !== "string" ||
      typeof sessionId !== "string" ||
      typeof expiresAt !== "string" ||
      typeof downloadURL !== "string" ||
      !EXPORT_ID_PATTERN.test(exportId) ||
      sessionId !== expectedSessionId ||
      !Number.isSafeInteger(recordCount) ||
      (recordCount as number) <= 0 ||
      !RFC3339_TIMESTAMP_PATTERN.test(expiresAt)
    ) {
      return null;
    }

    const expiresAtMilliseconds = Date.parse(expiresAt);
    const expectedPath = `${EXPORT_DOWNLOAD_ROUTE_PREFIX}${exportId}/download`;
    const expectedURLPrefix = `${expectedPath}?download_token=`;
    const downloadToken = downloadURL.startsWith(expectedURLPrefix)
      ? downloadURL.slice(expectedURLPrefix.length)
      : "";
    if (
      !Number.isFinite(nowMilliseconds) ||
      !Number.isFinite(expiresAtMilliseconds) ||
      expiresAtMilliseconds <= nowMilliseconds ||
      !DOWNLOAD_TOKEN_PATTERN.test(downloadToken) ||
      downloadURL !== expectedURLPrefix + downloadToken
    ) {
      return null;
    }

    // Copy only validated fields into a fresh object. This prevents untrusted
    // response properties or prototype state from reaching the form/DOM.
    return {
      export_id: exportId,
      session_id: sessionId,
      record_count: recordCount as number,
      expires_at: expiresAt,
      download_url: downloadURL,
    };
  } catch {
    // Accessor-bearing values are not expected from JSON, but treating them as
    // malformed keeps this parser total when tests or alternate clients call it.
    return null;
  }
}
