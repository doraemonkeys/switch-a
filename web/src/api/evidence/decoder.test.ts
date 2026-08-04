import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { decodeRequestEvidence, parseRequestEvidenceJson } from "./decoder";

interface EvidenceFixture {
  schema_version: number;
  cases: Array<{ name: string; evidence: Record<string, unknown> }>;
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

function cloneCase(name = "retry_same"): Record<string, unknown> {
  const fixtureCase = loadFixture().cases.find((item) => item.name === name);
  if (!fixtureCase) {
    throw new Error(`missing fixture case ${name}`);
  }
  return structuredClone(fixtureCase.evidence);
}

function semanticRecord(
  evidence: Record<string, unknown>,
): Record<string, unknown> {
  return evidence.semantic_error as Record<string, unknown>;
}

describe("request evidence decoder", () => {
  it("strictly decodes every shared semantic decision fixture", () => {
    const fixture = loadFixture();
    expect(fixture.schema_version).toBe(1);

    const decisions = fixture.cases.map(({ name, evidence }) => {
      const result = decodeRequestEvidence(evidence);
      expect(result, name).toMatchObject({ state: "available" });
      if (result.state !== "available" || result.evidence.v !== 2) {
        throw new Error(`${name} did not decode as v2 evidence`);
      }
      expect(
        Object.isFrozen(result.evidence.semantic_error?.rule.matching_rule_ids),
      ).toBe(true);
      return result.evidence.semantic_error?.decision.value;
    });

    expect(new Set(decisions)).toEqual(
      new Set([
        "passthrough",
        "observe_only",
        "commit_current",
        "retry_same",
        "switch_provider",
      ]),
    );
  });

  it("tolerates unknown envelope siblings without weakening semantic validation", () => {
    const evidence = cloneCase();
    evidence.future_observer = { value: "retained by its owner" };

    expect(decodeRequestEvidence(evidence).state).toBe("available");

    const semantic = semanticRecord(evidence);
    semantic.future_axis = true;
    expect(decodeRequestEvidence(evidence)).toMatchObject({
      state: "unavailable",
      reason: "invalid_schema",
    });
  });

  it.each([
    [
      "missing required axis",
      (semantic: Record<string, unknown>) => delete semantic.health,
    ],
    [
      "unknown boundary enum",
      (semantic: Record<string, unknown>) => {
        (semantic.response as Record<string, unknown>).boundary_reason =
          "future_reason";
      },
    ],
    [
      "noncanonical decimal",
      (semantic: Record<string, unknown>) => {
        (semantic.identity as Record<string, unknown>).logical_attempt = "01";
      },
    ],
    [
      "out-of-order matched fields",
      (semantic: Record<string, unknown>) => {
        (semantic.rule as Record<string, unknown>).matched_fields = [
          "message",
          "code",
        ];
      },
    ],
    [
      "keyword/index mismatch",
      (semantic: Record<string, unknown>) => {
        (semantic.rule as Record<string, unknown>).matched_keyword_indexes = [
          1, 0,
        ];
      },
    ],
    [
      "retry action drift",
      (semantic: Record<string, unknown>) => {
        (semantic.retry as Record<string, unknown>).action = "passthrough";
      },
    ],
  ])("returns a safe unavailable state for %s", (_name, mutate) => {
    const evidence = cloneCase();
    mutate(semanticRecord(evidence));

    expect(decodeRequestEvidence(evidence)).toMatchObject({
      state: "unavailable",
      reason: "invalid_schema",
    });
  });

  it("classifies absent, malformed, and unknown-version input", () => {
    expect(parseRequestEvidenceJson("  ")).toEqual({ state: "absent" });
    expect(parseRequestEvidenceJson("{")).toEqual({
      state: "unavailable",
      reason: "malformed_json",
      detail: "request evidence is not valid JSON",
    });
    expect(decodeRequestEvidence({ v: 3 })).toMatchObject({
      state: "unavailable",
      reason: "unsupported_version",
    });
    expect(decodeRequestEvidence([])).toMatchObject({
      state: "unavailable",
      reason: "invalid_schema",
    });
  });

  it("keeps validated historical v1 evidence renderable", () => {
    const result = parseRequestEvidenceJson(
      JSON.stringify({
        gateway: { terminal_status_code: 502, future_field: "ignored" },
        transport: {
          source: "upstream",
          message_snippet: "connection reset",
          is_timeout: false,
        },
      }),
    );

    expect(result).toMatchObject({
      state: "available",
      evidence: {
        gateway: { terminal_status_code: 502 },
        transport: {
          source: "upstream",
          message_snippet: "connection reset",
          is_timeout: false,
        },
      },
    });
  });

  it("does not hardcode the independently owned protocol catalog", () => {
    const evidence = cloneCase();
    (semanticRecord(evidence).response as Record<string, unknown>).protocol_id =
      "vendor.future-stream.v7";

    const result = decodeRequestEvidence(evidence);
    expect(result).toMatchObject({ state: "available" });
    if (result.state === "available" && result.evidence.v === 2) {
      expect(result.evidence.semantic_error?.response.protocol_id).toBe(
        "vendor.future-stream.v7",
      );
    }
  });
});
