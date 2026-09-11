import type { ConfigKey } from "../../config";

export type ConfigValues = Record<string, string>;
export type ConfigCategoryId = "routing" | "accounts" | "requests" | "system";

interface FieldBase {
  key: ConfigKey;
  label: string;
  description: string;
  defaultValue: string | number | boolean;
  help?: string;
  disabledWhen?: (values: ConfigValues) => boolean;
}

export interface ConfigOption {
  value: string;
  label: string;
  description?: string;
}

export type ConfigFieldDefinition = FieldBase &
  (
    | {
        kind: "select" | "choice" | "combobox";
        options: readonly ConfigOption[];
      }
    | { kind: "number"; min: number; max?: number; unit: string }
    | { kind: "text" }
    | { kind: "toggle" }
  );

export interface ConfigGroup {
  title: string;
  description: string;
  fields: readonly ConfigFieldDefinition[];
}

export interface ConfigCategory {
  id: ConfigCategoryId;
  title: string;
  description: string;
  groups: readonly ConfigGroup[];
}
