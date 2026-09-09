import { useEffect, useState } from "react";
import { useApi } from "@/api/useApi";
import type { DisguiseState } from "@/api/client-disguise/types";

export function useClientDisguise() {
  const api = useApi();
  const [state, setState] = useState<DisguiseState | null>(null);
  const [reload, setReload] = useState(0);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    let active = true;
    api.clientDisguise
      .get()
      .then((value) => {
        if (active) setState(value);
      })
      .catch((reason: unknown) => {
        if (active)
          setError(reason instanceof Error ? reason.message : String(reason));
      });
    return () => {
      active = false;
    };
  }, [api, reload]);

  async function mutate(
    action: () => Promise<unknown>,
    message = "Changes saved.",
  ) {
    setBusy(true);
    setError("");
    setNotice("");
    try {
      await action();
      setState(await api.clientDisguise.get());
      setNotice(message);
      return true;
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : String(reason));
      return false;
    } finally {
      setBusy(false);
    }
  }

  function retry() {
    setError("");
    setReload((value) => value + 1);
  }

  return { api, state, error, notice, busy, mutate, retry };
}
