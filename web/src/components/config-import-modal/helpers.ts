import type {
  ExportedConfig,
  ImportConfigRequest,
  ImportMode,
  ImportPreviewResponse,
  ImportScope,
} from "../../api/types";
import {
  EMPTY_SELECTION_CATALOG,
  IMPORT_SUMMARY_KEYS_BY_MODE,
} from "./constants";
import type { SelectionCatalog, SummarySectionKey } from "./types";
export function getSelectionCatalog(
  config: ExportedConfig | null,
): SelectionCatalog {
  if (!config) {
    return EMPTY_SELECTION_CATALOG;
  }

  const groupNameById = new Map(
    config.groups.map((group) => [group.id, group.name]),
  );
  const providerCountByGroup = new Map<string, number>();

  for (const provider of config.providers) {
    if (!provider.group_id) {
      continue;
    }

    providerCountByGroup.set(
      provider.group_id,
      (providerCountByGroup.get(provider.group_id) ?? 0) + 1,
    );
  }

  const groups = config.groups.map((group) => ({
    id: group.id,
    name: group.name,
    providerCount: providerCountByGroup.get(group.id) ?? 0,
  }));

  const providers = config.providers.map((provider) => ({
    id: provider.id,
    name: provider.name,
    groupId: provider.group_id ?? null,
    groupName: provider.group_id
      ? (groupNameById.get(provider.group_id) ?? null)
      : null,
  }));

  return {
    groups,
    providers,
    providersById: new Map(
      providers.map((provider) => [provider.id, provider]),
    ),
  };
}

export function getVisibleSummaryKeys(mode: ImportMode): SummarySectionKey[] {
  return IMPORT_SUMMARY_KEYS_BY_MODE[mode];
}

export function hasVisibleChanges(
  preview: ImportPreviewResponse | null,
  mode: ImportMode,
): boolean {
  if (!preview) {
    return false;
  }

  return getVisibleSummaryKeys(mode).some((key) => {
    const change = preview.changes[key];
    return change.add > 0 || change.update > 0 || change.delete > 0;
  });
}

export function toggleSelectedId(selectedIds: string[], id: string): string[] {
  return selectedIds.includes(id)
    ? selectedIds.filter((selectedId) => selectedId !== id)
    : [...selectedIds, id];
}

export function isJsonFile(file: File): boolean {
  return (
    file.type === "application/json" ||
    file.name.toLowerCase().endsWith(".json")
  );
}

export function buildImportScope(
  mode: ImportMode,
  selectedGroupIds: string[],
  selectedProviderIds: string[],
): ImportScope {
  if (mode === "full") {
    return { mode };
  }

  if (mode === "settings_only") {
    return { mode };
  }

  return {
    mode,
    selection: {
      group_ids: [...selectedGroupIds],
      provider_ids: [...selectedProviderIds],
    },
  };
}

export function buildImportRequest(
  parsedConfig: ExportedConfig,
  scope: ImportScope,
): ImportConfigRequest {
  return {
    version: parsedConfig.version,
    import_scope: scope,
    providers: parsedConfig.providers,
    groups: parsedConfig.groups,
    routing_policies: parsedConfig.routing_policies,
    settings: parsedConfig.settings,
    internal_error_rules: parsedConfig.internal_error_rules,
  };
}

export function formatSelectionSummary(
  selectedGroupIds: string[],
  selectedProviderIds: string[],
  catalog: SelectionCatalog,
): string {
  if (selectedGroupIds.length === 0 && selectedProviderIds.length === 0) {
    return "至少选择一个 Group 或 Provider 后才能预览。";
  }

  const selectedGroupIdSet = new Set(selectedGroupIds);
  const selectedGroupProviderCount = catalog.providers.filter(
    (provider) =>
      provider.groupId != null && selectedGroupIdSet.has(provider.groupId),
  ).length;
  const autoIncludedGroupIds = new Set<string>();
  for (const providerId of selectedProviderIds) {
    const provider = catalog.providersById.get(providerId);
    if (provider?.groupId && !selectedGroupIds.includes(provider.groupId)) {
      autoIncludedGroupIds.add(provider.groupId);
    }
  }

  const parts: string[] = [];

  if (selectedGroupIds.length > 0) {
    if (selectedGroupProviderCount > 0) {
      parts.push(
        `已选 ${selectedGroupIds.length} 个 Group，会同时导入其下 ${selectedGroupProviderCount} 个 Provider`,
      );
    } else {
      parts.push(`已选 ${selectedGroupIds.length} 个 Group，仅导入 Group 本身`);
    }
  }

  if (selectedProviderIds.length > 0) {
    parts.push(`已选 ${selectedProviderIds.length} 个 Provider`);
  }

  if (autoIncludedGroupIds.size > 0) {
    parts.push(
      `导入时会自动补齐 ${autoIncludedGroupIds.size} 个 Provider 所属 Group`,
    );
  }

  return parts.join("，") + "。";
}
