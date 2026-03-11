export function normalizeProviderApiKey(apiKey?: string | null): string {
  return apiKey?.trim() ?? "";
}

export function hasProviderApiKey(apiKey?: string | null): boolean {
  return normalizeProviderApiKey(apiKey) !== "";
}
