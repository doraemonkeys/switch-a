import type * as React from "react";
import type { ExportedConfig, ImportMode } from "../../api/types";
import { IMPORT_MODE_OPTIONS } from "./constants";
import { FileCard } from "./common";
import { formatSelectionSummary } from "./helpers";
import type { SelectionCatalog } from "./types";
const UploadIcon = () => (
  <svg
    className="w-12 h-12 text-text-muted"
    fill="none"
    stroke="currentColor"
    viewBox="0 0 24 24"
  >
    <path
      strokeLinecap="round"
      strokeLinejoin="round"
      strokeWidth={1.5}
      d="M7 16a4 4 0 01-.88-7.903A5 5 0 1115.9 6L16 6a5 5 0 011 9.9M15 13l-3-3m0 0l-3 3m3-3v12"
    />
  </svg>
);

function ScopeSelectionList({
  title,
  emptyState,
  items,
}: {
  title: string;
  emptyState: string;
  items: React.ReactNode;
}) {
  return (
    <section className="space-y-3">
      <div>
        <h4 className="text-sm font-medium text-text-primary">{title}</h4>
      </div>
      <div className="bg-bg-tertiary/60 border border-border-light rounded-lg p-3">
        {items || <p className="text-sm text-text-muted">{emptyState}</p>}
      </div>
    </section>
  );
}

export function SelectStep({
  isDragOver,
  onDrop,
  onDragOver,
  onDragLeave,
  onClick,
  fileInputRef,
  onInputChange,
  selectedFile,
  parsedConfig,
  mode,
  onModeChange,
  catalog,
  selectedGroupIds,
  selectedProviderIds,
  onToggleGroup,
  onToggleProvider,
  importing,
  previewing,
}: {
  isDragOver: boolean;
  onDrop: (e: React.DragEvent) => void;
  onDragOver: (e: React.DragEvent) => void;
  onDragLeave: (e: React.DragEvent) => void;
  onClick: () => void;
  fileInputRef: React.RefObject<HTMLInputElement | null>;
  onInputChange: (e: React.ChangeEvent<HTMLInputElement>) => void;
  selectedFile: File | null;
  parsedConfig: ExportedConfig | null;
  mode: ImportMode;
  onModeChange: (mode: ImportMode) => void;
  catalog: SelectionCatalog;
  selectedGroupIds: string[];
  selectedProviderIds: string[];
  onToggleGroup: (id: string) => void;
  onToggleProvider: (id: string) => void;
  importing: boolean;
  previewing: boolean;
}) {
  const scopeLocked = importing || previewing;

  if (!selectedFile || !parsedConfig) {
    return (
      <div
        className={`border-2 border-dashed rounded-xl p-8 text-center transition-colors cursor-pointer ${
          isDragOver
            ? "border-primary bg-primary/5"
            : "border-border-light hover:border-primary/50 hover:bg-bg-tertiary/50"
        }`}
        onDrop={onDrop}
        onDragOver={onDragOver}
        onDragLeave={onDragLeave}
        onClick={onClick}
      >
        <input
          ref={fileInputRef}
          type="file"
          accept=".json,application/json"
          onChange={onInputChange}
          className="hidden"
        />
        <div className="flex flex-col items-center gap-3">
          <UploadIcon />
          <div>
            <p className="text-text-primary font-medium">
              拖拽文件到这里，或点击选择
            </p>
            <p className="text-sm text-text-muted mt-1">
              支持 .json 格式的配置文件
            </p>
          </div>
        </div>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <input
        ref={fileInputRef}
        type="file"
        accept=".json,application/json"
        onChange={onInputChange}
        className="hidden"
      />

      <FileCard
        selectedFile={selectedFile}
        actionLabel="重新选择文件"
        onAction={onClick}
        disabled={scopeLocked}
      />

      <fieldset className="space-y-3">
        <legend className="text-sm font-medium text-text-secondary">
          导入方式
        </legend>
        <div className="grid gap-3">
          {IMPORT_MODE_OPTIONS.map((option) => {
            const checked = option.mode === mode;
            return (
              <label
                key={option.mode}
                className={`border rounded-lg p-4 transition-colors ${
                  checked
                    ? "border-primary bg-primary/5"
                    : "border-border-light hover:border-primary/40"
                } ${scopeLocked ? "cursor-not-allowed opacity-60" : "cursor-pointer"} ${
                  scopeLocked ? "hover:border-border-light" : ""
                }`}
              >
                <input
                  type="radio"
                  name="import-mode"
                  value={option.mode}
                  checked={checked}
                  disabled={scopeLocked}
                  onChange={() => onModeChange(option.mode)}
                  className="sr-only"
                />
                <div className="flex items-start justify-between gap-3">
                  <div>
                    <p className="text-sm font-medium text-text-primary">
                      {option.title}
                    </p>
                    <p className="text-sm text-text-secondary mt-1">
                      {option.description}
                    </p>
                  </div>
                  <span
                    className={`mt-1 h-4 w-4 rounded-full border ${
                      checked
                        ? "border-primary bg-primary"
                        : "border-border-light bg-bg-secondary"
                    }`}
                    aria-hidden="true"
                  />
                </div>
              </label>
            );
          })}
        </div>
      </fieldset>

      {mode === "selection" && (
        <div className="space-y-4">
          <div className="bg-bg-tertiary border border-border-light rounded-lg p-4">
            <p className="text-sm text-text-secondary">
              {formatSelectionSummary(
                selectedGroupIds,
                selectedProviderIds,
                catalog,
              )}
            </p>
          </div>

          <div className="grid gap-4 md:grid-cols-2">
            <ScopeSelectionList
              title="Groups"
              emptyState="文件中没有可选 Group。"
              items={
                catalog.groups.length > 0 ? (
                  <div className="space-y-2">
                    {catalog.groups.map((group) => (
                      <label
                        key={group.id}
                        className={`flex items-start gap-3 rounded-lg p-2 ${
                          scopeLocked
                            ? "cursor-not-allowed opacity-60"
                            : "cursor-pointer hover:bg-bg-secondary/60"
                        }`}
                      >
                        <input
                          type="checkbox"
                          checked={selectedGroupIds.includes(group.id)}
                          disabled={scopeLocked}
                          onChange={() => onToggleGroup(group.id)}
                          className="mt-1 h-4 w-4 rounded border-border-light bg-bg-secondary"
                        />
                        <div>
                          <p className="text-sm font-medium text-text-primary">
                            {group.name}
                          </p>
                          <p className="text-xs text-text-muted">
                            {group.providerCount} 个 Provider
                          </p>
                        </div>
                      </label>
                    ))}
                  </div>
                ) : null
              }
            />

            <ScopeSelectionList
              title="Providers"
              emptyState="文件中没有可选 Provider。"
              items={
                catalog.providers.length > 0 ? (
                  <div className="space-y-2">
                    {catalog.providers.map((provider) => (
                      <label
                        key={provider.id}
                        className={`flex items-start gap-3 rounded-lg p-2 ${
                          scopeLocked
                            ? "cursor-not-allowed opacity-60"
                            : "cursor-pointer hover:bg-bg-secondary/60"
                        }`}
                      >
                        <input
                          type="checkbox"
                          checked={selectedProviderIds.includes(provider.id)}
                          disabled={scopeLocked}
                          onChange={() => onToggleProvider(provider.id)}
                          className="mt-1 h-4 w-4 rounded border-border-light bg-bg-secondary"
                        />
                        <div>
                          <p className="text-sm font-medium text-text-primary">
                            {provider.name}
                          </p>
                          <p className="text-xs text-text-muted">
                            {provider.groupName
                              ? `所属 Group: ${provider.groupName}`
                              : "未绑定 Group，将只导入该 Provider"}
                          </p>
                        </div>
                      </label>
                    ))}
                  </div>
                ) : null
              }
            />
          </div>
        </div>
      )}
    </div>
  );
}
