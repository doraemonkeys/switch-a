import { useState } from "react";
import { ArrowUpRight, KeyRound, Search, X } from "lucide-react";
import type { ClientAPIKey } from "@/api/client-api-keys/types";
import { ClientKeyRow } from "./ClientKeyRow";

interface Props {
  keys: ClientAPIKey[];
  busy: boolean;
  onCreate: () => void;
  rename: (id: string, name: string) => Promise<boolean>;
  remove: (item: ClientAPIKey) => void;
}

export function ClientKeyList({ keys, busy, onCreate, rename, remove }: Props) {
  const [search, setSearch] = useState("");
  const matchingKeys = keys.filter((item) =>
    item.name.toLocaleLowerCase().includes(search.trim().toLocaleLowerCase()),
  );

  return (
    <section
      className="min-w-0 overflow-hidden rounded-2xl border border-slate-200 bg-white"
      aria-label="Configured client keys"
    >
      <header className="flex flex-wrap items-center justify-between gap-4 px-6 py-5">
        <div className="flex items-center gap-2.5">
          <h2 className="text-base font-semibold">Your keys</h2>
          <span
            className="rounded-md bg-slate-100 px-2 py-0.5 text-xs font-medium tabular-nums text-slate-600"
            aria-label={keys.length + " configured keys"}
          >
            {keys.length}
          </span>
        </div>
        {keys.length > 0 && (
          <div className="relative w-full sm:w-60">
            <Search
              size={16}
              className="pointer-events-none absolute top-3 left-3 text-slate-400"
              aria-hidden="true"
            />
            <input
              type="search"
              aria-label="Search keys by name"
              placeholder="Search by name…"
              value={search}
              onChange={(event) => setSearch(event.target.value)}
              className="key-input h-10 pl-9 pr-9 text-xs"
            />
            {search && (
              <button
                type="button"
                className="key-icon-button absolute top-0.5 right-0.5"
                aria-label="Clear search"
                onClick={() => setSearch("")}
              >
                <X size={14} aria-hidden="true" />
              </button>
            )}
          </div>
        )}
      </header>
      {keys.length === 0 ? (
        <div className="flex min-h-80 flex-col items-center justify-center border-t border-slate-100 px-6 py-12 text-center">
          <div className="mb-5 flex size-16 items-center justify-center rounded-2xl border border-slate-200 bg-slate-50 text-slate-400">
            <KeyRound size={28} strokeWidth={1.5} aria-hidden="true" />
          </div>
          <h3 className="text-base font-semibold">No client keys yet</h3>
          <p className="mt-2 max-w-xs text-sm leading-6 text-slate-500">
            Give each app or device its own key so it's easy to recognize and
            manage.
          </p>
          <button
            type="button"
            className="key-button-secondary mt-6"
            disabled={busy}
            onClick={onCreate}
          >
            Create your first key <ArrowUpRight size={16} aria-hidden="true" />
          </button>
        </div>
      ) : (
        <>
          <div className="key-list-columns" aria-hidden="true">
            <span>NAME / CREATED</span>
            <span>API KEY</span>
            <span className="text-right">ACTIONS</span>
          </div>
          {matchingKeys.length === 0 ? (
            <div className="px-6 py-16 text-center">
              <Search
                size={24}
                className="mx-auto mb-3 text-slate-400"
                aria-hidden="true"
              />
              <p className="text-sm font-medium">No matching keys</p>
              <p className="mt-1 text-sm text-slate-500">
                Try a different name or clear your search.
              </p>
              <button
                type="button"
                className="key-button-secondary mt-4"
                onClick={() => setSearch("")}
              >
                Show all keys
              </button>
            </div>
          ) : (
            <ul>
              {matchingKeys.map((item) => (
                <ClientKeyRow
                  key={item.id}
                  item={item}
                  busy={busy}
                  rename={rename}
                  remove={remove}
                />
              ))}
            </ul>
          )}
          <footer className="border-t border-slate-100 px-6 py-3 text-xs text-slate-500">
            Showing {matchingKeys.length} of {keys.length} keys
          </footer>
        </>
      )}
    </section>
  );
}
