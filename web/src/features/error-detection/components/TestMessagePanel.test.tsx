import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { parseAPICatalog } from "@/api/api-catalog";
import { parseTestMessageResponse } from "@/api/error-detection-decoders";
import type { Provider } from "@/api/types";
import apiCatalogFixture from "../../../../../contracts/internal-error/v1/api-catalog.json";
import testMessageFixture from "../../../../../contracts/internal-error/v1/test-message.json";
import { TestMessagePanel } from "./TestMessagePanel";

const catalog = parseAPICatalog(apiCatalogFixture);
const provider = {
  id: "provider-codex",
  name: "Codex primary",
} as Provider;

describe("TestMessagePanel", () => {
  it("sends exactly one backend request and renders extraction, all matches, and winner", async () => {
    const user = userEvent.setup();
    const response = parseTestMessageResponse(
      testMessageFixture.complete.response,
    );
    const onTest = vi.fn().mockResolvedValue(response);
    render(
      <TestMessagePanel
        catalog={catalog}
        providers={[provider]}
        disabled={false}
        onTest={onTest}
      />,
    );

    await user.selectOptions(screen.getByLabelText("API type"), "codex");
    await user.selectOptions(screen.getByLabelText("Rule scope"), provider.id);
    const contentType = screen.getByLabelText("Content-Type");
    await user.clear(contentType);
    await user.type(contentType, "text/event-stream; charset=utf-8");
    fireEvent.change(screen.getByLabelText("Response body"), {
      target: { value: "event: error\n\ndata: {}\n\n" },
    });
    await user.click(screen.getByRole("button", { name: "Analyze message" }));

    expect(onTest).toHaveBeenCalledTimes(1);
    expect(onTest).toHaveBeenCalledWith({
      api_type: "codex",
      provider_id: provider.id,
      content_type: "text/event-stream; charset=utf-8",
      content_encoding: "identity",
      body: { encoding: "utf8", value: "event: error\n\ndata: {}\n\n" },
    });
    expect(
      await screen.findByRole("region", { name: "Test Message result" }),
    ).toHaveTextContent("openai.responses.sse.v1");
    expect(screen.getByText("server_is_overloaded")).toBeVisible();
    expect(screen.getByText(/Winning rule/)).toBeVisible();
    expect(
      screen.getAllByText("11111111-1111-4111-8111-111111111111").length,
    ).toBeGreaterThanOrEqual(2);
  });

  it("renders a fail-open reason without inventing browser-side matches", async () => {
    const user = userEvent.setup();
    const response = parseTestMessageResponse(
      testMessageFixture.fail_open.response,
    );
    render(
      <TestMessagePanel
        catalog={catalog}
        providers={[]}
        disabled={false}
        onTest={vi.fn().mockResolvedValue(response)}
      />,
    );

    await user.click(screen.getByRole("button", { name: "Analyze message" }));

    expect(
      await screen.findByText("unsupported_content_encoding"),
    ).toBeVisible();
    expect(screen.getByText("No winning rule.")).toBeVisible();
    expect(
      screen.getByText("No structured error objects were extracted."),
    ).toBeVisible();
  });
});
