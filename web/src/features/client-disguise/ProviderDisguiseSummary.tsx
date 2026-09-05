import type { Provider } from "@/api/types";
import { DEFAULT_DISGUISE_POLICY } from "@/api/client-disguise/types";
export function ProviderDisguiseSummary({ provider }: { provider: Provider }) {
  const policy = provider.client_disguise ?? DEFAULT_DISGUISE_POLICY;
  const login =
    provider.api_types.find((item) => item.api_type === "codex")
      ?.credential_session_id ?? "";
  return (
    <section className="space-y-2 rounded-lg border border-border p-4 text-sm">
      <h3 className="font-semibold">
        Client disguise: {policy.enabled ? "enabled" : "disabled"}
      </h3>
      <p>
        Platform matching: {policy.match_platform === false ? "off" : "on"} ·
        Unknown platform:{" "}
        {policy.unknown_platform === "allow_current"
          ? "use current profile"
          : "exclude candidate"}
      </p>
      <p className="text-text-muted">
        Each new request or connection takes the login's current profile.
        Platform exclusions skip candidates; conversion failures terminate the
        request and retain diagnostics.
      </p>
      <a
        className="text-primary underline"
        href={"/admin/client-disguise?login=" + encodeURIComponent(login)}
      >
        View effective login identity and profile
      </a>
    </section>
  );
}
