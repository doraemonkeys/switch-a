import { useEffect, useEffectEvent, useState } from "react";
import type { CredentialSession } from "../../api";
import { CopyButton } from "../../components";
import { useChatGPTCredentialLogin } from "../../hooks/useChatGPTCredentialLogin";
import { resolveCredentialSessionAuthView } from "../../lib/providerAuth";

interface CredentialSessionReauthenticationModalProps {
  session: CredentialSession;
  onClose: () => void;
  onReauthenticated: (session: CredentialSession) => void | Promise<void>;
}

export function CredentialSessionReauthenticationModal({
  session,
  onClose,
  onReauthenticated,
}: CredentialSessionReauthenticationModalProps) {
  const [authData, setAuthData] = useState("");
  const [importing, setImporting] = useState(false);
  const {
    chatGPTStatus,
    chatGPTLoginError,
    startingChatGPTLogin,
    applyingChatGPTLogin,
    committingChatGPTReauthentication,
    chatGPTLoginAuthURL,
    lastReauthenticatedSession,
    handleStartChatGPTLogin,
    handleOpenChatGPTLoginPage,
    handleImportChatGPTLogin,
  } = useChatGPTCredentialLogin({
    enabled: true,
    initialAuthView: resolveCredentialSessionAuthView(session),
    initialCredentialSession: {
      sessionID: session.id,
      expectedVersion: session.version,
    },
  });
  const notifyReauthenticated = useEffectEvent((updated: CredentialSession) =>
    onReauthenticated(updated),
  );

  useEffect(() => {
    if (lastReauthenticatedSession) {
      void notifyReauthenticated(lastReauthenticatedSession);
    }
  }, [lastReauthenticatedSession]);

  const busy = startingChatGPTLogin || applyingChatGPTLogin || importing;
  const importCredential = async () => {
    const trimmed = authData.trim();
    if (!trimmed || busy) return;
    setImporting(true);
    try {
      if (await handleImportChatGPTLogin(trimmed)) {
        setAuthData("");
      }
    } finally {
      setImporting(false);
    }
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4 backdrop-blur-sm">
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby="credential-reauthentication-title"
        className="w-full max-w-xl rounded-xl border border-border bg-white shadow-2xl"
      >
        <div className="border-b border-border p-6">
          <h3
            id="credential-reauthentication-title"
            className="text-xl font-bold text-text-primary"
          >
            Reconnect GPT Credential
          </h3>
          <p className="mt-1 text-sm text-text-secondary">
            Reconnect “{session.name}” in place. Every referenced route recovers
            together, and signing in with a different GPT account is rejected.
          </p>
        </div>

        <div className="space-y-4 p-6">
          <div className="rounded-lg border border-border/70 bg-bg-secondary/40 p-3 text-sm text-text-secondary">
            <p>
              Status:{" "}
              <span className="font-medium">{session.auth_state.status}</span>
            </p>
            {session.auth_state.email && (
              <p>Account: {session.auth_state.email}</p>
            )}
            <p>Referenced routes: {session.route_references.length}</p>
          </div>

          <button
            type="button"
            className="btn btn-secondary w-full justify-center"
            disabled={busy}
            onClick={() => void handleStartChatGPTLogin()}
          >
            {startingChatGPTLogin || applyingChatGPTLogin
              ? "Reconnecting..."
              : "Reconnect with GPT Sign-In"}
          </button>

          {chatGPTLoginAuthURL && (
            <div className="space-y-2 rounded-lg border border-border/70 p-3">
              <input
                aria-label="GPT sign-in link"
                readOnly
                value={chatGPTLoginAuthURL}
                className="input font-mono text-xs"
              />
              <div className="flex items-center justify-between gap-3">
                <button
                  type="button"
                  className="btn btn-secondary"
                  onClick={handleOpenChatGPTLoginPage}
                >
                  Open Sign-In Page
                </button>
                <CopyButton text={chatGPTLoginAuthURL} />
              </div>
            </div>
          )}

          <div className="space-y-2 rounded-lg border border-border/70 p-3">
            <label
              htmlFor="credential-reauthentication-token"
              className="block text-sm font-medium text-text-secondary"
            >
              Import via token
            </label>
            <textarea
              id="credential-reauthentication-token"
              className="input font-mono text-xs"
              rows={4}
              value={authData}
              onChange={(event) => setAuthData(event.target.value)}
              placeholder="Paste Codex auth.json, token JSON, or session output"
              spellCheck={false}
              autoComplete="off"
            />
            <div className="flex justify-end">
              <button
                type="button"
                className="btn btn-secondary"
                disabled={busy || !authData.trim()}
                onClick={() => void importCredential()}
              >
                {importing ? "Importing..." : "Import Token"}
              </button>
            </div>
          </div>

          {chatGPTStatus && (
            <p className="text-sm text-success">{chatGPTStatus}</p>
          )}
          {chatGPTLoginError && (
            <p role="alert" className="text-sm text-danger">
              {chatGPTLoginError}
            </p>
          )}
        </div>

        <div className="flex justify-end border-t border-border p-4">
          <button
            type="button"
            className="btn btn-secondary"
            disabled={committingChatGPTReauthentication}
            onClick={onClose}
          >
            Close
          </button>
        </div>
      </div>
    </div>
  );
}
