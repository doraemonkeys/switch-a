import type { CredentialSession } from "../../api";
import { CredentialTableRow } from "./CredentialTableRow";

interface CredentialTableProps {
  sessions: CredentialSession[];
  disabled: boolean;
  onReconnect: (session: CredentialSession) => void;
  onRename: (session: CredentialSession) => void;
  onRotate: (session: CredentialSession) => void;
  onDelete: (session: CredentialSession) => void;
}

export function CredentialTable({
  sessions,
  disabled,
  onReconnect,
  onRename,
  onRotate,
  onDelete,
}: CredentialTableProps) {
  return (
    <div className="overflow-hidden rounded-2xl border border-border bg-white shadow-xs">
      <div className="overflow-x-auto">
        <table className="w-full min-w-[900px]">
          <thead className="border-b border-border bg-bg-secondary text-left text-[11px] font-bold uppercase tracking-wider text-text-secondary">
            <tr>
              <th className="px-4 py-3.5">Credential</th>
              <th className="px-4 py-3.5">Secret / Account</th>
              <th className="px-4 py-3.5">Status</th>
              <th className="px-4 py-3.5">Route References</th>
              <th className="px-4 py-3.5">Updated</th>
              <th className="px-4 py-3.5 text-right">Actions</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border/60">
            {sessions.map((session) => (
              <CredentialTableRow
                key={session.id}
                session={session}
                disabled={disabled}
                onReconnect={() => onReconnect(session)}
                onRename={() => onRename(session)}
                onRotate={() => onRotate(session)}
                onDelete={() => onDelete(session)}
              />
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
