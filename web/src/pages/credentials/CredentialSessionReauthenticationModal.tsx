import { useEffect, useEffectEvent, useState } from "react";
import { ExternalLink, RefreshCw, Sparkles, X } from "lucide-react";
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
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4 backdrop-blur-xs">
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby="credential-reauthentication-title"
        className="w-full max-w-xl overflow-hidden rounded-2xl border border-border bg-white shadow-2xl transition-all"
      >
        {/* Header */}
        <div className="flex items-center justify-between border-b border-border/80 p-5">
          <div className="flex items-center gap-3">
            <div className="flex h-10 w-10 items-center justify-center rounded-xl bg-emerald-50 text-emerald-600 ring-1 ring-emerald-500/10">
              <Sparkles className="h-5 w-5" />
            </div>
            <div>
              <h3
                id="credential-reauthentication-title"
                className="text-lg font-bold text-text-primary"
              >
                Reconnect GPT Credential
              </h3>
              <p className="text-xs text-text-secondary">
                {session.name} · Version {session.version}
              </p>
            </div>
          </div>

          <button
            type="button"
            onClick={onClose}
            disabled={committingChatGPTReauthentication}
            className="rounded-lg p-1.5 text-text-muted hover:bg-bg-secondary hover:text-text-primary"
            aria-label="Close"
          >
            <X className="h-5 w-5" />
          </button>
        </div>

        {/* Content Body */}
        <div className="space-y-4 p-6">
          {/* Info Card */}
          <div className="rounded-xl border border-emerald-200/80 bg-emerald-50/40 p-4 text-xs text-emerald-950 space-y-2">
            <div className="flex items-center justify-between">
              <span className="font-semibold">
                In-Place Reauthentication Guard
              </span>
              <span className="rounded-md bg-white/80 px-2 py-0.5 font-mono text-[11px] font-medium border border-emerald-200">
                {session.route_references.length} referenced routes
              </span>
            </div>
            <p className="text-emerald-900 leading-relaxed">
              Reconnect “{session.name}” in place. Every referenced route
              recovers together, and signing in with a different GPT account is
              rejected.
            </p>
            <div className="flex flex-wrap gap-3 pt-1 text-[11px] text-emerald-800">
              <span>
                Status:{" "}
                <strong className="capitalize">
                  {session.auth_state.status}
                </strong>
              </span>
              {session.auth_state.email && (
                <span>
                  Account: <strong>{session.auth_state.email}</strong>
                </span>
              )}
            </div>
          </div>

          {/* Action 1: OAuth Browser Login */}
          <div className="space-y-3 rounded-xl border border-border/80 bg-bg-secondary/40 p-4">
            <h4 className="text-xs font-semibold uppercase tracking-wider text-text-secondary">
              Method 1: Browser Sign-In
            </h4>
            <button
              type="button"
              className="btn btn-primary w-full justify-center text-sm shadow-xs"
              disabled={busy}
              onClick={() => void handleStartChatGPTLogin()}
            >
              {startingChatGPTLogin || applyingChatGPTLogin ? (
                <>
                  <RefreshCw className="h-4 w-4 animate-spin" />
                  Reconnecting...
                </>
              ) : (
                <>
                  <Sparkles className="h-4 w-4" />
                  Reconnect with GPT Sign-In
                </>
              )}
            </button>

            {chatGPTLoginAuthURL && (
              <div className="mt-3 space-y-2 rounded-lg border border-border bg-white p-3">
                <label className="block text-[11px] font-medium text-text-secondary">
                  Authentication URL:
                </label>
                <input
                  aria-label="GPT sign-in link"
                  readOnly
                  value={chatGPTLoginAuthURL}
                  className="input h-8 font-mono text-xs"
                />
                <div className="flex items-center justify-between gap-3 pt-1">
                  <button
                    type="button"
                    className="btn btn-secondary h-8 px-3 text-xs"
                    onClick={handleOpenChatGPTLoginPage}
                  >
                    <ExternalLink className="h-3.5 w-3.5" />
                    Open Sign-In Page
                  </button>
                  <CopyButton text={chatGPTLoginAuthURL} />
                </div>
              </div>
            )}
          </div>

          {/* Action 2: Token Import */}
          <div className="space-y-2 rounded-xl border border-border/80 bg-bg-secondary/40 p-4">
            <h4 className="text-xs font-semibold uppercase tracking-wider text-text-secondary">
              Method 2: Import Token JSON
            </h4>
            <textarea
              id="credential-reauthentication-token"
              className="input bg-white font-mono text-xs"
              rows={3}
              value={authData}
              onChange={(event) => setAuthData(event.target.value)}
              placeholder="Paste Codex auth.json, token JSON, or session output"
              spellCheck={false}
              autoComplete="off"
            />
            <div className="flex justify-end">
              <button
                type="button"
                className="btn btn-secondary h-8 px-3 text-xs"
                disabled={busy || !authData.trim()}
                onClick={() => void importCredential()}
              >
                {importing ? "Importing..." : "Import Token"}
              </button>
            </div>
          </div>

          {/* Feedback & Error Messages */}
          {chatGPTStatus && (
            <div className="rounded-lg bg-emerald-50 p-3 text-xs font-medium text-emerald-700">
              {chatGPTStatus}
            </div>
          )}
          {chatGPTLoginError && (
            <div
              role="alert"
              className="rounded-lg bg-red-50 p-3 text-xs font-medium text-red-700"
            >
              {chatGPTLoginError}
            </div>
          )}
        </div>

        {/* Footer */}
        <div className="flex justify-end border-t border-border/80 bg-bg-secondary/40 px-6 py-4">
          <button
            type="button"
            className="btn btn-secondary text-sm"
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
