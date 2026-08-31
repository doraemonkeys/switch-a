import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import type { ReactElement } from "react";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  parseAPICatalog,
  type ApiClient,
  type CredentialSession,
} from "../../api";
import { APICatalogContext, ApiContext } from "../../api/context";
import { PROVIDER_CREDENTIAL_TYPES } from "../../config/constants";
import { ProviderModal } from "./ProviderModal";

const tokenBlob = '{"tokens":{"access_token":"acc","refresh_token":"ref"}}';
const testAPICatalog = parseAPICatalog(
  JSON.parse(
    readFileSync(
      resolve(process.cwd(), "../contracts/internal-error/v1/api-catalog.json"),
      "utf8",
    ),
  ) as unknown,
);

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

function chatGPTSession(
  status: CredentialSession["auth_state"]["status"] = "active",
): CredentialSession {
  return {
    id: "credential-existing-gpt",
    name: "existing@example.com",
    kind: PROVIDER_CREDENTIAL_TYPES.CHATGPT,
    version: 3,
    subject: { kind: "account", value: "account-existing" },
    auth_state: {
      status,
      email: "existing@example.com",
      account_id: "account-existing",
    },
    referenced_route_target_ids: [],
    route_references: [],
    created_at: "2026-08-31T00:00:00Z",
    updated_at: "2026-08-31T00:00:00Z",
  };
}

function transactionAPI(
  session: CredentialSession,
  overrides: {
    startChatGPTLogin?: () => Promise<{ login_id: string; auth_url: string }>;
    reauthenticate?: () => Promise<CredentialSession>;
  } = {},
) {
  const reauthenticate = vi.fn();
  if (overrides.reauthenticate) {
    reauthenticate.mockImplementation(overrides.reauthenticate);
  }
  const startChatGPTLogin = vi.fn();
  if (overrides.startChatGPTLogin) {
    startChatGPTLogin.mockImplementation(overrides.startChatGPTLogin);
  }
  const credentialSessions = {
    list: vi.fn().mockResolvedValue([session]),
    create: vi.fn(),
    reauthenticate,
  };
  const api = {
    providers: {
      importChatGPTLogin: vi.fn().mockResolvedValue({
        login_id: "login-a",
        status: "completed",
        auth: {
          type: PROVIDER_CREDENTIAL_TYPES.CHATGPT,
          status: "active",
          email: "new@example.com",
          account_id: "account-new",
        },
      }),
      startChatGPTLogin,
    },
    credentialSessions,
  } as unknown as ApiClient;
  return { api, credentialSessions };
}

function renderModal(element: ReactElement, api: ApiClient) {
  return render(
    <ApiContext.Provider value={api}>
      <APICatalogContext.Provider
        value={{
          catalog: testAPICatalog,
          loading: false,
          error: null,
          refetch: () => Promise.resolve(),
        }}
      >
        {element}
      </APICatalogContext.Provider>
    </ApiContext.Provider>,
  );
}

async function openGPTForm(
  user: ReturnType<typeof userEvent.setup>,
  sessionStatus: CredentialSession["auth_state"]["status"],
) {
  await user.type(screen.getByLabelText("Name"), "GPT Credential Choice");
  await user.selectOptions(
    screen.getByLabelText("Credential Type"),
    PROVIDER_CREDENTIAL_TYPES.CHATGPT,
  );
  await screen.findByRole("option", {
    name: new RegExp(`existing@example\\.com · ${sessionStatus}`),
  });
}

async function importGPTCredential(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByLabelText("Import via token"));
  await user.paste(tokenBlob);
  await user.click(screen.getByRole("button", { name: /import token/i }));
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe("ProviderModal GPT credential transactions", () => {
  it("cannot save a completed account after starting its replacement", async () => {
    const user = userEvent.setup();
    const pendingStart = deferred<{ login_id: string; auth_url: string }>();
    vi.spyOn(window, "open").mockReturnValue(null);
    const onSubmit = vi.fn().mockResolvedValue(undefined);
    const { api, credentialSessions } = transactionAPI(chatGPTSession(), {
      startChatGPTLogin: () => pendingStart.promise,
    });
    renderModal(
      <ProviderModal onClose={vi.fn()} onSubmit={onSubmit} groups={[]} />,
      api,
    );
    await openGPTForm(user, "active");
    await importGPTCredential(user);
    await screen.findByText(
      "Connected as new@example.com. Save the provider to persist it.",
    );

    await user.click(screen.getByRole("button", { name: "Reconnect GPT" }));
    const save = screen.getByRole("button", { name: /add provider/i });
    expect(save).toBeDisabled();
    expect(credentialSessions.create).not.toHaveBeenCalled();

    await act(async () => {
      pendingStart.resolve({
        login_id: "login-b",
        auth_url: "https://example.com/login-b",
      });
      await pendingStart.promise;
    });
    expect(await screen.findByLabelText("GPT sign-in link")).toHaveValue(
      "https://example.com/login-b",
    );
    expect(save).toBeEnabled();
    await user.click(save);

    expect(
      await screen.findByText(
        "Complete GPT login before saving this provider.",
      ),
    ).toBeInTheDocument();
    expect(credentialSessions.create).not.toHaveBeenCalled();
    expect(onSubmit).not.toHaveBeenCalled();
  });

  it("locks target-changing controls while reauthentication commits", async () => {
    const user = userEvent.setup();
    const pendingReauthentication = deferred<CredentialSession>();
    const onClose = vi.fn();
    const existingSession = chatGPTSession("reauth_required");
    const { api, credentialSessions } = transactionAPI(existingSession, {
      reauthenticate: () => pendingReauthentication.promise,
    });
    renderModal(
      <ProviderModal onClose={onClose} onSubmit={vi.fn()} groups={[]} />,
      api,
    );
    await openGPTForm(user, "reauth_required");
    await user.selectOptions(
      screen.getByLabelText("Credential Session"),
      existingSession.id,
    );
    await importGPTCredential(user);
    await waitFor(() =>
      expect(credentialSessions.reauthenticate).toHaveBeenCalledTimes(1),
    );

    expect(screen.getByLabelText("Credential Session")).toBeDisabled();
    expect(screen.getByRole("button", { name: "Close" })).toBeDisabled();
    expect(
      screen.getByRole("button", { name: /add provider/i }),
    ).toBeDisabled();
    await user.keyboard("{Escape}");
    expect(onClose).not.toHaveBeenCalled();

    await act(async () => {
      pendingReauthentication.resolve({
        ...existingSession,
        version: existingSession.version + 1,
        auth_state: { ...existingSession.auth_state, status: "active" },
      });
      await pendingReauthentication.promise;
    });
    expect(
      await screen.findByText(
        "Reconnected as existing@example.com. Provider routes were not changed.",
      ),
    ).toBeInTheDocument();
    expect(screen.getByLabelText("Credential Session")).toBeEnabled();
    expect(screen.getByRole("button", { name: "Close" })).toBeEnabled();
  });
});
