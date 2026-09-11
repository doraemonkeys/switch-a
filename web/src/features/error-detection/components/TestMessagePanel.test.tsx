import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { parseAPICatalog } from "@/api/api-catalog";
import {
  parseTestMessageResponse,
  parseInternalErrorRuleListResponse,
} from "@/api/error-detection-decoders";
import type { Provider } from "@/api/types";
import apiCatalogFixture from "../../../../../contracts/internal-error/v1/api-catalog.json";
import ruleListFixture from "../../../../../contracts/internal-error/v1/rule-list.json";
import testMessageFixture from "../../../../../contracts/internal-error/v1/test-message.json";
import { TestMessagePanel } from "./TestMessagePanel";

const ruleSet = {
  ...parseInternalErrorRuleListResponse(ruleListFixture),
  rule_set_revision: "10",
};
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
        ruleSet={ruleSet}
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
        ruleSet={ruleSet}
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
  it("marks a result stale when its input or saved rule revision changes", async () => {
    const user = userEvent.setup();
    const response = parseTestMessageResponse(
      testMessageFixture.complete.response,
    );
    const props = {
      catalog,
      ruleSet,
      providers: [],
      disabled: false,
      onTest: vi.fn().mockResolvedValue(response),
    };
    const { rerender } = render(<TestMessagePanel {...props} />);
    await user.click(screen.getByRole("button", { name: "Analyze message" }));
    expect(
      await screen.findByText("Winning rule · Codex capacity"),
    ).toBeVisible();
    await user.type(screen.getByLabelText("Response body"), "changed response");
    expect(screen.getByRole("status")).toHaveTextContent("Input has changed.");
    expect(props.onTest).toHaveBeenCalledTimes(1);
    await user.click(screen.getByRole("button", { name: "Analyze message" }));
    expect(
      await screen.findByText("Winning rule · Codex capacity"),
    ).toBeVisible();
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
    rerender(
      <TestMessagePanel
        {...props}
        ruleSet={{ ...ruleSet, rule_set_revision: "11" }}
      />,
    );
    expect(screen.getByRole("status")).toHaveTextContent(
      "Saved rules have changed.",
    );
    expect(
      screen.queryByText("Winning rule · Codex capacity"),
    ).not.toBeInTheDocument();
  });

  it("keeps transport metadata and Base64 bytes exact when using advanced settings", async () => {
    const user = userEvent.setup();
    const onTest = vi
      .fn()
      .mockResolvedValue(
        parseTestMessageResponse(testMessageFixture.fail_open.response),
      );
    render(
      <TestMessagePanel
        catalog={catalog}
        ruleSet={ruleSet}
        providers={[]}
        disabled={false}
        onTest={onTest}
      />,
    );
    await user.click(screen.getByText("Transport settings"));
    const encoding = screen.getByLabelText("Content-Encoding");
    await user.clear(encoding);
    await user.type(encoding, "gzip");
    await user.click(screen.getByRole("radio", { name: "Base64 bytes" }));
    const bytes = "H4sIAAAAAAAAA6uuBQBDv6ajAgAAAA==";
    await user.type(screen.getByLabelText("Response body"), bytes);
    expect(
      screen.getByRole("button", { name: "Insert example" }),
    ).toBeDisabled();
    await user.click(screen.getByRole("button", { name: "Analyze message" }));
    expect(onTest).toHaveBeenCalledWith(
      expect.objectContaining({
        content_type: "application/json",
        content_encoding: "gzip",
        body: { encoding: "base64", value: bytes },
      }),
    );
  });
});
