import { describe, expect, it, vi } from "vitest";
import { downloadJsonFile, type FileDownloadPort } from "./jsonDownload";

function readBlob(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.addEventListener("load", () => resolve(String(reader.result)));
    reader.addEventListener("error", () => reject(reader.error));
    reader.readAsText(blob);
  });
}

describe("downloadJsonFile", () => {
  it("serializes readable JSON and delegates the browser side effect", async () => {
    const save = vi.fn<FileDownloadPort["save"]>();

    downloadJsonFile(
      "auth.json",
      {
        auth_mode: "chatgpt",
        OPENAI_API_KEY: null,
        tokens: { access_token: "token" },
      },
      { save },
    );

    expect(save).toHaveBeenCalledTimes(1);
    const [file, filename] = save.mock.calls[0];
    expect(filename).toBe("auth.json");
    expect(file.type).toBe("application/json");
    await expect(readBlob(file)).resolves.toBe(
      '{\n  "auth_mode": "chatgpt",\n  "OPENAI_API_KEY": null,\n  "tokens": {\n    "access_token": "token"\n  }\n}',
    );
  });
});
