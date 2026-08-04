import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { RequestAttempt } from "@/api/types";
import { RequestAttemptTimeline } from "./components/RequestAttemptTimeline";
import { RequestEvidenceViewer } from "./components/RequestEvidenceViewer";

interface EvidenceFixtureCase {
  readonly name: string;
  readonly evidence: Record<string, unknown> & {
    readonly semantic_error: {
      readonly decision: { readonly value: string };
    };
  };
}

interface EvidenceFixture {
  readonly cases: readonly EvidenceFixtureCase[];
}

function loadEvidenceFixture(): EvidenceFixture {
  return JSON.parse(
    readFileSync(
      resolve(
        process.cwd(),
        "../contracts/internal-error/v1/attempt-evidence-v2.json",
      ),
      "utf8",
    ),
  ) as EvidenceFixture;
}

function fixtureCase(name: string): EvidenceFixtureCase {
  const found = loadEvidenceFixture().cases.find(
    (candidate) => candidate.name === name,
  );
  if (!found) throw new Error(`missing evidence fixture case: ${name}`);
  return found;
}

function attemptWithEvidence(
  evidence: unknown,
  switchReason?: RequestAttempt["switch_reason"],
): RequestAttempt {
  return {
    id: 71,
    request_id: "request-v5c",
    provider_id: "provider-codex",
    semantics_version: "normalized_v1",
    attempt: 1,
    provider_attempt: 1,
    provider_switch_count: 0,
    status_code: 101,
    error: "provider semantic error",
    phase: "post_upgrade_pre_visible",
    outcome: "upstream_semantic_error",
    result_visible_to_client: false,
    attempt_evidence_json:
      typeof evidence === "string" ? evidence : JSON.stringify(evidence),
    latency_ms: 37,
    switch_reason: switchReason,
    created_at: "2026-08-04T00:00:00Z",
  };
}

const SEMANTIC_SECTION_NAMES = [
  "Semantic decision",
  "Winning rule and matches",
  "Response boundary",
  "Retry budget",
  "Alternate provider",
  "Health attribution",
  "Attempt identity",
] as const;

describe("V5C request evidence acceptance", () => {
  it("renders every shared decision fixture through all seven semantic axes", () => {
    for (const fixture of loadEvidenceFixture().cases) {
      const view = render(
        <RequestEvidenceViewer
          evidenceJson={JSON.stringify(fixture.evidence)}
        />,
      );

      for (const sectionName of SEMANTIC_SECTION_NAMES) {
        expect(
          screen.getByRole("region", { name: sectionName }),
          `${fixture.name}: ${sectionName}`,
        ).toBeInTheDocument();
      }
      expect(
        within(
          screen.getByRole("region", { name: "Semantic decision" }),
        ).getByText(`(${fixture.evidence.semantic_error.decision.value})`),
        fixture.name,
      ).toBeVisible();

      if (fixture.name === "worst_case_bounds") {
        expect(screen.getByText("256 matching rules")).toBeVisible();
        expect(screen.getByText("16 normalized rule keywords")).toBeVisible();
      }
      view.unmount();
    }
  });

  it("keeps timeline semantics independent from arbitrary or absent routing switch reasons", () => {
    const evidence = fixtureCase("switch_provider").evidence;
    const arbitraryReason = "future_reason_owned_by_routing";
    const { rerender } = render(
      <RequestAttemptTimeline
        attempts={[attemptWithEvidence(evidence, arbitraryReason)]}
        isWebSocket
      />,
    );

    expect(
      screen.getByRole("list", { name: "Request attempts" }),
    ).toBeInTheDocument();
    const article = screen.getByRole("article", {
      name: "Provider attempt 1",
    });
    for (const sectionName of SEMANTIC_SECTION_NAMES) {
      expect(
        within(article).getByRole("region", { name: sectionName }),
      ).toBeInTheDocument();
    }
    expect(
      within(
        within(article).getByRole("region", {
          name: "Alternate provider",
        }),
      ).getByText("(internal_error_rule_exhausted)"),
    ).toBeVisible();
    expect(screen.queryByText(arbitraryReason)).not.toBeInTheDocument();

    const originalClassName = article.className;
    const originalSemanticText = SEMANTIC_SECTION_NAMES.map(
      (sectionName) =>
        within(article).getByRole("region", { name: sectionName }).textContent,
    );
    rerender(
      <RequestAttemptTimeline
        attempts={[attemptWithEvidence(evidence, undefined)]}
        isWebSocket
      />,
    );

    const rerenderedArticle = screen.getByRole("article", {
      name: "Provider attempt 1",
    });
    expect(rerenderedArticle.className).toBe(originalClassName);
    expect(
      SEMANTIC_SECTION_NAMES.map(
        (sectionName) =>
          within(rerenderedArticle).getByRole("region", {
            name: sectionName,
          }).textContent,
      ),
    ).toEqual(originalSemanticText);
  });

  it("preserves malformed evidence behind an accessible fallback without breaking the attempt", () => {
    render(
      <RequestAttemptTimeline
        attempts={[attemptWithEvidence("{", "unrelated_routing_note")]}
        isWebSocket
      />,
    );

    expect(
      screen.getByRole("article", { name: "Provider attempt 1" }),
    ).toBeInTheDocument();
    const fallback = screen.getByRole("region", {
      name: "Structured evidence unavailable",
    });
    expect(fallback).toHaveTextContent(
      "The evidence was preserved but could not be decoded safely",
    );
    expect(
      within(fallback).getByRole("group", { name: "View raw evidence" }),
    ).toBeInTheDocument();
    expect(
      screen.queryByText("unrelated_routing_note"),
    ).not.toBeInTheDocument();
  });
});
