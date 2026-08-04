import type { ErrorDetectionPrefill } from "@/features/error-detection";

export function readErrorDetectionPrefill(
  searchParams: URLSearchParams,
): ErrorDetectionPrefill | undefined {
  if (searchParams.get("scope") !== "provider") return undefined;

  const providerID = searchParams.get("provider_id");
  if (!providerID) return undefined;

  const apiType = searchParams.get("api_type");
  return {
    target: { kind: "provider", provider_id: providerID },
    ...(apiType ? { api_type: apiType } : {}),
  };
}

export function getErrorDetectionPrefillKey(
  prefill: ErrorDetectionPrefill | undefined,
): string {
  if (!prefill) return "unscoped";

  const targetKind = prefill.target?.kind ?? "unspecified";
  const providerID =
    prefill.target?.kind === "provider" ? prefill.target.provider_id : "";
  return `${targetKind}:${providerID}:${prefill.api_type ?? ""}`;
}
