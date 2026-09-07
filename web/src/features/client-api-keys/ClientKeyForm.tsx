import { useState, type FormEvent } from "react";

interface Props {
  busy: boolean;
  create: (name: string, key?: string) => Promise<boolean>;
}

export function ClientKeyForm({ busy, create }: Props) {
  const [kind, setKind] = useState<"existing" | "generate">("generate");
  const [name, setName] = useState("");
  const [key, setKey] = useState("");
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (await create(name, kind === "existing" ? key : undefined)) {
      setName("");
      setKey("");
    }
  };

  return (
    <section className="rounded-xl border border-border-light bg-bg-secondary p-5">
      <h2 className="text-lg font-semibold">Add a client key</h2>
      <form onSubmit={(event) => void submit(event)} className="mt-4 space-y-4">
        <fieldset disabled={busy} className="space-y-4">
          <div className="flex flex-wrap gap-4">
            <label className="flex items-center gap-2">
              <input
                type="radio"
                name="key-source"
                checked={kind === "generate"}
                onChange={() => setKind("generate")}
              />
              Generate a new key
            </label>
            <label className="flex items-center gap-2">
              <input
                type="radio"
                name="key-source"
                checked={kind === "existing"}
                onChange={() => setKind("existing")}
              />
              Add an existing key
            </label>
          </div>
          <label className="block">
            Key name
            <input
              className="input mt-1 w-full"
              required
              value={name}
              onChange={(event) => setName(event.target.value)}
              placeholder="Work laptop"
            />
          </label>
          {kind === "existing" && (
            <label className="block">
              Existing API key
              <input
                className="input mt-1 w-full font-mono"
                required
                autoComplete="off"
                spellCheck={false}
                value={key}
                onChange={(event) => setKey(event.target.value)}
              />
            </label>
          )}
          <button className="btn-primary" type="submit" disabled={busy}>
            {kind === "generate" ? "Generate key" : "Add key"}
          </button>
        </fieldset>
      </form>
    </section>
  );
}
