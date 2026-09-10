import { useRef, useState, type FormEvent } from "react";
import { Search, X, SlidersHorizontal } from "lucide-react";
import { useToast } from "../hooks/useToast";
import { generateUUIDv4 } from "../lib/uuid";
import { CONFIG_CATEGORIES } from "../features/runtime-config/schema";
import { fieldMatches, validateConfig } from "../features/runtime-config/model";
import { useConfigDraft } from "../features/runtime-config/useConfigDraft";
import { ConfigField } from "../features/runtime-config/ConfigField";
import { ConfigNavigation } from "../features/runtime-config/ConfigNavigation";
import type {
  ConfigCategoryId,
  ConfigValues,
} from "../features/runtime-config/types";
import "../features/runtime-config/config.css";

import { ConfigSaveBar } from "../features/runtime-config/ConfigSaveBar";

interface ConfigFormProps {
  initialConfig: ConfigValues;
  defaults?: ConfigValues;
  onSave: (config: ConfigValues) => Promise<void>;
  saving: boolean;
}

export function ConfigForm({
  initialConfig,
  defaults = {},
  onSave,
  saving,
}: ConfigFormProps) {
  const toast = useToast();
  const { draft, changes, change, reset, acceptSaved } = useConfigDraft(
    initialConfig,
    defaults,
  );
  const [category, setCategory] = useState<ConfigCategoryId>("routing");
  const [query, setQuery] = useState("");
  const [onlyChanged, setOnlyChanged] = useState(false);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [submitting, setSubmitting] = useState(false);
  const saveInFlight = useRef(false);
  const busy = saving || submitting;
  const changeCount = Object.keys(changes).length;
  const filtering = query.trim() !== "" || onlyChanged;
  const activeCategory = CONFIG_CATEGORIES.find(
    (item) => item.id === category,
  )!;
  const visibleCategories = (filtering ? CONFIG_CATEGORIES : [activeCategory])
    .map((item) => ({
      ...item,
      groups: item.groups
        .map((group) => ({
          ...group,
          fields: group.fields.filter(
            (field) =>
              fieldMatches(field, query, `${item.title} ${group.title}`) &&
              (!onlyChanged || field.key in changes),
          ),
        }))
        .filter((group) => group.fields.length > 0),
    }))
    .filter((item) => item.groups.length > 0);
  const resultCount = visibleCategories.reduce(
    (total, item) =>
      total +
      item.groups.reduce((count, group) => count + group.fields.length, 0),
    0,
  );

  const selectCategory = (id: ConfigCategoryId) => {
    setCategory(id);
    setQuery("");
    setOnlyChanged(false);
  };

  const handleFieldChange = (key: string, value: string) => {
    change(key, value);
    if (errors[key])
      setErrors((previous) => {
        const next = { ...previous };
        delete next[key];
        return next;
      });
  };

  const handleSubmit = async (event: FormEvent) => {
    event.preventDefault();
    if (!changeCount || busy || saveInFlight.current) return;
    const nextErrors = validateConfig(draft);
    setErrors(nextErrors);
    const invalidCategory = CONFIG_CATEGORIES.find((item) =>
      item.groups
        .flatMap((group) => group.fields)
        .some((field) => field.key in nextErrors),
    );
    if (invalidCategory) {
      selectCategory(invalidCategory.id);
      const form = event.currentTarget;
      // Reveal the category before focusing a control that was not mounted.
      requestAnimationFrame(() => {
        form.querySelector<HTMLElement>('[aria-invalid="true"]')?.focus();
      });
      toast.error("请检查标出的配置项");
      return;
    }
    saveInFlight.current = true;
    setSubmitting(true);
    const operationId = generateUUIDv4();
    const submitted = { ...draft };
    console.info("config.save.started", {
      operationId,
      changedKeys: Object.keys(changes),
    });
    try {
      await onSave(submitted);
      acceptSaved(submitted);
      console.info("config.save.completed", { operationId });
      toast.success("配置已保存");
    } catch (error) {
      console.error("config.save.failed", { operationId, error });
      toast.error(
        error instanceof Error ? error.message : "保存配置失败，请重试",
      );
    } finally {
      saveInFlight.current = false;
      setSubmitting(false);
    }
  };

  return (
    <form
      className="config-workspace"
      onSubmit={handleSubmit}
      noValidate
      aria-label="运行配置"
    >
      <div className="config-toolbar">
        <div className="config-search">
          <Search size={18} aria-hidden="true" />
          <input
            type="search"
            aria-label="搜索配置"
            placeholder="搜索设置名称或配置键…"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === "Enter") event.preventDefault();
            }}
          />
          {query && (
            <button
              type="button"
              aria-label="清空搜索"
              onClick={() => setQuery("")}
            >
              <X size={16} />
            </button>
          )}
        </div>
        <button
          type="button"
          className="config-filter"
          aria-pressed={onlyChanged}
          onClick={() => setOnlyChanged(!onlyChanged)}
        >
          <SlidersHorizontal size={16} aria-hidden="true" />
          仅看未保存{changeCount > 0 && <span>{changeCount}</span>}
        </button>
      </div>
      <div className="config-body">
        <ConfigNavigation
          active={filtering ? undefined : category}
          changes={changes}
          onSelect={selectCategory}
        />
        <div className="config-content">
          <div className="config-content-heading">
            <div>
              <h3>{filtering ? "筛选结果" : activeCategory.title}</h3>
              <p>
                {filtering
                  ? "在全部分类中查找，修改会与其他配置一起保存。"
                  : activeCategory.description}
              </p>
            </div>
            <span aria-live="polite">{resultCount} 项</span>
          </div>
          <fieldset className="config-fields" disabled={busy}>
            <legend className="sr-only">配置项</legend>
            {visibleCategories.flatMap((item) =>
              item.groups.map((group) => (
                <section
                  className="config-group"
                  key={group.title}
                  aria-label={group.title}
                >
                  <div className="config-group-heading">
                    <h4>{group.title}</h4>
                    <p>{group.description}</p>
                  </div>
                  {group.fields.map((field) => (
                    <ConfigField
                      key={field.key}
                      field={field}
                      values={draft}
                      defaultValue={defaults[field.key]}
                      changed={field.key in changes}
                      error={errors[field.key]}
                      onChange={handleFieldChange}
                    />
                  ))}
                </section>
              )),
            )}
          </fieldset>
          {resultCount === 0 && (
            <div className="config-empty">
              <Search size={28} aria-hidden="true" />
              <h4>
                {onlyChanged && !query
                  ? "没有待保存的修改"
                  : "没有找到匹配的配置"}
              </h4>
              <p>试试其他名称，或清除筛选查看全部设置。</p>
              <button
                type="button"
                className="config-button config-button-secondary"
                onClick={() => {
                  setQuery("");
                  setOnlyChanged(false);
                }}
              >
                清除筛选
              </button>
            </div>
          )}
        </div>
      </div>
      <ConfigSaveBar
        changeCount={changeCount}
        busy={busy}
        onReset={() => {
          reset();
          setErrors({});
        }}
      />
    </form>
  );
}
