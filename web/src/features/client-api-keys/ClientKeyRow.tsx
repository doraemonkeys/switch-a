import { useState } from "react";
import type { ClientAPIKey } from "@/api/client-api-keys/types";
import { CopyButton } from "@/components/CopyButton";

interface Props {
  item: ClientAPIKey;
  busy: boolean;
  rename: (id: string, name: string) => Promise<boolean>;
  remove: (item: ClientAPIKey) => void;
}

export function ClientKeyRow({ item, busy, rename, remove }: Props) {
  const [revealed, setRevealed] = useState(false);
  const [editing, setEditing] = useState(false);
  const [name, setName] = useState(item.name);

  return (
    <li className="space-y-3 rounded-lg border border-border-light p-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h3 className="font-semibold">{item.name}</h3>
          <p className="text-xs text-text-muted">
            Created {new Date(item.created_at).toLocaleString()}
          </p>
        </div>
        <div className="flex gap-3">
          <button
            type="button"
            className="btn-secondary"
            disabled={busy}
            onClick={() => {
              setName(item.name);
              setEditing(!editing);
            }}
          >
            Rename
          </button>
          <button
            type="button"
            className="btn-danger"
            disabled={busy}
            onClick={() => remove(item)}
          >
            Delete
          </button>
        </div>
      </div>
      <div className="flex flex-wrap items-center gap-3">
        <code className="min-w-0 break-all text-sm">
          {revealed ? item.key : "••••••••••••••••"}
        </code>
        <button
          type="button"
          className="text-sm text-primary"
          aria-label={
            revealed
              ? "Hide key for " + item.name
              : "Reveal key for " + item.name
          }
          onClick={() => setRevealed(!revealed)}
        >
          {revealed ? "Hide" : "Reveal"}
        </button>
        <CopyButton text={item.key} />
      </div>
      {editing && (
        <form
          className="flex flex-wrap gap-2"
          onSubmit={(event) => {
            event.preventDefault();
            void rename(item.id, name).then((saved) => {
              if (saved) setEditing(false);
            });
          }}
        >
          <input
            className="input flex-1"
            aria-label={"New name for " + item.name}
            required
            value={name}
            disabled={busy}
            onChange={(event) => setName(event.target.value)}
          />
          <button className="btn-primary" type="submit" disabled={busy}>
            Save name
          </button>
          <button
            className="btn-secondary"
            type="button"
            disabled={busy}
            onClick={() => setEditing(false)}
          >
            Cancel
          </button>
        </form>
      )}
    </li>
  );
}
