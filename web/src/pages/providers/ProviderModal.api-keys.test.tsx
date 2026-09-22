import { describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { PROVIDER_CREDENTIAL_TYPES } from "../../config/constants";
import { ProviderModal } from "./ProviderModal";
import type { ApiClient } from "../../api";
import {
  apiKeySession,
  createCredentialSessionsApi,
  persistedSplitProvider,
  renderModal,
} from "./ProviderModal.test-support";

describe("ProviderModal credential binding precedence", () => {
  it("edits a saved API key directly and retains the replacement after a failed save", async () => {
    const user = userEvent.setup();
    const initial = persistedSplitProvider();
    const onSubmit = vi
      .fn()
      .mockRejectedValueOnce(new Error("Provider save failed"))
      .mockResolvedValue(undefined);
    const credentialSessions = createCredentialSessionsApi([
      apiKeySession("credential-override"),
      apiKeySession("credential-default"),
    ]);
    renderModal(
      <ProviderModal
        initialData={initial}
        onClose={vi.fn()}
        onSubmit={onSubmit}
        groups={[]}
      />,
      { credentialSessions } as unknown as ApiClient,
    );
    const key = await screen.findByLabelText("API key for claude");
    await waitFor(() => expect(key).toHaveValue("secret-credential-override"));
    expect(key).not.toHaveAttribute("readonly");
    await user.clear(key);
    await user.type(key, "replacement-key");
    await user.click(screen.getByRole("button", { name: /save changes/i }));
    expect(await screen.findByText("Provider save failed")).toBeInTheDocument();
    expect(key).toHaveValue("replacement-key");
    await user.click(screen.getByRole("button", { name: /save changes/i }));
    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(2));
    for (const [payload] of onSubmit.mock.calls) {
      expect(payload.new_credential_sessions).toEqual([
        expect.objectContaining({
          kind: "api_key",
          secret_data: "replacement-key",
        }),
      ]);
      expect(payload.api_types).toEqual([
        {
          ...initial.api_types[0],
          credential_session_id: payload.new_credential_sessions[0].id,
        },
        initial.api_types[1],
      ]);
    }
    expect(credentialSessions.create).not.toHaveBeenCalled();
  });

  it("rejects an emptied saved key and lets undo restore its original binding", async () => {
    const user = userEvent.setup();
    const initial = persistedSplitProvider();
    const onSubmit = vi.fn().mockResolvedValue(undefined);
    const credentialSessions = createCredentialSessionsApi([
      apiKeySession("credential-override"),
      apiKeySession("credential-default"),
    ]);
    renderModal(
      <ProviderModal
        initialData={initial}
        onClose={vi.fn()}
        onSubmit={onSubmit}
        groups={[]}
      />,
      { credentialSessions } as unknown as ApiClient,
    );
    const key = await screen.findByLabelText("API key for claude");
    await waitFor(() => expect(key).toBeEnabled());
    await user.clear(key);
    await user.click(screen.getByRole("button", { name: /save changes/i }));
    expect(
      await screen.findByText(
        'API key for "claude" cannot be empty. Enter a key or undo the change.',
      ),
    ).toBeInTheDocument();
    expect(onSubmit).not.toHaveBeenCalled();
    await user.click(
      screen.getByRole("button", { name: "Undo API key change for claude" }),
    );
    expect(key).toHaveValue("secret-credential-override");
    await user.click(screen.getByRole("button", { name: /save changes/i }));
    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
    expect(onSubmit.mock.calls[0][0].api_types).toEqual(initial.api_types);
    expect(onSubmit.mock.calls[0][0].new_credential_sessions).toEqual([]);
  });

  it.each(["select another credential", "restore the original key"])(
    "discards the replacement when the user chooses to %s",
    async (action) => {
      const user = userEvent.setup();
      const initial = persistedSplitProvider();
      const onSubmit = vi.fn().mockResolvedValue(undefined);
      const credentialSessions = createCredentialSessionsApi([
        apiKeySession("credential-override"),
        apiKeySession("credential-default"),
      ]);
      renderModal(
        <ProviderModal
          initialData={initial}
          onClose={vi.fn()}
          onSubmit={onSubmit}
          groups={[]}
        />,
        { credentialSessions } as unknown as ApiClient,
      );
      const key = await screen.findByLabelText("API key for claude");
      await waitFor(() => expect(key).toBeEnabled());
      await user.clear(key);
      await user.type(key, "discarded-key");
      let expectedSessionID = initial.api_types[0].credential_session_id;
      if (action === "select another credential") {
        expectedSessionID = "credential-default";
        await user.selectOptions(
          screen.getByLabelText("Credential session for claude"),
          expectedSessionID,
        );
      } else {
        await user.clear(key);
        await user.type(key, "secret-credential-override");
      }
      expect(key).toHaveValue(`secret-${expectedSessionID}`);
      await user.click(screen.getByRole("button", { name: /save changes/i }));
      await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
      expect(onSubmit.mock.calls[0][0].api_types[0].credential_session_id).toBe(
        expectedSessionID,
      );
      expect(onSubmit.mock.calls[0][0].new_credential_sessions).toEqual([]);
    },
  );

  it("reveals and copies the current API key without creating a replacement", async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn().mockResolvedValue(undefined);
    const credentialSessions = createCredentialSessionsApi([
      apiKeySession("credential-override"),
      apiKeySession("credential-default"),
    ]);
    const api = { credentialSessions } as unknown as ApiClient;

    renderModal(
      <ProviderModal
        initialData={persistedSplitProvider()}
        onClose={vi.fn()}
        onSubmit={onSubmit}
        groups={[]}
      />,
      api,
    );

    const currentKey = await screen.findByLabelText("API key for claude");
    expect(currentKey).toHaveAttribute("type", "password");
    expect(currentKey).toHaveValue("secret-credential-override");

    await user.click(
      screen.getByRole("button", {
        name: "Show API key for claude",
      }),
    );
    expect(currentKey).toHaveAttribute("type", "text");
    await user.click(screen.getAllByRole("button", { name: "Copy" })[0]);
    expect(await navigator.clipboard.readText()).toBe(
      "secret-credential-override",
    );

    await user.click(screen.getByRole("button", { name: /save changes/i }));
    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
    expect(credentialSessions.create).not.toHaveBeenCalled();
  });

  it("preserves existing bindings when a shared key credentials a new route", async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn().mockResolvedValue(undefined);
    const credentialSessions = createCredentialSessionsApi([
      apiKeySession("credential-override"),
      apiKeySession("credential-default"),
    ]);
    const api = { credentialSessions } as unknown as ApiClient;

    renderModal(
      <ProviderModal
        initialData={persistedSplitProvider()}
        onClose={vi.fn()}
        onSubmit={onSubmit}
        groups={[]}
      />,
      api,
    );

    expect(
      screen.queryByLabelText("New Shared API Key"),
    ).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "gemini" }));
    await user.type(screen.getByLabelText("New Shared API Key"), "shared-key");
    await user.clear(screen.getByLabelText("Base URL for gemini"));
    await user.type(
      screen.getByLabelText("Base URL for gemini"),
      "https://gemini.example.com",
    );
    await user.click(screen.getByRole("button", { name: /save changes/i }));

    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
    const submitted = onSubmit.mock.calls[0]?.[0];
    const createdSession = submitted.new_credential_sessions?.[0];
    expect(createdSession).toMatchObject({
      name: "Split Credentials",
      kind: PROVIDER_CREDENTIAL_TYPES.API_KEY,
      secret_data: "shared-key",
    });
    expect(submitted).toEqual(
      expect.objectContaining({
        api_types: [
          {
            api_type: "claude",
            transport: "http",
            base_url: "https://claude.example.com",
            credential_session_id: "credential-override",
          },
          {
            api_type: "codex",
            transport: "http",
            base_url: "https://codex.example.com",
            credential_session_id: "credential-default",
          },
          {
            api_type: "gemini",
            transport: "http",
            base_url: "https://gemini.example.com",
            credential_session_id: createdSession?.id,
          },
        ],
        new_credential_sessions: [createdSession],
      }),
    );
    expect(credentialSessions.create).not.toHaveBeenCalled();
  });

  it("uses a shared key only for new routes without a selected session", async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn().mockResolvedValue(undefined);
    const credentialSessions = createCredentialSessionsApi([
      apiKeySession("credential-selected"),
    ]);
    const api = { credentialSessions } as unknown as ApiClient;

    renderModal(
      <ProviderModal onClose={vi.fn()} onSubmit={onSubmit} groups={[]} />,
      api,
    );

    await user.type(screen.getByLabelText("Name"), "Mixed Bindings");
    await user.type(screen.getByLabelText("New Shared API Key"), "shared-key");
    await user.click(screen.getByRole("button", { name: "claude" }));
    await user.click(screen.getByRole("button", { name: "codex" }));
    await user.type(
      screen.getByLabelText("Base URL for claude"),
      "https://claude.example.com",
    );
    await user.type(
      screen.getByLabelText("Base URL for codex"),
      "https://codex.example.com",
    );
    await user.selectOptions(
      screen.getByLabelText("Credential session for claude"),
      "credential-selected",
    );
    await user.click(screen.getByRole("button", { name: /add provider/i }));

    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
    const submitted = onSubmit.mock.calls[0]?.[0];
    const createdSession = submitted.new_credential_sessions?.[0];
    expect(createdSession).toMatchObject({
      name: "Mixed Bindings",
      kind: PROVIDER_CREDENTIAL_TYPES.API_KEY,
      secret_data: "shared-key",
    });
    expect(submitted).toEqual(
      expect.objectContaining({
        api_types: [
          {
            api_type: "claude",
            transport: "http",
            base_url: "https://claude.example.com",
            credential_session_id: "credential-selected",
          },
          {
            api_type: "codex",
            transport: "http",
            base_url: "https://codex.example.com",
            credential_session_id: createdSession?.id,
          },
        ],
        new_credential_sessions: [createdSession],
      }),
    );
    expect(credentialSessions.create).not.toHaveBeenCalled();
  });

  it("retains a transactional API key draft when the provider write fails", async () => {
    const user = userEvent.setup();
    const onSubmit = vi
      .fn()
      .mockRejectedValueOnce(new Error("provider write failed"))
      .mockResolvedValueOnce(undefined);
    const credentialSessions = createCredentialSessionsApi([]);
    const api = { credentialSessions } as unknown as ApiClient;

    renderModal(
      <ProviderModal onClose={vi.fn()} onSubmit={onSubmit} groups={[]} />,
      api,
    );

    await user.type(screen.getByLabelText("Name"), "Retry Provider");
    await user.type(screen.getByLabelText("New Shared API Key"), "retry-key");
    await user.click(screen.getByRole("button", { name: "claude" }));
    await user.type(
      screen.getByLabelText("Base URL for claude"),
      "https://claude.example.com",
    );

    await user.click(screen.getByRole("button", { name: /add provider/i }));
    await screen.findByText("provider write failed");
    await user.click(screen.getByRole("button", { name: /add provider/i }));
    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(2));

    const first = onSubmit.mock.calls[0]?.[0];
    const second = onSubmit.mock.calls[1]?.[0];
    const firstSession = first.new_credential_sessions?.[0];
    const secondSession = second.new_credential_sessions?.[0];
    expect(firstSession).toMatchObject({
      name: "Retry Provider",
      secret_data: "retry-key",
    });
    expect(secondSession).toMatchObject({
      name: "Retry Provider",
      secret_data: "retry-key",
    });
    expect(secondSession?.id).not.toBe(firstSession?.id);
    expect(first.api_types[0]?.credential_session_id).toBe(firstSession?.id);
    expect(second.api_types[0]?.credential_session_id).toBe(secondSession?.id);
    expect(credentialSessions.create).not.toHaveBeenCalled();
  });
});
