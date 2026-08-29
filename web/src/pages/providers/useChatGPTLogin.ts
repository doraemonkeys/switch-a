import { useContext, useEffect, useRef, useState } from "react";
import type { ProviderAuthView } from "../../api";
import type { ApiClient } from "../../api/client";
import { ApiContext } from "../../api/context";
import { resolveLoginAuthView } from "../../lib/providerAuth";
import type { ChatGPTCredentialDraft } from "./types";

const CHATGPT_LOGIN_POLL_INTERVAL_MS = 1000;
const CHATGPT_LOGIN_WINDOW_TARGET = "_blank";
const CHATGPT_LOGIN_WINDOW_FEATURES = "noopener,noreferrer";
const CHATGPT_LOGIN_READY_MESSAGE =
  "Sign-in link ready. Open it in any browser on this machine. Switch-A will detect completion automatically.";
const CHATGPT_LOGIN_EXPIRED_MESSAGE =
  "GPT sign-in expired before it was saved. Start a new sign-in link.";
const CHATGPT_LOGIN_STATUS_ERROR_MESSAGE = "Failed to check GPT login status";
const CHATGPT_LOGIN_START_ERROR_MESSAGE = "Failed to start GPT login";
const CHATGPT_LOGIN_IMPORT_ERROR_MESSAGE = "Failed to import GPT token";
const CHATGPT_LOGIN_COMPLETED_MESSAGE =
  "GPT login completed. Save the provider to persist it.";

function openChatGPTLoginWindow(authURL: string) {
  const loginWindow = window.open(
    authURL,
    CHATGPT_LOGIN_WINDOW_TARGET,
    CHATGPT_LOGIN_WINDOW_FEATURES,
  );
  loginWindow?.focus();
}

function errorMessage(error: unknown, fallback: string): string {
  return error instanceof Error ? error.message : fallback;
}

interface ChatGPTLoginSession {
  loginId: string;
  authURL: string;
  generation: number;
}

function describeConnectedChatGPTAccount(
  authView?: ProviderAuthView | null,
): string {
  if (authView?.email) {
    return `Connected as ${authView.email}. Save the provider to persist it.`;
  }
  return CHATGPT_LOGIN_COMPLETED_MESSAGE;
}

function describePersistedChatGPTAccount(
  authView?: ProviderAuthView | null,
): string | null {
  if (!authView || authView.status === "not_connected") {
    return null;
  }
  if (authView.status === "reauth_required") {
    return authView.email
      ? `Reconnect required for ${authView.email}.`
      : "Reconnect required for this provider.";
  }
  return authView.email
    ? `Connected as ${authView.email}.`
    : "A GPT account is already connected for this provider.";
}

interface ChatGPTPollHandlers {
  onCompleted: (loginID: string, authView: ProviderAuthView | null) => void;
  onExpired: () => void;
  onError: (message: string) => void;
}

// startChatGPTLoginPolling polls a pending OAuth login until it completes, expires,
// or the returned cleanup runs. Kept outside the hook so the effect body stays small
// and the polling lifecycle is isolated from React state wiring.
function startChatGPTLoginPolling(
  api: ApiClient,
  loginID: string,
  handlers: ChatGPTPollHandlers,
): () => void {
  let cancelled = false;
  let timeoutID: number | undefined;

  const scheduleNextPoll = () => {
    timeoutID = window.setTimeout(
      () => void pollLoginStatus(),
      CHATGPT_LOGIN_POLL_INTERVAL_MS,
    );
  };

  const pollLoginStatus = async () => {
    try {
      const loginStatus = await api.providers.getChatGPTLoginStatus(loginID);
      if (cancelled) {
        return;
      }
      if (loginStatus.status === "completed") {
        handlers.onCompleted(loginID, resolveLoginAuthView(loginStatus));
        return;
      }
      if (loginStatus.status === "expired") {
        handlers.onExpired();
        return;
      }
      scheduleNextPoll();
    } catch (err) {
      if (cancelled) {
        return;
      }
      handlers.onError(
        err instanceof Error ? err.message : CHATGPT_LOGIN_STATUS_ERROR_MESSAGE,
      );
      scheduleNextPoll();
    }
  };

  void pollLoginStatus();

  return () => {
    cancelled = true;
    if (timeoutID !== undefined) {
      window.clearTimeout(timeoutID);
    }
  };
}

interface UseChatGPTLoginArgs {
  enabled: boolean;
  initialAuthView: ProviderAuthView | null;
  initialCredentialSessionID: string;
}

function credentialSessionDraft(sessionID: string): ChatGPTCredentialDraft {
  return sessionID
    ? { kind: "credential_session", credentialSessionID: sessionID }
    : { kind: "none" };
}

// Credential selection and login acquisition share one draft so a stale login
// cannot silently override the account most recently selected by the user.
export function useChatGPTLogin({
  enabled,
  initialAuthView,
  initialCredentialSessionID,
}: UseChatGPTLoginArgs) {
  const api = useContext(ApiContext);
  const loginGeneration = useRef(0);

  const [chatGPTStatus, setChatGPTStatus] = useState<string | null>(() =>
    describePersistedChatGPTAccount(initialAuthView),
  );
  const [chatGPTLoginError, setChatGPTLoginError] = useState<string | null>(
    null,
  );
  const [startingChatGPTLogin, setStartingChatGPTLogin] = useState(false);
  const [chatGPTLoginSession, setChatGPTLoginSession] =
    useState<ChatGPTLoginSession | null>(null);
  const [pendingChatGPTAuth, setPendingChatGPTAuth] =
    useState<ProviderAuthView | null>(null);
  const [credential, setCredential] = useState<ChatGPTCredentialDraft>(() =>
    credentialSessionDraft(initialCredentialSessionID),
  );

  useEffect(() => {
    if (!api || !chatGPTLoginSession || !enabled) {
      return;
    }

    return startChatGPTLoginPolling(api, chatGPTLoginSession.loginId, {
      onCompleted: (loginID, authView) => {
        if (chatGPTLoginSession.generation !== loginGeneration.current) {
          return;
        }
        setChatGPTLoginSession(null);
        setChatGPTLoginError(null);
        setPendingChatGPTAuth(authView);
        setChatGPTStatus(describeConnectedChatGPTAccount(authView));
        setCredential({ kind: "credential_login", credentialLoginID: loginID });
      },
      onExpired: () => {
        if (chatGPTLoginSession.generation !== loginGeneration.current) {
          return;
        }
        setChatGPTLoginSession(null);
        setPendingChatGPTAuth(null);
        setChatGPTStatus(null);
        setChatGPTLoginError(CHATGPT_LOGIN_EXPIRED_MESSAGE);
      },
      onError: (message) => {
        if (chatGPTLoginSession.generation === loginGeneration.current) {
          setChatGPTLoginError(message);
        }
      },
    });
  }, [api, chatGPTLoginSession, enabled]);

  const handleStartChatGPTLogin = async () => {
    const generation = ++loginGeneration.current;
    setChatGPTLoginSession(null);
    setStartingChatGPTLogin(true);
    setChatGPTLoginError(null);
    try {
      if (!api) {
        throw new Error("API client is unavailable for GPT login");
      }
      setPendingChatGPTAuth(null);
      const start = await api.providers.startChatGPTLogin();
      if (generation !== loginGeneration.current) {
        return;
      }
      setChatGPTLoginSession({
        loginId: start.login_id,
        authURL: start.auth_url,
        generation,
      });
      openChatGPTLoginWindow(start.auth_url);
      setChatGPTStatus(CHATGPT_LOGIN_READY_MESSAGE);
    } catch (err) {
      if (generation !== loginGeneration.current) {
        return;
      }
      setChatGPTLoginSession(null);
      setChatGPTStatus(null);
      setChatGPTLoginError(
        errorMessage(err, CHATGPT_LOGIN_START_ERROR_MESSAGE),
      );
    } finally {
      if (generation === loginGeneration.current) {
        setStartingChatGPTLogin(false);
      }
    }
  };

  const handleOpenChatGPTLoginPage = () =>
    chatGPTLoginSession?.authURL &&
    openChatGPTLoginWindow(chatGPTLoginSession.authURL);

  // Returns whether the import succeeded so the caller can decide whether to clear
  // the pasted credential; errors are surfaced via chatGPTLoginError, not thrown.
  const handleImportChatGPTLogin = async (
    authData: string,
  ): Promise<boolean> => {
    const generation = ++loginGeneration.current;
    setChatGPTLoginSession(null);
    setChatGPTLoginError(null);
    try {
      if (!api) {
        throw new Error("API client is unavailable for GPT login");
      }
      const result = await api.providers.importChatGPTLogin(authData);
      if (generation !== loginGeneration.current) {
        return false;
      }
      const authView = resolveLoginAuthView(result);
      // Import yields a completed session directly, so stop any OAuth polling and
      // reuse the same save path the popup flow drives via credential_login_id.
      setChatGPTLoginSession(null);
      setPendingChatGPTAuth(authView);
      setChatGPTStatus(describeConnectedChatGPTAccount(authView));
      setCredential({
        kind: "credential_login",
        credentialLoginID: result.login_id,
      });
      return true;
    } catch (err) {
      if (generation !== loginGeneration.current) {
        return false;
      }
      setChatGPTLoginError(
        errorMessage(err, CHATGPT_LOGIN_IMPORT_ERROR_MESSAGE),
      );
      return false;
    }
  };

  const selectCredentialSession = (sessionID: string) => {
    ++loginGeneration.current;
    setChatGPTLoginSession(null);
    setPendingChatGPTAuth(null);
    setChatGPTStatus(null);
    setChatGPTLoginError(null);
    setStartingChatGPTLogin(false);
    setCredential(credentialSessionDraft(sessionID));
  };

  const adoptMaterializedCredentialSession = (sessionID: string) =>
    setCredential(credentialSessionDraft(sessionID));

  return {
    chatGPTStatus,
    chatGPTLoginError,
    startingChatGPTLogin,
    chatGPTLoginAuthURL: chatGPTLoginSession?.authURL ?? null,
    pendingChatGPTAuth,
    credential,
    selectCredentialSession,
    adoptMaterializedCredentialSession,
    handleStartChatGPTLogin,
    handleOpenChatGPTLoginPage,
    handleImportChatGPTLogin,
  };
}
