import type { CredentialSession } from "@/api";
export function CredentialDisguiseSummary({
  session,
}: {
  session: CredentialSession;
}) {
  return (
    <div className="mt-2 text-xs text-text-secondary">
      <p>
        {session.client_disguise
          ? "Device: " + session.client_disguise.device_id
          : "Client disguise identity: unbound"}
      </p>
      {session.client_disguise?.revision_id && (
        <p>
          Profile: {session.client_disguise.revision_id} (
          {session.client_disguise.mode})
        </p>
      )}
      <a
        className="text-primary underline"
        href={"/admin/client-disguise?login=" + encodeURIComponent(session.id)}
      >
        Manage client disguise
      </a>
    </div>
  );
}
