import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { cleanup, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { RequestEvidenceViewer } from "./RequestEvidenceViewer";

interface EvidenceFixture {
  cases: Array<{
    name: string;
    evidence: Record<string, unknown> & {
      semantic_error: {
        decision: { value: string };
        alternate: Record<string, unknown>;
      };
    };
  }>;
}

function loadFixture(): EvidenceFixture {
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

afterEach(cleanup);

describe("RequestEvidenceViewer", () => {
  it("renders every shared-fixture decision as an explicit semantic axis", () => {
    for (const fixtureCase of loadFixture().cases) {
      render(
        <RequestEvidenceViewer
          evidenceJson={JSON.stringify(fixtureCase.evidence)}
        />,
      );

      const decision = screen.getByRole("region", {
        name: "Semantic decision",
      });
      expect(
        within(decision).getByText(
          `(${fixtureCase.evidence.semantic_error.decision.value})`,
        ),
        fixtureCase.name,
      ).toBeInTheDocument();
      cleanup();
    }
  });

  it("renders all semantic dimensions and bounded worst-case collections", () => {
    const worstCase = loadFixture().cases.find(
      (fixtureCase) => fixtureCase.name === "worst_case_bounds",
    );
    if (!worstCase) {
      throw new Error("missing worst_case_bounds fixture");
    }
    render(
      <RequestEvidenceViewer
        evidenceJson={JSON.stringify(worstCase.evidence)}
      />,
    );

    for (const sectionName of [
      "Semantic decision",
      "Winning rule and matches",
      "Response boundary",
      "Retry budget",
      "Alternate provider",
      "Health attribution",
      "Attempt identity",
      "Transport",
    ]) {
      expect(
        screen.getByRole("region", { name: sectionName }),
      ).toBeInTheDocument();
    }
    expect(screen.getByText("256 matching rules")).toBeInTheDocument();
    expect(screen.getByText("16 normalized rule keywords")).toBeInTheDocument();
  });

  it("shows switch metadata only as explicit alternate-provider evidence", () => {
    const switchCase = loadFixture().cases.find(
      (fixtureCase) => fixtureCase.name === "switch_provider",
    );
    if (!switchCase) {
      throw new Error("missing switch_provider fixture");
    }
    render(
      <RequestEvidenceViewer
        evidenceJson={JSON.stringify(switchCase.evidence)}
      />,
    );

    const alternate = screen.getByRole("region", {
      name: "Alternate provider",
    });
    expect(within(alternate).getByText("Switch reason")).toBeInTheDocument();
    expect(
      within(alternate).getByText("(internal_error_rule_exhausted)"),
    ).toBeInTheDocument();
  });

  it("preserves malformed and invalid evidence behind an accessible fallback", () => {
    const { rerender } = render(<RequestEvidenceViewer evidenceJson="{" />);
    expect(
      screen.getByRole("region", { name: "Structured evidence unavailable" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("group", { name: "View raw evidence" }),
    ).toBeInTheDocument();

    const invalid = structuredClone(loadFixture().cases[0].evidence);
    delete (invalid.semantic_error as Record<string, unknown>).health;
    rerender(<RequestEvidenceViewer evidenceJson={JSON.stringify(invalid)} />);
    expect(screen.getByText(/health is required/)).toBeInTheDocument();
  });

  it("treats unknown semantic switch reason as unavailable instead of inferring", () => {
    const fixtureCase = structuredClone(loadFixture().cases[0].evidence);
    (
      (fixtureCase.semantic_error as Record<string, unknown>)
        .alternate as Record<string, unknown>
    ).switch_reason = "freeform_routing_note";

    render(
      <RequestEvidenceViewer evidenceJson={JSON.stringify(fixtureCase)} />,
    );
    expect(
      screen.getByRole("region", { name: "Structured evidence unavailable" }),
    ).toBeInTheDocument();
    expect(screen.queryByText(/switched provider/i)).not.toBeInTheDocument();
  });

  it("keeps historical evidence on the legacy renderer", () => {
    render(
      <RequestEvidenceViewer
        evidenceJson={JSON.stringify({
          gateway: { terminal_status_code: 502 },
          transport: {
            source: "upstream",
            message_snippet: "connection reset",
            is_timeout: false,
          },
        })}
      />,
    );

    expect(screen.getByRole("region", { name: "Gateway" })).toBeInTheDocument();
    expect(
      screen.getByRole("region", { name: "Transport" }),
    ).toBeInTheDocument();
    expect(screen.getByText("connection reset")).toBeInTheDocument();
  });
});
