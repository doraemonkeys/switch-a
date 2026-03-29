import type { Provider } from "../../api";

export function getProviderMeta(
  provider: Provider,
  groupNameById: Map<string, string>,
): string {
  const parts = [
    provider.group_id
      ? (groupNameById.get(provider.group_id) ?? provider.group_id)
      : null,
    provider.vendor || null,
    provider.enabled ? null : "disabled",
  ].filter((value): value is string => Boolean(value));

  return parts.join(" • ");
}

export function getProviderOptionLabel(
  provider: Provider,
  groupNameById: Map<string, string>,
): string {
  const meta = getProviderMeta(provider, groupNameById);
  return meta ? `${provider.name} (${meta})` : provider.name;
}
