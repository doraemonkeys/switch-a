import type { ImportMode } from "../../api/types";
import type {
  ProviderOption,
  SelectionCatalog,
  SummarySectionKey,
} from "./types";

export const CONFIG_TRANSFER_VERSION = "4.0";

export const IMPORT_SUMMARY_SECTIONS: Record<
  SummarySectionKey,
  { label: string }
> = {
  providers: { label: "Providers" },
  groups: { label: "Groups" },
  routing_policies: { label: "Routing Policies" },
  settings: { label: "Settings" },
  internal_error_rules: { label: "Internal Error Rules" },
};

export const IMPORT_SUMMARY_KEYS_BY_MODE: Record<
  ImportMode,
  SummarySectionKey[]
> = {
  full: [
    "providers",
    "groups",
    "routing_policies",
    "settings",
    "internal_error_rules",
  ],
  settings_only: ["settings"],
  selection: ["providers", "groups", "internal_error_rules"],
};

export const REQUIRED_IMPORT_ARRAY_FIELDS = [
  "providers",
  "groups",
  "routing_policies",
  "internal_error_rules",
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
      "导入文件中的 Providers、Groups、Routing Policies、Settings 和 Internal Error Rules。",
  },
  {
    mode: "settings_only",
    title: "仅导入 Settings",
    description:
      "只更新运行时配置，Providers、Groups、Routing Policies 和 Internal Error Rules 保持当前系统值。",
  },
  {
    mode: "selection",
    title: "按 Group / Provider 选择",
    description:
      "选中的 Group 会同时导入该 Group 下的 Providers 和对应 Internal Error Rules；选中的 Provider 会自动补齐其所属 Group。Routing Policies 与 Settings 不会导入。",
  },
];

export const EMPTY_SELECTION_CATALOG: SelectionCatalog = {
  groups: [],
  providers: [],
  providersById: new Map<string, ProviderOption>(),
};
