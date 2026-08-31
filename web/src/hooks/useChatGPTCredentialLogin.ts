import { useContext, useEffect, useReducer, useRef } from "react";
import type { ProviderAuthView } from "../api";
import type { ApiClient } from "../api/client";
import { ApiContext } from "../api/context";
import { resolveLoginAuthView } from "../lib/providerAuth";
import {
  chatGPTLoginReducer,
  initialChatGPTLoginState,
  type ChatGPTCredentialSessionTarget,
  type ChatGPTLoginAction,
  type ChatGPTLoginControllerState,
  type CompletedLoginPersistence,
  type UseChatGPTCredentialLoginArgs,
} from "./chatgpt-credential-login/state";
export type {
  ChatGPTCredentialDraft,
  ChatGPTCredentialSessionTarget,
} from "./chatgpt-credential-login/state";

const CHATGPT_LOGIN_POLL_INTERVAL_MS = 1000;
const CHATGPT_LOGIN_WINDOW_TARGET = "_blank";
const CHATGPT_LOGIN_WINDOW_FEATURES = "noopener,noreferrer";
const CHATGPT_LOGIN_STATUS_ERROR_MESSAGE = "Failed to check GPT login status";
const CHATGPT_LOGIN_START_ERROR_MESSAGE = "Failed to start GPT login";
const CHATGPT_LOGIN_IMPORT_ERROR_MESSAGE = "Failed to import GPT token";
const CHATGPT_REAUTHENTICATION_ERROR_MESSAGE =
  "Failed to reconnect GPT credential session";

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

async function persistCompletedChatGPTLogin(
  api: ApiClient,
  loginID: string,
  authView: ProviderAuthView | null,
  target: ChatGPTCredentialSessionTarget | null,
): Promise<CompletedLoginPersistence> {
  if (!target) {
    return { kind: "provider_draft", authView };
  }
  const session = await api.credentialSessions.reauthenticate(
    target.sessionID,
    {
      expected_version: target.expectedVersion,
      credential_login_id: loginID,
    },
  );
  return { kind: "reauthenticated", session };
}

interface LoginCompletionContext {
  api: ApiClient;
  loginID: string;
  authView: ProviderAuthView | null;
  target: ChatGPTCredentialSessionTarget | null;
  generation: number;
  loginGeneration: { current: number };
  reauthenticationCommit: { current: number | null };
  dispatch: (action: ChatGPTLoginAction) => void;
  fallbackErrorMessage: string;
}

async function completeChatGPTLogin({
  api,
  loginID,
  authView,
  target,
  generation,
  loginGeneration,
  reauthenticationCommit,
  dispatch,
  fallbackErrorMessage,
}: LoginCompletionContext): Promise<boolean> {
  if (generation !== loginGeneration.current) {
    return false;
  }
  if (target) {
    // Reauthentication mutates a shared session immediately, so target-changing
    // actions must not turn an accepted commit into a stale background write.
    reauthenticationCommit.current = generation;
  }
  dispatch({ type: "completion_started", reauthenticating: Boolean(target) });
  try {
    const result = await persistCompletedChatGPTLogin(
      api,
      loginID,
      authView,
      target,
    );
    if (generation !== loginGeneration.current) {
      return false;
    }
    dispatch({ type: "completion_succeeded", loginID, result });
    return true;
  } catch (err) {
    if (generation !== loginGeneration.current) {
      return false;
    }
    dispatch({
      type: "completion_failed",
      message: errorMessage(err, fallbackErrorMessage),
    });
    return false;
  } finally {
    if (reauthenticationCommit.current === generation) {
      reauthenticationCommit.current = null;
    }
  }
}

function useChatGPTLoginPolling(
  api: ApiClient | null,
  enabled: boolean,
  state: ChatGPTLoginControllerState,
  loginGeneration: { current: number },
  reauthenticationCommit: { current: number | null },
  dispatch: (action: ChatGPTLoginAction) => void,
) {
  const session = state.loginSession;
  const targetSessionID = state.reauthenticationTarget?.sessionID ?? "";
  const targetVersion = state.reauthenticationTarget?.expectedVersion ?? 0;

  useEffect(() => {
    if (!api || !session || !enabled) {
      return;
    }
    return startChatGPTLoginPolling(api, session.loginId, {
      onCompleted: (loginID, authView) => {
        if (session.generation !== loginGeneration.current) {
          return;
        }
        const target = targetSessionID
          ? { sessionID: targetSessionID, expectedVersion: targetVersion }
          : null;
        void completeChatGPTLogin({
          api,
          loginID,
          authView,
          target,
          generation: session.generation,
          loginGeneration,
          reauthenticationCommit,
          dispatch,
          fallbackErrorMessage: CHATGPT_REAUTHENTICATION_ERROR_MESSAGE,
        });
      },
      onExpired: () => {
        if (session.generation === loginGeneration.current) {
          dispatch({ type: "login_expired" });
        }
      },
      onError: (message) => {
        if (session.generation === loginGeneration.current) {
          dispatch({ type: "poll_failed", message });
        }
      },
    });
  }, [
    api,
    dispatch,
    enabled,
    loginGeneration,
    reauthenticationCommit,
    session,
    targetSessionID,
    targetVersion,
  ]);
}

function useChatGPTLoginActions(
  api: ApiClient | null,
  state: ChatGPTLoginControllerState,
  loginGeneration: { current: number },
  reauthenticationCommit: { current: number | null },
  dispatch: (action: ChatGPTLoginAction) => void,
) {
  const handleStartChatGPTLogin = async () => {
    if (reauthenticationCommit.current !== null) {
      return;
    }
    const generation = ++loginGeneration.current;
    dispatch({ type: "start_requested" });
    try {
      if (!api) {
        throw new Error("API client is unavailable for GPT login");
      }
      const start = await api.providers.startChatGPTLogin();
      if (generation !== loginGeneration.current) {
        return;
      }
      dispatch({
        type: "start_ready",
        session: {
          loginId: start.login_id,
          authURL: start.auth_url,
          generation,
        },
      });
      openChatGPTLoginWindow(start.auth_url);
    } catch (err) {
      if (generation === loginGeneration.current) {
        dispatch({
          type: "start_failed",
          message: errorMessage(err, CHATGPT_LOGIN_START_ERROR_MESSAGE),
        });
      }
    }
  };

  const handleOpenChatGPTLoginPage = () =>
    state.loginSession?.authURL &&
    openChatGPTLoginWindow(state.loginSession.authURL);

  // The boolean lets the token field retain rejected credentials for correction.
  const handleImportChatGPTLogin = async (
    authData: string,
  ): Promise<boolean> => {
    if (reauthenticationCommit.current !== null) {
      return false;
    }
    const generation = ++loginGeneration.current;
    dispatch({ type: "completion_started", reauthenticating: false });
    try {
      if (!api) {
        throw new Error("API client is unavailable for GPT login");
      }
      const result = await api.providers.importChatGPTLogin(authData);
      if (generation !== loginGeneration.current) {
        return false;
      }
      return completeChatGPTLogin({
        api,
        loginID: result.login_id,
        authView: resolveLoginAuthView(result),
        target: state.reauthenticationTarget,
        generation,
        loginGeneration,
        reauthenticationCommit,
        dispatch,
        fallbackErrorMessage: state.reauthenticationTarget
          ? CHATGPT_REAUTHENTICATION_ERROR_MESSAGE
          : CHATGPT_LOGIN_IMPORT_ERROR_MESSAGE,
      });
    } catch (err) {
      if (generation === loginGeneration.current) {
        dispatch({
          type: "completion_failed",
          message: errorMessage(
            err,
            state.reauthenticationTarget
              ? CHATGPT_REAUTHENTICATION_ERROR_MESSAGE
              : CHATGPT_LOGIN_IMPORT_ERROR_MESSAGE,
          ),
        });
      }
      return false;
    }
  };

  const selectCredentialSession = (
    target: ChatGPTCredentialSessionTarget | null,
  ) => {
    if (reauthenticationCommit.current !== null) {
      return;
    }
    ++loginGeneration.current;
    dispatch({ type: "session_selected", target });
  };
  const adoptMaterializedCredentialSession = (sessionID: string) => {
    if (reauthenticationCommit.current !== null) {
      return;
    }
    dispatch({ type: "session_adopted", sessionID });
  };

  return {
    handleStartChatGPTLogin,
    handleOpenChatGPTLoginPage,
    handleImportChatGPTLogin,
    selectCredentialSession,
    adoptMaterializedCredentialSession,
  };
}

// Credential selection and login acquisition share one reducer so a stale login
// cannot silently override the account most recently selected by the user.
export function useChatGPTCredentialLogin(args: UseChatGPTCredentialLoginArgs) {
  const api = useContext(ApiContext);
  const loginGeneration = useRef(0);
  const reauthenticationCommit = useRef<number | null>(null);
  const [state, dispatch] = useReducer(
    chatGPTLoginReducer,
    args,
    initialChatGPTLoginState,
  );
  useEffect(
    () => () => {
      // Starting a login may finish after its modal disappears. Invalidating the
      // generation prevents that orphaned request from opening a browser window.
      ++loginGeneration.current;
    },
    [loginGeneration],
  );
  useChatGPTLoginPolling(
    api,
    args.enabled,
    state,
    loginGeneration,
    reauthenticationCommit,
    dispatch,
  );
  const actions = useChatGPTLoginActions(
    api,
    state,
    loginGeneration,
    reauthenticationCommit,
    dispatch,
  );

  return {
    chatGPTStatus: state.status,
    chatGPTLoginError: state.error,
    startingChatGPTLogin: state.starting,
    applyingChatGPTLogin: state.applying,
    committingChatGPTReauthentication: state.committingReauthentication,
    chatGPTLoginAuthURL: state.loginSession?.authURL ?? null,
    pendingChatGPTAuth: state.pendingAuth,
    lastReauthenticatedSession: state.lastReauthenticatedSession,
    credential: state.credential,
    ...actions,
  };
}
