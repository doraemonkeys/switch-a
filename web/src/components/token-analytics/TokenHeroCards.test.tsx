import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type {
  TokenBucketDTO,
  TokenCoverageDTO,
  TokenDataQualityDTO,
  TokenSummaryDTO,
} from "../../api/types";
import { TokenHeroCards } from "./TokenHeroCards";

const mockSummary: TokenSummaryDTO = {
  total_tokens: "12451200",
  input_tokens: "8204110",
  output_tokens: "4247090",
  fresh_input_tokens: "4663710",
  cache_read_input_tokens: "3120400",
  cache_creation_input_tokens: "420000",
  unclassified_input_tokens: "0",
  standard_output_tokens: "3426690",
  reasoning_tokens: "820400",
  unclassified_output_tokens: "0",
  cache_hit_rate: 0.3803,
  reasoning_ratio: 0.1932,
};

const mockCoverage: TokenCoverageDTO = {
  total_requests: 1456,
  observed_requests: 1430,
  comparable_requests: 1430,
  without_usage_requests: 26,
  rate: 0.9821,
};

const mockDataQuality: TokenDataQualityDTO = {
  quality_rate: 1.0,
  partial_requests: 0,
  invalid_requests: 0,
  unknown_semantics_requests: 0,
};

const mockTimeseries: TokenBucketDTO[] = [
  {
    start: "2026-08-21T00:00:00Z",
    end: "2026-08-21T01:00:00Z",
    total_tokens: "1200000",
    input_tokens: "800000",
    output_tokens: "400000",
    fresh_input_tokens: "400000",
    cache_read_input_tokens: "350000",
    cache_creation_input_tokens: "50000",
    unclassified_input_tokens: "0",
    standard_output_tokens: "320000",
    reasoning_tokens: "80000",
    unclassified_output_tokens: "0",
    total_requests: 120,
    observed_requests: 118,
    comparable_requests: 118,
  },
];

describe("TokenHeroCards", () => {
  it("renders all 4 hero cards with formatted metrics", () => {
    render(
      <TokenHeroCards
        summary={mockSummary}
        coverage={mockCoverage}
        dataQuality={mockDataQuality}
        timeseries={mockTimeseries}
      />,
    );

    // Total tokens card
    expect(screen.getByText("Total Tokens")).toBeInTheDocument();
    expect(screen.getByText("12.45M")).toBeInTheDocument();
    expect(screen.getByText("12,451,200 total tokens")).toBeInTheDocument();
    expect(screen.getByText("1,430")).toBeInTheDocument();
    expect(screen.getByText("1.2M / bucket")).toBeInTheDocument();

    // Input tokens card
    expect(screen.getByText("Input Tokens")).toBeInTheDocument();
    expect(screen.getByText("8.2M")).toBeInTheDocument();
    expect(screen.getByText("8,204,110 tokens")).toBeInTheDocument();
    expect(screen.getByText("65.9% Total")).toBeInTheDocument();
    expect(screen.getByText(/Cache Read \(Hit\)/)).toBeInTheDocument();
    expect(screen.getByText(/3.12M \(38.0%\)/)).toBeInTheDocument();
    expect(screen.getByText(/Cache Creation/)).toBeInTheDocument();
    expect(screen.getByText(/420K \(5.1%\)/)).toBeInTheDocument();
    expect(screen.getByText(/Uncached Fresh/)).toBeInTheDocument();
    expect(screen.getByText(/4.66M \(56.8%\)/)).toBeInTheDocument();

    // Output tokens card
    expect(screen.getByText("Output Tokens")).toBeInTheDocument();
    expect(screen.getByText("4.25M")).toBeInTheDocument();
    expect(screen.getByText("4,247,090 tokens")).toBeInTheDocument();
    expect(screen.getByText("34.1% Total")).toBeInTheDocument();
    expect(screen.getByText(/Reasoning \(CoT\)/)).toBeInTheDocument();
    expect(screen.getByText(/820.4K \(19.3%\)/)).toBeInTheDocument();
    expect(screen.getByText(/Standard Output/)).toBeInTheDocument();
    expect(screen.getByText(/3.43M \(80.7%\)/)).toBeInTheDocument();

    // Efficiency card
    expect(screen.getByText("Efficiency & Quality")).toBeInTheDocument();
    expect(screen.getByText("38.0%")).toBeInTheDocument();
    expect(screen.getByText("19.3%")).toBeInTheDocument();
    expect(screen.getByText("8.7K tok")).toBeInTheDocument();
    expect(screen.getByText("1,430 / 1,456 (98.2%)")).toBeInTheDocument();
    expect(screen.getByText("1,430 / 1,430 (100.0%)")).toBeInTheDocument();
  });

  it("renders unclassified segments when non-zero", () => {
    const summaryWithUnclassified: TokenSummaryDTO = {
      ...mockSummary,
      unclassified_input_tokens: "100000",
      unclassified_output_tokens: "50000",
    };

    render(
      <TokenHeroCards
        summary={summaryWithUnclassified}
        coverage={mockCoverage}
        dataQuality={mockDataQuality}
        timeseries={mockTimeseries}
      />,
    );

    expect(screen.getByText(/Unclassified Input/)).toBeInTheDocument();
    expect(screen.getByText(/100K \(1.2%\)/)).toBeInTheDocument();
    expect(screen.getByText(/Unclassified Output/)).toBeInTheDocument();
    expect(screen.getByText(/50K \(1.2%\)/)).toBeInTheDocument();
  });
});
