import { useState } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { parseAPICatalog } from "@/api/api-catalog";
import type { Provider } from "@/api/types";
import apiCatalogFixture from "../../../../../contracts/internal-error/v1/api-catalog.json";
import {
  createEmptyRuleDraft,
  type RuleDraft,
  type RuleDraftErrors,
} from "../model";
import { RuleEditor } from "./RuleEditor";

const catalog = parseAPICatalog(apiCatalogFixture);
const providers = [{ id: "provider-codex", name: "Codex primary" } as Provider];

function EditorHarness({
  initialDraft = createEmptyRuleDraft(),
  errors = {},
  busy = false,
}: {
  initialDraft?: RuleDraft;
  errors?: RuleDraftErrors;
  busy?: boolean;
}) {
  const [baseline] = useState(initialDraft);
  const [draft, setDraft] = useState<RuleDraft>(initialDraft);
  return (
    <RuleEditor
      mode="create"
      draft={draft}
      baseline={baseline}
      catalog={catalog}
      providers={providers}
      providersLoading={false}
      providersError={null}
      errors={errors}
      submitError={null}
      busy={busy}
      globalMaxAttempts={3}
      configUnavailable={false}
      onChange={setDraft}
      onSubmit={vi.fn()}
      onCancel={vi.fn()}
    />
  );
}

describe("RuleEditor", () => {
  it("requires explicit confirmation before a preset replaces a dirty draft", async () => {
    const user = userEvent.setup();
    render(<EditorHarness />);

    await user.type(screen.getByLabelText("Rule name"), "My local draft");
    await user.click(screen.getByRole("button", { name: /Codex capacity/i }));

    expect(
      screen.getByRole("alertdialog", {
        name: "Replace unsaved draft fields?",
      }),
    ).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Keep editing" }));
    expect(screen.getByLabelText("API type")).toHaveValue("");

    await user.click(screen.getByRole("button", { name: /Codex capacity/i }));
    await user.click(screen.getByRole("button", { name: "Replace draft" }));

    expect(screen.getByLabelText("Rule name")).toHaveValue("My local draft");
    expect(screen.getByLabelText("API type")).toHaveValue("codex");
    expect(screen.getByLabelText(/Keywords/)).toHaveValue(
      "server_is_overloaded\nour servers are currently overloaded at capacity",
    );
    expect(
      screen.getByRole("region", { name: "Current provider wait estimate" }),
    ).toHaveTextContent("Effective same-provider retries1");
    expect(
      screen.getByText(/One finite global attempt is reserved/),
    ).toBeVisible();
    const visibleResponse = screen.getByLabelText(
      /When an error is already streaming/,
    );
    expect(visibleResponse).toHaveValue("disconnect_client");
    await user.selectOptions(visibleResponse, "commit_current");
    expect(visibleResponse).toHaveValue("commit_current");
  });

  it("labels custom API types unsupported without disabling local editing", async () => {
    const user = userEvent.setup();
    render(<EditorHarness />);

    await user.type(screen.getByLabelText("API type"), "custom:private");
    expect(
      screen.getByText(
        /Custom API types do not support structured error detection/,
      ),
    ).toBeVisible();
    expect(screen.getByRole("checkbox", { name: /Enabled/ })).toBeEnabled();
  });

  it("associates validation errors and disables every editor control while busy", () => {
    render(
      <EditorHarness
        initialDraft={{
          ...createEmptyRuleDraft(),
          target: { kind: "provider", provider_id: providers[0].id },
        }}
        errors={{ name: "Enter a rule name.", target: "Choose a provider." }}
        busy
      />,
    );

    const name = screen.getByLabelText("Rule name");
    expect(name).toBeDisabled();
    expect(name).toHaveAttribute("aria-invalid", "true");
    expect(name).toHaveAccessibleDescription("Enter a rule name.");
    expect(screen.getByLabelText("Rule scope")).toBeDisabled();
    expect(screen.getByLabelText("Provider")).toBeDisabled();
    expect(screen.getByLabelText(/Keywords/)).toBeDisabled();
    expect(screen.getByRole("checkbox", { name: /Enabled/ })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Saving…" })).toBeDisabled();
  });
});
