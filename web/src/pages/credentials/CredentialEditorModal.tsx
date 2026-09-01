import { useState } from "react";
import { AlertTriangle, Eye, EyeOff, Pencil, RotateCcw, X } from "lucide-react";
import type { CredentialSession } from "../../api";

export type CredentialEditorState =
  | { kind: "rename"; session: CredentialSession; value: string }
  | { kind: "rotate"; session: CredentialSession; value: string }
  | null;

interface CredentialEditorModalProps {
  editor: Exclude<CredentialEditorState, null>;
  busy: boolean;
  onChange: (value: string) => void;
  onCancel: () => void;
  onSave: () => void;
}

export function CredentialEditorModal({
  editor,
  busy,
  onChange,
  onCancel,
  onSave,
}: CredentialEditorModalProps) {
  const [showSecret, setShowSecret] = useState(false);
  const rotating = editor.kind === "rotate";
  const { session } = editor;
  const references = session.route_references;

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!editor.value.trim() || busy) return;
    onSave();
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4 backdrop-blur-xs">
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby="credential-editor-title"
        className="w-full max-w-lg overflow-hidden rounded-2xl border border-border bg-white shadow-2xl transition-all"
      >
        {/* Header */}
        <div className="flex items-center justify-between border-b border-border/80 p-5">
          <div className="flex items-center gap-3">
            <div
              className={`flex h-10 w-10 items-center justify-center rounded-xl ${
                rotating
                  ? "bg-amber-50 text-amber-600 ring-1 ring-amber-500/10"
                  : "bg-primary-light text-primary ring-1 ring-primary/10"
              }`}
            >
              {rotating ? (
                <RotateCcw className="h-5 w-5" />
              ) : (
                <Pencil className="h-5 w-5" />
              )}
            </div>
            <div>
              <h3
                id="credential-editor-title"
                className="text-lg font-bold text-text-primary"
              >
                {rotating ? "Rotate API Key" : "Rename Credential"}
              </h3>
              <p className="mt-1 text-sm text-text-secondary">
                {rotating
                  ? `This changes every one of the ${editor.session.route_references.length} referenced routes together.`
                  : "The name is shown in provider credential selectors."}
              </p>
            </div>
          </div>

          <button
            type="button"
            onClick={onCancel}
            disabled={busy}
            className="rounded-lg p-1.5 text-text-muted hover:bg-bg-secondary hover:text-text-primary"
            aria-label="Close"
          >
            <X className="h-5 w-5" />
          </button>
        </div>

        {/* Form Body */}
        <form onSubmit={handleSubmit}>
          <div className="space-y-4 p-6">
            {rotating ? (
              <>
                <div className="rounded-xl border border-amber-200/80 bg-amber-50/50 p-4 text-xs text-amber-900 leading-relaxed">
                  <div className="flex items-start gap-2">
                    <AlertTriangle className="h-4 w-4 shrink-0 text-amber-600 mt-0.5" />
                    <div>
                      <p className="font-semibold">
                        Atomic Key Rotation with CAS Concurrency Guard
                      </p>
                      <p className="mt-1 text-amber-800">
                        In-flight requests will seamlessly switch to the newly
                        rotated key without downtime.
                      </p>
                    </div>
                  </div>

                  {references.length > 0 && (
                    <div className="mt-3 border-t border-amber-200/60 pt-2.5">
                      <span className="font-medium text-amber-900">
                        Affected Routes ({references.length}):
                      </span>
                      <div className="mt-1.5 flex flex-wrap gap-1">
                        {references.map((ref) => (
                          <span
                            key={`${ref.provider_id}/${ref.api_type}`}
                            className="rounded-md bg-white/80 px-2 py-0.5 text-[11px] font-medium text-amber-950 border border-amber-200"
                          >
                            {ref.provider_name} · {ref.api_type}
                          </span>
                        ))}
                      </div>
                    </div>
                  )}
                </div>

                <div>
                  <label
                    className="block text-sm font-medium text-text-secondary"
                    htmlFor="credential-editor-value"
                  >
                    New API Key
                  </label>
                  <div className="relative mt-2">
                    <input
                      id="credential-editor-value"
                      autoFocus
                      className="input pr-10 font-mono text-sm"
                      type={showSecret ? "text" : "password"}
                      placeholder="sk-..."
                      value={editor.value}
                      onChange={(e) => onChange(e.target.value)}
                    />
                    <button
                      type="button"
                      onClick={() => setShowSecret(!showSecret)}
                      className="absolute right-3 top-1/2 -translate-y-1/2 text-text-muted hover:text-text-primary"
                      aria-label={showSecret ? "Hide key" : "Show key"}
                    >
                      {showSecret ? (
                        <EyeOff className="h-4 w-4" />
                      ) : (
                        <Eye className="h-4 w-4" />
                      )}
                    </button>
                  </div>
                </div>
              </>
            ) : (
              <div>
                <div className="flex items-center justify-between">
                  <label
                    className="block text-sm font-medium text-text-secondary"
                    htmlFor="credential-editor-value"
                  >
                    Name
                  </label>
                  <span className="text-[11px] text-text-muted">
                    {editor.value.length} / 120
                  </span>
                </div>
                <input
                  id="credential-editor-value"
                  autoFocus
                  className="input mt-2 text-sm"
                  type="text"
                  maxLength={120}
                  placeholder="e.g. Production Claude Key"
                  value={editor.value}
                  onChange={(e) => onChange(e.target.value)}
                />
              </div>
            )}
          </div>

          {/* Footer Actions */}
          <div className="flex items-center justify-end gap-3 border-t border-border/80 bg-bg-secondary/40 px-6 py-4">
            <button
              type="button"
              className="btn btn-secondary text-sm"
              disabled={busy}
              onClick={onCancel}
            >
              Cancel
            </button>
            <button
              type="submit"
              className="btn btn-primary text-sm shadow-xs"
              disabled={busy || !editor.value.trim()}
            >
              {busy ? "Saving..." : "Save"}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}
