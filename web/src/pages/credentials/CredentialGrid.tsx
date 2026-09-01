import type { CredentialSession } from "../../api";
import { CredentialCard } from "./CredentialCard";

interface CredentialGridProps {
  sessions: CredentialSession[];
  disabled: boolean;
  onReconnect: (session: CredentialSession) => void;
  onRename: (session: CredentialSession) => void;
  onRotate: (session: CredentialSession) => void;
  onDelete: (session: CredentialSession) => void;
}

export function CredentialGrid({
  sessions,
  disabled,
  onReconnect,
  onRename,
  onRotate,
  onDelete,
}: CredentialGridProps) {
  return (
    <div className="grid grid-cols-1 gap-5 md:grid-cols-2 lg:grid-cols-3">
      {sessions.map((session) => (
        <CredentialCard
          key={session.id}
          session={session}
          disabled={disabled}
          onReconnect={() => onReconnect(session)}
          onRename={() => onRename(session)}
          onRotate={() => onRotate(session)}
          onDelete={() => onDelete(session)}
        />
      ))}
    </div>
  );
}
