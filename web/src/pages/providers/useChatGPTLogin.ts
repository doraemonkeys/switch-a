import { useContext, useEffect, useState } from "react";
import type { ProviderAuthView } from "../../api";
import type { ApiClient } from "../../api/client";
import { ApiContext } from "../../api/context";
import { resolveLoginAuthView } from "../../lib/providerAuth";

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

interface ChatGPTLoginSession {
  loginId: string;
  authURL: string;
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
}

// Both acquisition paths converge on one completed login ID. The provider modal
// materializes that proof as a credential session only when the user saves.
export function useChatGPTLogin({
  enabled,
  initialAuthView,
}: UseChatGPTLoginArgs) {
  const api = useContext(ApiContext);

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
  const [credentialLoginID, setCredentialLoginID] = useState("");

  useEffect(() => {
    if (!api || !chatGPTLoginSession || !enabled) {
      return;
    }

    return startChatGPTLoginPolling(api, chatGPTLoginSession.loginId, {
      onCompleted: (loginID, authView) => {
        setChatGPTLoginSession(null);
        setChatGPTLoginError(null);
        setPendingChatGPTAuth(authView);
        setChatGPTStatus(describeConnectedChatGPTAccount(authView));
        setCredentialLoginID(loginID);
      },
      onExpired: () => {
        setChatGPTLoginSession(null);
        setPendingChatGPTAuth(null);
        setChatGPTStatus(null);
        setChatGPTLoginError(CHATGPT_LOGIN_EXPIRED_MESSAGE);
      },
      onError: setChatGPTLoginError,
    });
  }, [api, chatGPTLoginSession, enabled]);

  const handleStartChatGPTLogin = async () => {
    setStartingChatGPTLogin(true);
    setChatGPTLoginError(null);
    try {
      if (!api) {
        throw new Error("API client is unavailable for GPT login");
      }
      setPendingChatGPTAuth(null);
      setCredentialLoginID("");
      const start = await api.providers.startChatGPTLogin();
      setChatGPTLoginSession({
        loginId: start.login_id,
        authURL: start.auth_url,
      });
      const loginWindow = window.open(
        start.auth_url,
        CHATGPT_LOGIN_WINDOW_TARGET,
        CHATGPT_LOGIN_WINDOW_FEATURES,
      );
      if (loginWindow) {
        loginWindow.focus();
      }
      setChatGPTStatus(CHATGPT_LOGIN_READY_MESSAGE);
    } catch (err) {
      setChatGPTLoginSession(null);
      setChatGPTStatus(null);
      setChatGPTLoginError(
        err instanceof Error ? err.message : CHATGPT_LOGIN_START_ERROR_MESSAGE,
      );
    } finally {
      setStartingChatGPTLogin(false);
    }
  };

  const handleOpenChatGPTLoginPage = () => {
    if (!chatGPTLoginSession?.authURL) {
      return;
    }
    const loginWindow = window.open(
      chatGPTLoginSession.authURL,
      CHATGPT_LOGIN_WINDOW_TARGET,
      CHATGPT_LOGIN_WINDOW_FEATURES,
    );
    loginWindow?.focus();
  };

  // Returns whether the import succeeded so the caller can decide whether to clear
  // the pasted credential; errors are surfaced via chatGPTLoginError, not thrown.
  const handleImportChatGPTLogin = async (
    authData: string,
  ): Promise<boolean> => {
    setChatGPTLoginError(null);
    try {
      if (!api) {
        throw new Error("API client is unavailable for GPT login");
      }
      const result = await api.providers.importChatGPTLogin(authData);
      const authView = resolveLoginAuthView(result);
      // Import yields a completed session directly, so stop any OAuth polling and
      // reuse the same save path the popup flow drives via credential_login_id.
      setChatGPTLoginSession(null);
      setPendingChatGPTAuth(authView);
      setChatGPTStatus(describeConnectedChatGPTAccount(authView));
      setCredentialLoginID(result.login_id);
      return true;
    } catch (err) {
      setChatGPTLoginError(
        err instanceof Error ? err.message : CHATGPT_LOGIN_IMPORT_ERROR_MESSAGE,
      );
      return false;
    }
  };

  const clearCredentialLogin = () => {
    setCredentialLoginID("");
  };

  return {
    chatGPTStatus,
    chatGPTLoginError,
    startingChatGPTLogin,
    chatGPTLoginAuthURL: chatGPTLoginSession?.authURL ?? null,
    pendingChatGPTAuth,
    credentialLoginID,
    clearCredentialLogin,
    handleStartChatGPTLogin,
    handleOpenChatGPTLoginPage,
    handleImportChatGPTLogin,
  };
}
