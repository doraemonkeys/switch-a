import { useState } from "react";
import { changedValues, effectiveConfig, equalConfig } from "./model";
import type { ConfigValues } from "./types";

export function useConfigDraft(
  initialConfig: ConfigValues,
  defaults: ConfigValues,
) {
  const remote = effectiveConfig(initialConfig, defaults);
  const [source, setSource] = useState(remote);
  const [saved, setSaved] = useState(remote);
  const [draft, setDraft] = useState(remote);

  // Query refreshes produce new objects. Compare values so unrelated renders
  // cannot erase edits; preserve edited keys when the server refreshes.
  if (!equalConfig(remote, source)) {
    setSource(remote);
    setSaved(remote);
    setDraft({ ...remote, ...changedValues(draft, saved) });
  }

  return {
    draft,
    saved,
    changes: changedValues(draft, saved),
    change: (key: string, value: string) =>
      setDraft((previous) => ({ ...previous, [key]: value })),
    reset: () => setDraft(saved),
    acceptSaved: (submitted: ConfigValues) => setSaved(submitted),
  };
}
