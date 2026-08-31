import { useContext, useEffect, useReducer, useRef } from "react";
import type {
  CredentialSession,
  ProviderAuthView,
  ProviderCredentialSession,
} from "../api";
import type { ApiClient } from "../api/client";
import { ApiContext } from "../api/context";
import {
  resolveCredentialSessionAuthView,
  resolveLoginAuthView,
} from "../lib/providerAuth";

export type ChatGPTCredentialDraft =
  | { kind: "none" }
  | { kind: "credential_session"; credentialSessionID: string }
  | { kind: "credential_login"; credentialLoginID: string };

export interface ChatGPTCredentialSessionTarget {
  sessionID: string;
  expectedVersion: number;
}

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
const CHATGPT_REAUTHENTICATION_COMPLETED_MESSAGE =
  "GPT credential session reconnected. Provider routes were not changed.";
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

interface UseChatGPTCredentialLoginArgs {
  enabled: boolean;
  initialAuthView: ProviderAuthView | null;
  initialCredentialSession: ChatGPTCredentialSessionTarget | null;
}

type CompletedLoginPersistence =
  | { kind: "provider_draft"; authView: ProviderAuthView | null }
  | { kind: "reauthenticated"; session: CredentialSession };

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

function describeReauthenticatedChatGPTAccount(
  session: ProviderCredentialSession,
): string {
  return session.auth_state.email
    ? `Reconnected as ${session.auth_state.email}. Provider routes were not changed.`
    : CHATGPT_REAUTHENTICATION_COMPLETED_MESSAGE;
}

function credentialSessionDraft(sessionID: string): ChatGPTCredentialDraft {
  return sessionID
    ? { kind: "credential_session", credentialSessionID: sessionID }
    : { kind: "none" };
}

interface ChatGPTLoginControllerState {
  status: string | null;
  error: string | null;
  starting: boolean;
  applying: boolean;
  loginSession: ChatGPTLoginSession | null;
  pendingAuth: ProviderAuthView | null;
  lastReauthenticatedSession: CredentialSession | null;
  credential: ChatGPTCredentialDraft;
  reauthenticationTarget: ChatGPTCredentialSessionTarget | null;
}

type ChatGPTLoginAction =
  | { type: "start_requested" }
  | { type: "start_ready"; session: ChatGPTLoginSession }
  | { type: "start_failed"; message: string }
  | { type: "completion_started"; reauthenticating: boolean }
  | {
      type: "completion_succeeded";
      loginID: string;
      result: CompletedLoginPersistence;
    }
  | { type: "completion_failed"; message: string }
  | { type: "login_expired" }
  | { type: "poll_failed"; message: string }
  | {
      type: "session_selected";
      target: ChatGPTCredentialSessionTarget | null;
    }
  | { type: "session_adopted"; sessionID: string };

function initialChatGPTLoginState({
  initialAuthView,
  initialCredentialSession,
}: UseChatGPTCredentialLoginArgs): ChatGPTLoginControllerState {
  return {
    status: describePersistedChatGPTAccount(initialAuthView),
    error: null,
    starting: false,
    applying: false,
    loginSession: null,
    pendingAuth: null,
    lastReauthenticatedSession: null,
    credential: credentialSessionDraft(
      initialCredentialSession?.sessionID ?? "",
    ),
    reauthenticationTarget: initialCredentialSession,
  };
}

function chatGPTLoginReducer(
  state: ChatGPTLoginControllerState,
  action: ChatGPTLoginAction,
): ChatGPTLoginControllerState {
  switch (action.type) {
    case "start_requested":
      return {
        ...state,
        loginSession: null,
        starting: true,
        error: null,
        pendingAuth: null,
      };
    case "start_ready":
      return {
        ...state,
        loginSession: action.session,
        starting: false,
        status: CHATGPT_LOGIN_READY_MESSAGE,
      };
    case "start_failed":
      return {
        ...state,
        loginSession: null,
        starting: false,
        status: null,
        error: action.message,
      };
    case "completion_started":
      return {
        ...state,
        loginSession: null,
        error: null,
        applying: action.reauthenticating,
      };
    case "completion_succeeded":
      if (action.result.kind === "reauthenticated") {
        return {
          ...state,
          applying: false,
          pendingAuth: resolveCredentialSessionAuthView(action.result.session),
          status: describeReauthenticatedChatGPTAccount(action.result.session),
          credential: credentialSessionDraft(action.result.session.id),
          reauthenticationTarget: {
            sessionID: action.result.session.id,
            expectedVersion: action.result.session.version,
          },
          lastReauthenticatedSession: action.result.session,
        };
      }
      return {
        ...state,
        applying: false,
        pendingAuth: action.result.authView,
        status: describeConnectedChatGPTAccount(action.result.authView),
        credential: {
          kind: "credential_login",
          credentialLoginID: action.loginID,
        },
      };
    case "completion_failed":
      return {
        ...state,
        applying: false,
        pendingAuth: null,
        status: null,
        error: action.message,
      };
    case "login_expired":
      return {
        ...state,
        loginSession: null,
        pendingAuth: null,
        status: null,
        error: CHATGPT_LOGIN_EXPIRED_MESSAGE,
      };
    case "poll_failed":
      return { ...state, error: action.message };
    case "session_selected":
      return {
        ...state,
        loginSession: null,
        pendingAuth: null,
        status: null,
        error: null,
        starting: false,
        applying: false,
        credential: credentialSessionDraft(action.target?.sessionID ?? ""),
        reauthenticationTarget: action.target,
      };
    case "session_adopted":
      return {
        ...state,
        credential: credentialSessionDraft(action.sessionID),
        reauthenticationTarget: null,
      };
  }
}

interface LoginCompletionContext {
  api: ApiClient;
  loginID: string;
  authView: ProviderAuthView | null;
  target: ChatGPTCredentialSessionTarget | null;
  generation: number;
  loginGeneration: { current: number };
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
  dispatch,
  fallbackErrorMessage,
}: LoginCompletionContext): Promise<boolean> {
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
  }
}

function useChatGPTLoginPolling(
  api: ApiClient | null,
  enabled: boolean,
  state: ChatGPTLoginControllerState,
  loginGeneration: { current: number },
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
    session,
    targetSessionID,
    targetVersion,
  ]);
}

function useChatGPTLoginActions(
  api: ApiClient | null,
  state: ChatGPTLoginControllerState,
  loginGeneration: { current: number },
  dispatch: (action: ChatGPTLoginAction) => void,
) {
  const handleStartChatGPTLogin = async () => {
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
    ++loginGeneration.current;
    dispatch({ type: "session_selected", target });
  };
  const adoptMaterializedCredentialSession = (sessionID: string) =>
    dispatch({ type: "session_adopted", sessionID });

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
  const [state, dispatch] = useReducer(
    chatGPTLoginReducer,
    args,
    initialChatGPTLoginState,
  );
  useChatGPTLoginPolling(api, args.enabled, state, loginGeneration, dispatch);
  const actions = useChatGPTLoginActions(api, state, loginGeneration, dispatch);

  return {
    chatGPTStatus: state.status,
    chatGPTLoginError: state.error,
    startingChatGPTLogin: state.starting,
    applyingChatGPTLogin: state.applying,
    chatGPTLoginAuthURL: state.loginSession?.authURL ?? null,
    pendingChatGPTAuth: state.pendingAuth,
    lastReauthenticatedSession: state.lastReauthenticatedSession,
    credential: state.credential,
    ...actions,
  };
}
