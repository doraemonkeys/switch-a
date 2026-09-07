import { useState } from "react";
import { Check, Eye, EyeOff, KeyRound, Pencil, Trash2, X } from "lucide-react";
import type { ClientAPIKey } from "@/api/client-api-keys/types";
import { CopyButton } from "@/components/CopyButton";

interface Props {
  item: ClientAPIKey;
  busy: boolean;
  rename: (id: string, name: string) => Promise<boolean>;
  remove: (item: ClientAPIKey) => void;
}

const dateFormat = new Intl.DateTimeFormat(undefined, {
  month: "short",
  day: "numeric",
  year: "numeric",
});

export function ClientKeyRow({ item, busy, rename, remove }: Props) {
  const [revealed, setRevealed] = useState(false);
  const [editing, setEditing] = useState(false);
  const [name, setName] = useState(item.name);

  return (
    <li className="key-row">
      <div className="flex min-w-0 items-center gap-3">
        <span className="flex size-10 shrink-0 items-center justify-center rounded-xl border border-slate-200 bg-slate-50 text-slate-500">
          <KeyRound size={18} aria-hidden="true" />
        </span>
        <div className="min-w-0">
          <h3 className="truncate text-sm font-semibold" title={item.name}>
            {item.name}
          </h3>
          <p className="mt-1 text-xs text-slate-500">
            Created{" "}
            <time
              dateTime={item.created_at}
              title={new Date(item.created_at).toLocaleString()}
            >
              {dateFormat.format(new Date(item.created_at))}
            </time>
          </p>
        </div>
      </div>
      <div className="flex min-w-0 items-center gap-1">
        <code
          className={`min-w-0 flex-1 rounded-md bg-slate-50 px-3 py-2 font-mono text-xs leading-5 text-slate-600 ${revealed ? "break-all whitespace-normal" : "truncate"}`}
          title={revealed ? item.key : undefined}
        >
          {revealed ? item.key : "•••• •••• •••• ••••"}
        </code>
        <button
          type="button"
          className="key-icon-button shrink-0"
          aria-label={
            (revealed ? "Hide key for " : "Reveal key for ") + item.name
          }
          title={revealed ? "Hide key" : "Reveal key"}
          onClick={() => setRevealed(!revealed)}
        >
          {revealed ? (
            <EyeOff size={16} aria-hidden="true" />
          ) : (
            <Eye size={16} aria-hidden="true" />
          )}
        </button>
        <CopyButton text={item.key} className="key-copy-button" />
      </div>
      <div className="flex items-center justify-end gap-1">
        <button
          type="button"
          className="key-icon-button"
          aria-label={"Rename " + item.name}
          title="Rename"
          disabled={busy}
          onClick={() => {
            setName(item.name);
            setEditing(!editing);
          }}
        >
          <Pencil size={16} aria-hidden="true" />
        </button>
        <button
          type="button"
          className="key-icon-button key-delete-button"
          aria-label={"Delete " + item.name}
          title="Delete"
          disabled={busy}
          onClick={() => remove(item)}
        >
          <Trash2 size={16} aria-hidden="true" />
        </button>
      </div>
      {editing && (
        <form
          className="col-span-full flex flex-wrap items-center gap-2 border-t border-slate-100 pt-4"
          onSubmit={(event) => {
            event.preventDefault();
            void rename(item.id, name.trim()).then((saved) => {
              if (saved) setEditing(false);
            });
          }}
        >
          <input
            className="key-input min-w-0 flex-1 basis-48"
            aria-label={"New name for " + item.name}
            required
            autoFocus
            value={name}
            disabled={busy}
            onChange={(event) => setName(event.target.value)}
          />
          <button
            className="key-button-primary"
            type="submit"
            disabled={busy || !name.trim()}
          >
            <Check size={16} aria-hidden="true" /> Save name
          </button>
          <button
            className="key-icon-button"
            type="button"
            aria-label="Cancel rename"
            title="Cancel rename"
            disabled={busy}
            onClick={() => setEditing(false)}
          >
            <X size={16} aria-hidden="true" />
          </button>
        </form>
      )}
    </li>
  );
}
