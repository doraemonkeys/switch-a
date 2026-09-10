import { CONFIG_FIELDS } from "./schema";
import type { ConfigFieldDefinition, ConfigValues } from "./types";

export function effectiveConfig(
  values: ConfigValues,
  defaults: ConfigValues,
): ConfigValues {
  return Object.fromEntries(
    Object.entries({
      ...Object.fromEntries(
        CONFIG_FIELDS.map((field) => [field.key, field.defaultValue]),
      ),
      ...defaults,
      ...values,
    }).map(([key, value]) => [key, String(value)]),
  );
}

export function changedValues(
  values: ConfigValues,
  saved: ConfigValues,
): ConfigValues {
  return Object.fromEntries(
    Object.entries(values).filter(([key, value]) => value !== saved[key]),
  );
}

export function equalConfig(left: ConfigValues, right: ConfigValues): boolean {
  return (
    Object.keys(left).length === Object.keys(right).length &&
    Object.entries(left).every(([key, value]) => value === right[key])
  );
}

export function fieldMatches(
  field: ConfigFieldDefinition,
  query: string,
  group: string,
): boolean {
  const content = [
    group,
    field.key,
    field.label,
    field.description,
    field.help,
    ...("options" in field ? field.options.map((option) => option.label) : []),
  ]
    .join(" ")
    .toLocaleLowerCase();
  return query
    .trim()
    .toLocaleLowerCase()
    .split(/\s+/)
    .every((term) => content.includes(term));
}

export function validateConfig(values: ConfigValues): Record<string, string> {
  const errors: Record<string, string> = {};
  for (const field of CONFIG_FIELDS) {
    if (field.kind !== "number" || field.disabledWhen?.(values)) continue;
    const value = values[field.key];
    const number = Number(value);
    if (
      value.trim() === "" ||
      !Number.isInteger(number) ||
      number < field.min
    ) {
      errors[field.key] = `请输入不小于 ${field.min} 的整数`;
    } else if (field.max !== undefined && number > field.max) {
      errors[field.key] = `请输入不大于 ${field.max} 的整数`;
    }
  }
  return errors;
}
