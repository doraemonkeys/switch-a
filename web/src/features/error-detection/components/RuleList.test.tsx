import { useState } from "react";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { parseAPICatalog } from "@/api/api-catalog";
import apiCatalogFixture from "../../../../../contracts/internal-error/v1/api-catalog.json";
import type { InternalErrorRule } from "../contracts";
import { RuleList } from "./RuleList";

const catalog = parseAPICatalog(apiCatalogFixture);

function makeRule(
  id: string,
  name: string,
  position: number,
  target: InternalErrorRule["target"] = { kind: "global" },
): InternalErrorRule {
  return {
    id,
    name,
    enabled: true,
    target,
    api_type: "codex",
    keywords: [`${name.toLowerCase()} error`],
    match_mode: "any",
    action: { type: "passthrough" },
    position,
    created_at: "2026-08-03T01:00:00Z",
    updated_at: "2026-08-03T01:00:00Z",
  };
}

const initialRules = [
  makeRule("11111111-1111-4111-8111-111111111111", "First", 0, {
    kind: "provider",
    provider_id: "deleted-provider",
  }),
  makeRule("22222222-2222-4222-8222-222222222222", "Second", 1),
  makeRule("33333333-3333-4333-8333-333333333333", "Third", 2),
];

function ReorderHarness({
  onReorder,
}: {
  onReorder: (ids: readonly string[]) => void;
}) {
  const [rules, setRules] = useState(initialRules);
  return (
    <RuleList
      rules={rules}
      ruleRevision="7"
      catalog={catalog}
      providers={[]}
      stats={{
        schema_version: 1,
        rule_set_revision: "7",
        stats: [
          {
            rule_id: initialRules[0].id,
            hit_count: "18446744073709551615",
            last_hit_at: null,
          },
        ],
      }}
      statsLoading={false}
      statsError={null}
      loading={false}
      error={null}
      busy={false}
      canCreate
      onCreate={vi.fn()}
      onEdit={vi.fn()}
      onDelete={vi.fn()}
      onToggle={vi.fn().mockResolvedValue(undefined)}
      onReorder={async (ids) => {
        onReorder(ids);
        setRules(
          ids.map((id, position) => ({
            ...rules.find((rule) => rule.id === id)!,
            position,
          })),
        );
      }}
    />
  );
}

describe("RuleList", () => {
  it("sends the full permutation and preserves keyboard focus after moving", async () => {
    const user = userEvent.setup();
    const onReorder = vi.fn();
    render(<ReorderHarness onReorder={onReorder} />);
    const moveButton = screen.getByRole("button", { name: "Move First down" });
    moveButton.focus();

    await user.keyboard("{Enter}");

    expect(onReorder).toHaveBeenCalledWith([
      initialRules[1].id,
      initialRules[0].id,
      initialRules[2].id,
    ]);
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "Move First down" }),
      ).toHaveFocus(),
    );
    expect(
      screen.getByText(/Deleted provider · deleted-provider/),
    ).toBeVisible();
    expect(screen.getByText("18,446,744,073,709,551,615")).toBeVisible();
  });

  it("renders explicit loading and empty states", () => {
    const props = {
      rules: [],
      ruleRevision: null,
      catalog,
      providers: [],
      stats: null,
      statsLoading: true,
      statsError: null,
      error: null,
      busy: false,
      canCreate: false,
      onCreate: vi.fn(),
      onEdit: vi.fn(),
      onDelete: vi.fn(),
      onToggle: vi.fn().mockResolvedValue(undefined),
      onReorder: vi.fn().mockResolvedValue(undefined),
    } as const;
    const { rerender } = render(<RuleList {...props} loading />);
    expect(screen.getByText("Loading detection rules…")).toBeVisible();

    rerender(<RuleList {...props} loading={false} />);
    expect(screen.getByText("No detection rules configured")).toBeVisible();
    expect(screen.getByRole("button", { name: "Add rule" })).toBeDisabled();
  });

  it("announces refresh failures and locks row actions during a mutation", () => {
    render(
      <RuleList
        rules={initialRules}
        ruleRevision="7"
        catalog={catalog}
        providers={[]}
        stats={null}
        statsLoading={false}
        statsError={new Error("statistics endpoint unavailable")}
        loading={false}
        error={new Error("rules endpoint unavailable")}
        busy
        canCreate
        onCreate={vi.fn()}
        onEdit={vi.fn()}
        onDelete={vi.fn()}
        onToggle={vi.fn().mockResolvedValue(undefined)}
        onReorder={vi.fn().mockResolvedValue(undefined)}
      />,
    );

    expect(
      screen.getByRole("region", { name: "Detection rules" }),
    ).toHaveAttribute("aria-busy", "true");
    expect(screen.getByRole("alert")).toHaveTextContent(
      "rules endpoint unavailable",
    );
    expect(screen.getByRole("status")).toHaveTextContent(
      "statistics endpoint unavailable",
    );
    expect(screen.getByRole("button", { name: "Add rule" })).toBeDisabled();
    expect(
      screen.getByRole("button", { name: "Move Second down" }),
    ).toBeDisabled();
    expect(
      screen.getByRole("button", { name: "Disable First" }),
    ).toBeDisabled();
    expect(screen.getByRole("button", { name: "Edit First" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Delete First" })).toBeDisabled();
  });
});
