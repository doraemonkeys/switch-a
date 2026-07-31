import { act, renderHook, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { ProviderImportCommitResult, ProviderImportPreview } from "../api";
import {
  useProviderImportFlow,
  type ProviderImportGateway,
} from "./useProviderImportFlow";

const preview: ProviderImportPreview = {
  import_id: "import-1",
  expires_at: "2026-07-30T22:00:00Z",
  summary: {
    total: 1,
    ready: 1,
    existing: 0,
    duplicate: 0,
    invalid: 0,
    unsupported: 0,
  },
  warnings: [],
  items: [
    {
      candidate_id: "candidate-1",
      source_index: 0,
      status: "ready",
      name: "Imported Account",
      provider_id: "imported-account",
      email: "import@example.com",
      priority: 1,
      concurrency: 10,
      default_selected: true,
      warnings: [],
    },
  ],
};

const commitResult: ProviderImportCommitResult = {
  import_id: "import-1",
  summary: { created: 1, updated: 0, skipped: 0 },
  items: [
    {
      candidate_id: "candidate-1",
      outcome: "created",
      provider_id: "imported-account",
      name: "Imported Account",
    },
  ],
};

function createGateway(): ProviderImportGateway {
  return {
    preview: vi.fn().mockResolvedValue(preview),
    commit: vi.fn().mockResolvedValue(commitResult),
    discard: vi.fn().mockResolvedValue(undefined),
  };
}

function createFile(contents: string, name = "sub2api-account.txt") {
  const file = new File([contents], name, { type: "text/plain" });
  Object.defineProperty(file, "text", {
    value: vi.fn().mockResolvedValue(contents),
  });
  return file;
}

describe("useProviderImportFlow", () => {
  it("sends source JSON once and retains only the sanitized preview", async () => {
    const gateway = createGateway();
    const source = JSON.stringify({
      accounts: [
        {
          credentials: {
            access_token: "secret-access",
            refresh_token: "secret-refresh",
          },
        },
      ],
    });
    const { result } = renderHook(() =>
      useProviderImportFlow({ gateway, existingProviderIds: [] }),
    );

    await act(async () => {
      await result.current.previewFile(createFile(source));
    });

    expect(gateway.preview).toHaveBeenCalledWith(source);
    expect(result.current.state.phase).toBe("review");
    expect(JSON.stringify(result.current.state)).not.toContain("secret-access");
    expect(JSON.stringify(result.current.state)).not.toContain(
      "secret-refresh",
    );
  });

  it("delegates JSON validation and keeps a rejected file available for retry", async () => {
    const gateway = createGateway();
    vi.mocked(gateway.preview).mockRejectedValue(
      new Error("Invalid sub2api import file"),
    );
    const file = createFile("{bad-json");
    const { result } = renderHook(() =>
      useProviderImportFlow({ gateway, existingProviderIds: [] }),
    );

    await act(async () => {
      await result.current.previewFile(file);
    });

    expect(gateway.preview).toHaveBeenCalledWith("{bad-json");
    expect(result.current.state).toMatchObject({
      phase: "upload",
      file,
      error: "Invalid sub2api import file",
    });
  });

  it("requires acknowledgement, commits once, and reports completion", async () => {
    const gateway = createGateway();
    const onCommitted = vi.fn();
    const { result } = renderHook(() =>
      useProviderImportFlow({
        gateway,
        existingProviderIds: [],
        onCommitted,
      }),
    );
    await act(async () => {
      await result.current.previewFile(createFile('{"accounts":[]}'));
    });
    expect(result.current.canCommit).toBe(false);

    act(() => result.current.setAcknowledgement(true));
    expect(result.current.canCommit).toBe(true);
    await act(async () => {
      await Promise.all([result.current.commit(), result.current.commit()]);
    });

    expect(gateway.commit).toHaveBeenCalledTimes(1);
    expect(gateway.commit).toHaveBeenCalledWith("import-1", {
      group_id: null,
      items: [
        {
          candidate_id: "candidate-1",
          action: "create",
          provider_id: "imported-account",
          name: "Imported Account",
          priority: 1,
          concurrency: 10,
        },
      ],
    });
    expect(onCommitted).toHaveBeenCalledWith(commitResult);
    await waitFor(() => expect(result.current.state.phase).toBe("result"));
  });

  it("discards a reviewed draft before returning to upload", async () => {
    const gateway = createGateway();
    const { result } = renderHook(() =>
      useProviderImportFlow({ gateway, existingProviderIds: [] }),
    );
    await act(async () => {
      await result.current.previewFile(createFile('{"accounts":[]}'));
    });

    await act(async () => {
      await result.current.abandonDraft();
    });

    expect(gateway.discard).toHaveBeenCalledWith("import-1");
    expect(result.current.state.phase).toBe("upload");
  });

  it("discards a server draft that finishes after the preview was abandoned", async () => {
    let resolvePreview!: (value: ProviderImportPreview) => void;
    const pendingPreview = new Promise<ProviderImportPreview>((resolve) => {
      resolvePreview = resolve;
    });
    const gateway = createGateway();
    gateway.preview = vi.fn().mockReturnValue(pendingPreview);
    const { result } = renderHook(() =>
      useProviderImportFlow({ gateway, existingProviderIds: [] }),
    );
    let previewRequest!: Promise<void>;

    act(() => {
      previewRequest = result.current.previewFile(
        createFile('{"accounts":[]}'),
      );
    });
    expect(result.current.state.phase).toBe("previewing");
    await act(async () => {
      await result.current.abandonDraft();
    });
    await act(async () => {
      resolvePreview(preview);
      await previewRequest;
    });

    expect(gateway.discard).toHaveBeenCalledWith("import-1");
    expect(result.current.state.phase).toBe("upload");
  });

  it.each([
    { status: 404, details: undefined, reason: "unavailable" as const },
    { status: 409, details: undefined, reason: "stale" as const },
    {
      status: 409,
      details: { kind: "provider_import_commit_mismatch" },
      reason: "committed_mismatch" as const,
    },
    { status: 410, details: undefined, reason: "expired" as const },
  ])(
    "requires a fresh preview for $reason after a $status commit response",
    async ({ status, details, reason }) => {
      const gateway = createGateway();
      gateway.commit = vi
        .fn()
        .mockRejectedValue(
          Object.assign(new Error("preview unavailable"), { status, details }),
        );
      const { result } = renderHook(() =>
        useProviderImportFlow({ gateway, existingProviderIds: [] }),
      );
      await act(async () => {
        await result.current.previewFile(createFile('{"accounts":[]}'));
      });
      act(() => result.current.setAcknowledgement(true));

      await act(async () => {
        await result.current.commit();
      });

      expect(result.current.state).toEqual({
        phase: "recovery",
        importId: "import-1",
        reason,
      });
      expect(gateway.discard).not.toHaveBeenCalled();

      await act(async () => {
        await result.current.abandonDraft();
      });
      expect(gateway.discard).toHaveBeenCalledWith("import-1");
      expect(result.current.state.phase).toBe("upload");
    },
  );

  it("keeps an ordinary commit failure reviewable and retryable", async () => {
    const gateway = createGateway();
    gateway.commit = vi
      .fn()
      .mockRejectedValueOnce(new Error("Network unavailable"))
      .mockResolvedValueOnce(commitResult);
    const { result } = renderHook(() =>
      useProviderImportFlow({ gateway, existingProviderIds: [] }),
    );
    await act(async () => {
      await result.current.previewFile(createFile('{"accounts":[]}'));
    });
    act(() => result.current.setAcknowledgement(true));

    await act(async () => {
      await result.current.commit();
    });
    expect(result.current.state).toMatchObject({
      phase: "review",
      error: "Network unavailable",
    });
    expect(result.current.canCommit).toBe(true);

    await act(async () => {
      await result.current.commit();
    });
    expect(gateway.commit).toHaveBeenCalledTimes(2);
    expect(result.current.state.phase).toBe("result");
  });

  it.each([
    {
      details: { kind: "provider_import_signing_keys_unavailable" },
      expected:
        "OpenAI signing keys are temporarily unavailable. Your review is unchanged; retry in a moment.",
    },
    {
      details: {
        kind: "provider_import_token_verification_failed",
        candidate_id: "candidate-1",
      },
      expected:
        'Could not verify OAuth tokens for "Imported Account". Skip this account or upload a fresh trusted export.',
    },
  ])(
    "keeps a $details.kind failure actionable in review",
    async ({ details, expected }) => {
      const gateway = createGateway();
      gateway.commit = vi
        .fn()
        .mockRejectedValue(
          Object.assign(new Error("Commit failed"), { details }),
        );
      const { result } = renderHook(() =>
        useProviderImportFlow({ gateway, existingProviderIds: [] }),
      );
      await act(async () => {
        await result.current.previewFile(createFile('{"accounts":[]}'));
      });
      act(() => result.current.setAcknowledgement(true));

      await act(async () => {
        await result.current.commit();
      });

      expect(result.current.state).toMatchObject({
        phase: "review",
        error: expected,
      });
      expect(result.current.canCommit).toBe(true);
    },
  );
});
