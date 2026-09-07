import { useState, type FormEvent } from "react";
import { ArrowRight, KeyRound, LoaderCircle, WandSparkles } from "lucide-react";
import type { ClientAPIKey } from "@/api/client-api-keys/types";

interface Props {
  busy: boolean;
  error: string | null;
  create: (name: string, key?: string) => Promise<ClientAPIKey | null>;
  onCreated: (item: ClientAPIKey) => void;
  onCancel: () => void;
}

export function ClientKeyForm({
  busy,
  error,
  create,
  onCreated,
  onCancel,
}: Props) {
  const [kind, setKind] = useState<"existing" | "generate">("generate");
  const [name, setName] = useState("");
  const [key, setKey] = useState("");
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    const item = await create(
      name.trim(),
      kind === "existing" ? key : undefined,
    );
    if (item) onCreated(item);
  };

  return (
    <form onSubmit={(event) => void submit(event)}>
      <fieldset disabled={busy} className="space-y-5 p-6">
        <legend className="sr-only">Key details</legend>
        <div className="grid grid-cols-2 gap-1 rounded-xl bg-slate-100 p-1">
          <label className="key-source-option">
            <input
              type="radio"
              name="key-source"
              aria-label="Generate a new key"
              checked={kind === "generate"}
              onChange={() => setKind("generate")}
              className="sr-only"
            />
            <WandSparkles size={16} aria-hidden="true" /> Generate new
          </label>
          <label className="key-source-option">
            <input
              type="radio"
              name="key-source"
              aria-label="Add an existing key"
              checked={kind === "existing"}
              onChange={() => setKind("existing")}
              className="sr-only"
            />
            <KeyRound size={16} aria-hidden="true" /> Use existing
          </label>
        </div>
        <label className="block text-sm font-medium">
          Key name
          <input
            className="key-input mt-2"
            required
            data-key-autofocus
            value={name}
            onChange={(event) => setName(event.target.value)}
            placeholder="e.g. Work laptop"
            aria-describedby="key-name-hint"
          />
        </label>
        <p
          id="key-name-hint"
          className="-mt-3 text-xs leading-5 text-slate-500"
        >
          A name to help you recognize the app or device.
        </p>
        {kind === "existing" ? (
          <label className="block text-sm font-medium">
            Existing API key
            <input
              className="key-input mt-2 font-mono"
              required
              autoComplete="off"
              spellCheck={false}
              value={key}
              onChange={(event) => setKey(event.target.value)}
              placeholder="Paste your client API key"
            />
          </label>
        ) : (
          <p className="rounded-xl bg-slate-50 p-4 text-sm leading-6 text-slate-500">
            A key will be generated for you. You can reveal and copy it again
            from the list at any time.
          </p>
        )}
        {error && (
          <p role="alert" className="key-error">
            {error}
          </p>
        )}
      </fieldset>
      <footer className="flex justify-end gap-3 border-t border-slate-100 px-6 py-4">
        <button
          type="button"
          className="key-button-secondary"
          disabled={busy}
          onClick={onCancel}
        >
          Cancel
        </button>
        <button
          className="key-button-primary"
          type="submit"
          disabled={busy || !name.trim()}
        >
          {busy ? (
            <LoaderCircle
              size={16}
              className="animate-spin"
              aria-hidden="true"
            />
          ) : (
            <ArrowRight size={16} aria-hidden="true" />
          )}
          {kind === "generate" ? "Generate key" : "Add key"}
        </button>
      </footer>
    </form>
  );
}
