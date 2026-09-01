import { useState } from "react";
import { Eye, EyeOff, Plus, X } from "lucide-react";
import type { CreateCredentialSessionInput } from "../../api";

interface CredentialCreateModalProps {
  isOpen: boolean;
  busy: boolean;
  onClose: () => void;
  onSubmit: (input: CreateCredentialSessionInput) => Promise<void>;
}

export function CredentialCreateModal({
  isOpen,
  busy,
  onClose,
  onSubmit,
}: CredentialCreateModalProps) {
  const [name, setName] = useState("");
  const [secretData, setSecretData] = useState("");
  const [showSecret, setShowSecret] = useState(false);

  if (!isOpen) return null;

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!name.trim() || !secretData.trim() || busy) return;

    await onSubmit({
      name: name.trim(),
      kind: "api_key",
      secret_data: secretData.trim(),
    });
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4 backdrop-blur-xs">
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby="create-credential-title"
        className="w-full max-w-lg overflow-hidden rounded-2xl border border-border bg-white shadow-2xl transition-all"
      >
        {/* Header */}
        <div className="flex items-center justify-between border-b border-border/80 p-5">
          <div className="flex items-center gap-3">
            <div className="flex h-10 w-10 items-center justify-center rounded-xl bg-primary-light text-primary ring-1 ring-primary/10">
              <Plus className="h-5 w-5" />
            </div>
            <div>
              <h3
                id="create-credential-title"
                className="text-lg font-bold text-text-primary"
              >
                Add New Credential
              </h3>
              <p className="text-xs text-text-secondary">
                Create a reusable API key credential session
              </p>
            </div>
          </div>

          <button
            type="button"
            onClick={onClose}
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
            <div>
              <label
                className="block text-xs font-semibold uppercase tracking-wider text-text-secondary"
                htmlFor="credential-create-name"
              >
                Credential Name
              </label>
              <input
                id="credential-create-name"
                autoFocus
                className="input mt-2 text-sm"
                type="text"
                maxLength={120}
                placeholder="e.g. Claude 3.7 Production Key"
                value={name}
                onChange={(e) => setName(e.target.value)}
                required
              />
              <p className="mt-1.5 text-xs text-text-muted">
                A memorable label shown in route configurations and logs.
              </p>
            </div>

            <div>
              <label
                className="block text-xs font-semibold uppercase tracking-wider text-text-secondary"
                htmlFor="credential-create-secret"
              >
                API Secret Key
              </label>
              <div className="relative mt-2">
                <input
                  id="credential-create-secret"
                  className="input pr-10 font-mono text-sm"
                  type={showSecret ? "text" : "password"}
                  placeholder="sk-..."
                  value={secretData}
                  onChange={(e) => setSecretData(e.target.value)}
                  required
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
              <p className="mt-1.5 text-xs text-text-muted">
                The raw API token (e.g. OpenAI / Anthropic / DeepSeek token).
              </p>
            </div>
          </div>

          {/* Footer Actions */}
          <div className="flex items-center justify-end gap-3 border-t border-border/80 bg-bg-secondary/40 px-6 py-4">
            <button
              type="button"
              className="btn btn-secondary text-sm"
              disabled={busy}
              onClick={onClose}
            >
              Cancel
            </button>
            <button
              type="submit"
              className="btn btn-primary text-sm shadow-xs"
              disabled={busy || !name.trim() || !secretData.trim()}
            >
              {busy ? "Creating..." : "Create Credential"}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}
