import {
  DEFAULT_DISGUISE_POLICY,
  type ClientDisguisePolicy,
} from "../../api/client-disguise/types";

export function ProviderDisguiseFields({
  value = DEFAULT_DISGUISE_POLICY,
  onChange,
}: {
  value?: ClientDisguisePolicy;
  onChange: (value: ClientDisguisePolicy) => void;
}) {
  return (
    <fieldset className="space-y-3 rounded-xl border border-border bg-bg-secondary p-4">
      <legend className="px-1 font-semibold text-text-primary">
        Client disguise
      </legend>
      <label className="flex items-center gap-2 text-sm">
        <input
          type="checkbox"
          checked={value.enabled}
          onChange={(event) =>
            onChange({ ...value, enabled: event.target.checked })
          }
        />
        Enable client disguise for this Provider
      </label>
      <p className="text-xs text-text-secondary">
        Providers sharing a credential share its device and profile. This switch
        applies only to this Provider, starting with new requests and
        connections.
      </p>
      <label className="flex items-center gap-2 text-sm">
        <input
          type="checkbox"
          checked={value.match_platform ?? true}
          disabled={!value.enabled}
          onChange={(event) =>
            onChange({ ...value, match_platform: event.target.checked })
          }
        />
        Match the original client platform
      </label>
      <label className="block space-y-1 text-sm">
        Unknown client platform
        <select
          className="input w-full"
          value={value.unknown_platform || "exclude"}
          disabled={!value.enabled || value.match_platform === false}
          onChange={(event) =>
            onChange({
              ...value,
              unknown_platform: event.target
                .value as ClientDisguisePolicy["unknown_platform"],
            })
          }
        >
          <option value="exclude">Exclude this Provider</option>
          <option value="allow_current">Allow the current bound profile</option>
        </select>
      </label>
      <p className="text-xs text-text-secondary">
        Platform exclusions skip a candidate. Conversion failures stop the
        logical request and retain diagnostics; they do not count as upstream
        health failures.
      </p>
      <a
        className="text-sm text-primary hover:underline"
        href="/admin/client-disguise"
      >
        Manage login identities and profiles →
      </a>
    </fieldset>
  );
}
