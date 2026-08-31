import type { CredentialSession, ProviderAuthView } from "../../api";
import { resolveCredentialSessionAuthView } from "../../lib/providerAuth";

export type ChatGPTCredentialDraft =
  | { kind: "none" }
  | { kind: "credential_session"; credentialSessionID: string }
  | { kind: "credential_login"; credentialLoginID: string };

export interface ChatGPTCredentialSessionTarget {
  sessionID: string;
  expectedVersion: number;
}

export interface ChatGPTLoginSession {
  loginId: string;
  authURL: string;
  generation: number;
}

export interface UseChatGPTCredentialLoginArgs {
  enabled: boolean;
  initialAuthView: ProviderAuthView | null;
  initialCredentialSession: ChatGPTCredentialSessionTarget | null;
}

export type CompletedLoginPersistence =
  | { kind: "provider_draft"; authView: ProviderAuthView | null }
  | { kind: "reauthenticated"; session: CredentialSession };

export interface ChatGPTLoginControllerState {
  status: string | null;
  error: string | null;
  starting: boolean;
  applying: boolean;
  committingReauthentication: boolean;
  loginSession: ChatGPTLoginSession | null;
  pendingAuth: ProviderAuthView | null;
  lastReauthenticatedSession: CredentialSession | null;
  credential: ChatGPTCredentialDraft;
  reauthenticationTarget: ChatGPTCredentialSessionTarget | null;
}

export type ChatGPTLoginAction =
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

const CHATGPT_LOGIN_READY_MESSAGE =
  "Sign-in link ready. Open it in any browser on this machine. Switch-A will detect completion automatically.";
const CHATGPT_LOGIN_EXPIRED_MESSAGE =
  "GPT sign-in expired before it was saved. Start a new sign-in link.";
const CHATGPT_LOGIN_COMPLETED_MESSAGE =
  "GPT login completed. Save the provider to persist it.";
const CHATGPT_REAUTHENTICATION_COMPLETED_MESSAGE =
  "GPT credential session reconnected. Provider routes were not changed.";

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

function describeReauthenticatedChatGPTAccount(
  session: CredentialSession,
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

export function initialChatGPTLoginState({
  initialAuthView,
  initialCredentialSession,
}: UseChatGPTCredentialLoginArgs): ChatGPTLoginControllerState {
  return {
    status: describePersistedChatGPTAccount(initialAuthView),
    error: null,
    starting: false,
    applying: false,
    committingReauthentication: false,
    loginSession: null,
    pendingAuth: null,
    lastReauthenticatedSession: null,
    credential: credentialSessionDraft(
      initialCredentialSession?.sessionID ?? "",
    ),
    reauthenticationTarget: initialCredentialSession,
  };
}

export function chatGPTLoginReducer(
  state: ChatGPTLoginControllerState,
  action: ChatGPTLoginAction,
): ChatGPTLoginControllerState {
  switch (action.type) {
    case "start_requested":
      return {
        ...state,
        loginSession: null,
        starting: true,
        applying: false,
        committingReauthentication: false,
        status: null,
        error: null,
        pendingAuth: null,
        credential:
          state.credential.kind === "credential_login"
            ? { kind: "none" }
            : state.credential,
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
        starting: false,
        status: null,
        error: null,
        pendingAuth: null,
        applying: true,
        committingReauthentication: action.reauthenticating,
        credential:
          state.credential.kind === "credential_login"
            ? { kind: "none" }
            : state.credential,
      };
    case "completion_succeeded":
      if (action.result.kind === "reauthenticated") {
        return {
          ...state,
          applying: false,
          committingReauthentication: false,
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
        committingReauthentication: false,
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
        committingReauthentication: false,
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
        committingReauthentication: false,
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
