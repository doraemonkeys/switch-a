import type { ImportMode } from "../../api/types";
import type {
  ProviderOption,
  SelectionCatalog,
  SummarySectionKey,
} from "./types";

export const IMPORT_SUMMARY_SECTIONS: Record<
  SummarySectionKey,
  { label: string }
> = {
  providers: { label: "Providers" },
  groups: { label: "Groups" },
  routing_policies: { label: "Routing Policies" },
  settings: { label: "Settings" },
};

export const IMPORT_SUMMARY_KEYS_BY_MODE: Record<
  ImportMode,
  SummarySectionKey[]
> = {
  full: ["providers", "groups", "routing_policies", "settings"],
  settings_only: ["settings"],
  selection: ["providers", "groups"],
};

export const REQUIRED_IMPORT_ARRAY_FIELDS = [
  "providers",
  "groups",
  "routing_policies",
] as const;

export const IMPORT_MODE_OPTIONS: Array<{
  mode: ImportMode;
  title: string;
  description: string;
}> = [
  {
    mode: "full",
    title: "全量导入",
    description:
      "导入文件中的 Providers、Groups、Routing Policies 和 Settings。",
  },
  {
    mode: "settings_only",
    title: "仅导入 Settings",
    description:
      "只更新运行时配置，Providers、Groups 和 Routing Policies 保持当前系统值。",
  },
  {
    mode: "selection",
    title: "按 Group / Provider 选择",
    description:
      "选中的 Group 会同时导入该 Group 下的 Providers；选中的 Provider 会自动补齐其所属 Group。Routing Policies 与 Settings 不会导入。",
  },
];

export const EMPTY_SELECTION_CATALOG: SelectionCatalog = {
  groups: [],
  providers: [],
  providersById: new Map<string, ProviderOption>(),
};
