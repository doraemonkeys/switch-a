import { Check, ChevronDown } from "lucide-react";
import type { ConfigFieldDefinition, ConfigValues } from "./types";

interface ConfigFieldProps {
  field: ConfigFieldDefinition;
  values: ConfigValues;
  defaultValue?: string;
  changed: boolean;
  error?: string;
  onChange: (key: string, value: string) => void;
}

function displayValue(field: ConfigFieldDefinition, value: string) {
  if (field.kind === "toggle") return value === "true" ? "开启" : "关闭";
  if ("options" in field)
    return (
      field.options.find((option) => option.value === value)?.label ?? value
    );
  return field.kind === "number" ? `${value} ${field.unit}` : value;
}

export function ConfigField({
  field,
  values,
  defaultValue,
  changed,
  error,
  onChange,
}: ConfigFieldProps) {
  const value = values[field.key];
  const customized = defaultValue !== undefined && value !== defaultValue;
  const disabled = field.disabledWhen?.(values);
  const descriptionId = `${field.key}-description`;
  const feedbackId = `${field.key}-feedback`;
  const controlProps = {
    id: field.key,
    "aria-describedby": `${descriptionId} ${feedbackId}`,
    "aria-invalid": error ? true : undefined,
    disabled,
  };
  const selectedDescription =
    "options" in field
      ? field.options.find((option) => option.value === value)?.description
      : undefined;

  if (field.kind === "choice") {
    return (
      <fieldset className="config-choice-field">
        <legend>
          {field.label}
          {changed && <span className="config-pending-dot" title="未保存" />}
        </legend>
        <div className="config-choices">
          {field.options.map((option) => (
            <label key={option.value} className="config-choice">
              <input
                type="radio"
                name={field.key}
                value={option.value}
                checked={value === option.value}
                onChange={() => onChange(field.key, option.value)}
              />
              <span className="config-choice-title">
                {option.label}
                <Check size={16} aria-hidden="true" />
              </span>
              <span className="config-choice-description">
                {option.description}
              </span>
            </label>
          ))}
        </div>
        {customized && (
          <p className="config-default">
            自定义 · 默认：{displayValue(field, defaultValue)}
          </p>
        )}
      </fieldset>
    );
  }

  return (
    <div className="config-field" data-pending={changed || undefined}>
      <div className="config-field-copy">
        <label htmlFor={field.key}>
          {field.label}
          {changed && <span className="config-pending-dot" title="未保存" />}
        </label>
        <p id={descriptionId}>{field.description}</p>
      </div>
      <div className="config-field-control">
        {field.kind === "toggle" && (
          <div className="config-switch-control">
            <span aria-hidden="true">
              {value === "true" ? "已开启" : "已关闭"}
            </span>
            <input
              {...controlProps}
              type="checkbox"
              role="switch"
              className="config-switch"
              checked={value === "true"}
              onChange={(event) =>
                onChange(field.key, String(event.target.checked))
              }
            />
          </div>
        )}
        {field.kind === "select" && (
          <div className="config-select">
            <select
              {...controlProps}
              value={value}
              onChange={(event) => onChange(field.key, event.target.value)}
            >
              {field.options.map((option) => (
                <option key={option.value} value={option.value}>
                  {option.label}
                </option>
              ))}
            </select>
            <ChevronDown size={16} aria-hidden="true" />
          </div>
        )}
        {field.kind === "number" && (
          <div className="config-number">
            <input
              {...controlProps}
              type="number"
              min={field.min}
              max={field.max}
              step={1}
              required
              value={value}
              onChange={(event) => onChange(field.key, event.target.value)}
            />
            <span aria-hidden="true">{field.unit}</span>
          </div>
        )}
        {field.kind === "text" && (
          <input
            {...controlProps}
            type="text"
            className="config-text-input"
            value={value}
            onChange={(event) => onChange(field.key, event.target.value)}
          />
        )}
        <div id={feedbackId} className="config-field-feedback">
          {selectedDescription && <p>{selectedDescription}</p>}
          {field.help && <p>{field.help}</p>}
          {customized && (
            <p className="config-default">
              自定义 · 默认：{displayValue(field, defaultValue)}
            </p>
          )}
          {error && (
            <p className="config-field-error" role="alert">
              {error}
            </p>
          )}
        </div>
      </div>
    </div>
  );
}
