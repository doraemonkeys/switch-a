import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { TokenModelRankDTO, TokenProviderRankDTO } from "../../api/types";
import { TokenTopBreakdown } from "./TokenTopBreakdown";

const mockProviders: TokenProviderRankDTO[] = [
  {
    provider_id: "prov-1",
    provider_name: "Anthropic Direct",
    total_tokens: "7420000",
    input_tokens: "5000000",
    output_tokens: "2420000",
    fresh_input_tokens: "2000000",
    cache_read_input_tokens: "2800000",
    cache_creation_input_tokens: "200000",
    unclassified_input_tokens: "0",
    standard_output_tokens: "2420000",
    reasoning_tokens: "0",
    unclassified_output_tokens: "0",
    request_count: 840,
    share: 0.596,
  },
  {
    provider_id: "prov-2",
    provider_name: "", // Tests fallback to provider_id
    total_tokens: "3810000",
    input_tokens: "2500000",
    output_tokens: "1310000",
    fresh_input_tokens: "1500000",
    cache_read_input_tokens: "800000",
    cache_creation_input_tokens: "200000",
    unclassified_input_tokens: "0",
    standard_output_tokens: "1000000",
    reasoning_tokens: "310000",
    unclassified_output_tokens: "0",
    request_count: 510,
    share: 0.306,
  },
];

const mockModels: TokenModelRankDTO[] = [
  {
    model: "claude-3-7-sonnet-20250219",
    total_tokens: "6120000",
    input_tokens: "4000000",
    output_tokens: "2120000",
    fresh_input_tokens: "1800000",
    cache_read_input_tokens: "2000000",
    cache_creation_input_tokens: "200000",
    unclassified_input_tokens: "0",
    standard_output_tokens: "2120000",
    reasoning_tokens: "0",
    unclassified_output_tokens: "0",
    request_count: 620,
    share: 0.492,
  },
];

describe("TokenTopBreakdown", () => {
  it("renders top providers and top models lists", () => {
    render(
      <TokenTopBreakdown byProvider={mockProviders} byModel={mockModels} />,
    );

    expect(screen.getByText("Top Providers")).toBeInTheDocument();
    expect(screen.getByText("Top Models")).toBeInTheDocument();

    // Provider 1
    expect(screen.getByText("Anthropic Direct")).toBeInTheDocument();
    expect(screen.getByText("7.42M")).toBeInTheDocument();
    expect(screen.getByText(/\(59\.6%\) • 840 reqs/)).toBeInTheDocument();

    // Provider 2 fallback to ID
    expect(screen.getByText("prov-2")).toBeInTheDocument();
    expect(screen.getByText("3.81M")).toBeInTheDocument();
    expect(screen.getByText(/\(30\.6%\) • 510 reqs/)).toBeInTheDocument();

    // Model 1
    expect(screen.getByText("claude-3-7-sonnet-20250219")).toBeInTheDocument();
    expect(screen.getByText("6.12M")).toBeInTheDocument();
    expect(screen.getByText(/\(49\.2%\) • 620 reqs/)).toBeInTheDocument();
  });

  it("renders empty state messages when lists are empty", () => {
    render(<TokenTopBreakdown byProvider={[]} byModel={[]} />);

    expect(
      screen.getByText("No provider usage recorded in this time window."),
    ).toBeInTheDocument();
    expect(
      screen.getByText("No model usage recorded in this time window."),
    ).toBeInTheDocument();
  });
});
