import { useRef, useState } from "react";
import type { DragEvent } from "react";
import { FileJson, RefreshCw, ShieldCheck, Upload } from "lucide-react";
import type { ProviderImportFlowState } from "../../lib/providerImport";

interface UploadStepProps {
  state: Extract<ProviderImportFlowState, { phase: "upload" | "previewing" }>;
  onFile: (file: File) => void;
  onRetry: () => void;
}

export function UploadStep({ state, onFile, onRetry }: UploadStepProps) {
  const inputRef = useRef<HTMLInputElement>(null);
  const [dragging, setDragging] = useState(false);
  const previewing = state.phase === "previewing";

  function chooseFile() {
    inputRef.current?.click();
  }

  function handleDrop(event: DragEvent<HTMLDivElement>) {
    event.preventDefault();
    setDragging(false);
    if (previewing) return;
    const file = event.dataTransfer.files.item(0);
    if (file) onFile(file);
  }

  return (
    <div className="space-y-5">
      <input
        ref={inputRef}
        type="file"
        accept=".json,.txt,application/json,text/plain"
        aria-label="sub2api export file"
        className="sr-only"
        tabIndex={-1}
        disabled={previewing}
        onClick={(event) => {
          event.currentTarget.value = "";
        }}
        onChange={(event) => {
          const file = event.target.files?.item(0);
          if (file) onFile(file);
        }}
      />

      <div
        onDrop={handleDrop}
        onDragOver={(event) => {
          event.preventDefault();
          if (!previewing) setDragging(true);
        }}
        onDragLeave={(event) => {
          event.preventDefault();
          setDragging(false);
        }}
        className={`rounded-xl border-2 border-dashed p-8 text-center transition-colors ${
          dragging
            ? "border-primary bg-primary/5"
            : "border-border bg-bg-secondary/60"
        }`}
      >
        <div className="mx-auto mb-4 flex h-12 w-12 items-center justify-center rounded-xl bg-primary-light text-primary">
          {previewing ? (
            <RefreshCw className="h-6 w-6 animate-spin" aria-hidden="true" />
          ) : (
            <Upload className="h-6 w-6" aria-hidden="true" />
          )}
        </div>
        <h3 className="font-semibold text-text-primary">
          {previewing
            ? "Checking accounts and existing providers…"
            : "Drop a sub2api export here"}
        </h3>
        <p className="mt-1 text-sm text-text-secondary">
          {previewing
            ? state.file.name
            : "JSON content exported as a .json or .txt file, up to 5 MB"}
        </p>
        {!previewing && (
          <button
            type="button"
            data-autofocus
            onClick={chooseFile}
            className="btn btn-primary mt-5"
          >
            Choose export file
          </button>
        )}
      </div>

      {state.phase === "upload" && state.error && (
        <div
          role="alert"
          tabIndex={-1}
          data-step-error
          className="rounded-lg border border-danger/20 bg-danger/5 p-3 text-sm text-danger"
        >
          <p>{state.error}</p>
          {state.file && (
            <div className="mt-3 flex items-center justify-between gap-3">
              <span className="flex min-w-0 items-center gap-2 text-text-secondary">
                <FileJson className="h-4 w-4 shrink-0" aria-hidden="true" />
                <span className="truncate">{state.file.name}</span>
              </span>
              <button
                type="button"
                onClick={onRetry}
                className="btn btn-secondary shrink-0"
              >
                Retry
              </button>
            </div>
          )}
        </div>
      )}

      <div className="flex items-start gap-3 rounded-lg border border-border bg-bg-secondary/60 p-4">
        <ShieldCheck
          className="mt-0.5 h-5 w-5 shrink-0 text-success"
          aria-hidden="true"
        />
        <div>
          <p className="text-sm font-medium text-text-primary">
            Credentials stay on this Switch-A server
          </p>
          <p className="mt-1 text-xs leading-relaxed text-text-secondary">
            The file is sent once to create a short-lived, sanitized preview.
            OAuth tokens are never returned to or rendered by this screen.
          </p>
        </div>
      </div>
    </div>
  );
}
