import type { ClientAPIKeyPolicyChange } from "@/api/config-transfer-types";

export function ClientAccessNotice({
  policy,
  empty,
}: {
  policy?: ClientAPIKeyPolicyChange;
  empty?: boolean;
}) {
  const label = (mode: string) =>
    mode === "restricted" ? "Configured keys only" : "Allow any key";
  return (
    <div className="rounded-lg border border-border-light p-4 text-sm">
      <p className="font-medium">Client gateway access</p>
      {policy ? (
        <>
          <p>
            {label(policy.from)} → {label(policy.to)}
            {policy.from === policy.to ? " (unchanged)" : ""}
          </p>
          <p className="mt-1 text-text-secondary">
            The backup replaces the configured client keys and access policy.
            Changes apply to new requests and connections.
          </p>
          {empty && policy.to === "restricted" && (
            <p className="mt-2 text-warning">
              No keys will remain. All new client requests will be blocked.
            </p>
          )}
        </>
      ) : (
        <p className="text-text-secondary">
          Existing client keys and access policy are preserved.
        </p>
      )}
    </div>
  );
}
