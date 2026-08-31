import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ApiClient, CredentialSession } from "../api";
import { ApiContext } from "../api/context";
import {
  type ChatGPTCredentialSessionTarget,
  useChatGPTCredentialLogin,
} from "./useChatGPTCredentialLogin";

interface Deferred<T> {
  promise: Promise<T>;
  resolve: (value: T) => void;
}

function deferred<T>(): Deferred<T> {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((accept) => {
    resolve = accept;
  });
  return { promise, resolve };
}

function chatGPTSession(id: string, version: number): CredentialSession {
  return {
    id,
    name: id,
    kind: "chatgpt",
    version,
    subject: { kind: "account", value: "account-a" },
    auth_state: {
      status: "active",
      email: "a@example.com",
      account_id: "account-a",
    },
    referenced_route_target_ids: [],
    route_references: [],
    created_at: "2026-08-31T00:00:00Z",
    updated_at: "2026-08-31T00:00:00Z",
  };
}

function LoginHarness({
  initialTarget,
}: {
  initialTarget: ChatGPTCredentialSessionTarget | null;
}) {
  const login = useChatGPTCredentialLogin({
    enabled: true,
    initialAuthView: null,
    initialCredentialSession: initialTarget,
  });
  return (
    <>
      <output data-testid="credential">
        {JSON.stringify(login.credential)}
      </output>
      <output data-testid="committing">
        {String(login.committingChatGPTReauthentication)}
      </output>
      <output data-testid="status">{login.chatGPTStatus}</output>
      <button
        type="button"
        onClick={() => void login.handleImportChatGPTLogin("token")}
      >
        Import
      </button>
      <button
        type="button"
        onClick={() =>
          login.selectCredentialSession({
            sessionID: "session-b",
            expectedVersion: 4,
          })
        }
      >
        Select B
      </button>
      <button
        type="button"
        onClick={() => void login.handleStartChatGPTLogin()}
      >
        Start
      </button>
    </>
  );
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe("useChatGPTCredentialLogin transactions", () => {
  it("keeps the reauthentication target immutable after its commit starts", async () => {
    const user = userEvent.setup();
    const pendingReauthentication = deferred<CredentialSession>();
    const reauthenticate = vi
      .fn()
      .mockReturnValue(pendingReauthentication.promise);
    const api = {
      providers: {
        importChatGPTLogin: vi.fn().mockResolvedValue({
          login_id: "login-a",
          status: "completed",
          auth: {
            type: "chatgpt",
            status: "active",
            email: "a@example.com",
            account_id: "account-a",
          },
        }),
      },
      credentialSessions: { reauthenticate },
    } as unknown as ApiClient;

    render(
      <ApiContext.Provider value={api}>
        <LoginHarness
          initialTarget={{ sessionID: "session-a", expectedVersion: 3 }}
        />
      </ApiContext.Provider>,
    );

    await user.click(screen.getByRole("button", { name: "Import" }));
    await waitFor(() =>
      expect(reauthenticate).toHaveBeenCalledWith("session-a", {
        expected_version: 3,
        credential_login_id: "login-a",
      }),
    );
    expect(screen.getByTestId("committing")).toHaveTextContent("true");

    await user.click(screen.getByRole("button", { name: "Select B" }));
    expect(screen.getByTestId("credential")).toHaveTextContent(
      '"credentialSessionID":"session-a"',
    );

    await act(async () => {
      pendingReauthentication.resolve(chatGPTSession("session-a", 4));
      await pendingReauthentication.promise;
    });
    expect(
      await screen.findByText(
        "Reconnected as a@example.com. Provider routes were not changed.",
      ),
    ).toBeInTheDocument();
    expect(screen.getByTestId("committing")).toHaveTextContent("false");
  });

  it("invalidates a staged login and suppresses a late sign-in window", async () => {
    const user = userEvent.setup();
    const pendingStart = deferred<{ login_id: string; auth_url: string }>();
    const openWindow = vi.spyOn(window, "open").mockReturnValue(null);
    const api = {
      providers: {
        importChatGPTLogin: vi.fn().mockResolvedValue({
          login_id: "login-a",
          status: "completed",
          auth: {
            type: "chatgpt",
            status: "active",
            email: "a@example.com",
            account_id: "account-a",
          },
        }),
        startChatGPTLogin: vi.fn().mockReturnValue(pendingStart.promise),
      },
    } as unknown as ApiClient;
    const rendered = render(
      <ApiContext.Provider value={api}>
        <LoginHarness initialTarget={null} />
      </ApiContext.Provider>,
    );

    await user.click(screen.getByRole("button", { name: "Import" }));
    await waitFor(() =>
      expect(screen.getByTestId("credential")).toHaveTextContent(
        '"credentialLoginID":"login-a"',
      ),
    );
    await user.click(screen.getByRole("button", { name: "Start" }));
    expect(screen.getByTestId("credential")).toHaveTextContent('"kind":"none"');

    rendered.unmount();
    await act(async () => {
      pendingStart.resolve({
        login_id: "login-b",
        auth_url: "https://example.com/login-b",
      });
      await pendingStart.promise;
    });
    expect(openWindow).not.toHaveBeenCalled();
  });
});
