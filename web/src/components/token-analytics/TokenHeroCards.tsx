import { Activity, ArrowDownLeft, ArrowUpRight, Layers } from "lucide-react";
import type {
  TokenBucketDTO,
  TokenCoverageDTO,
  TokenDataQualityDTO,
  TokenSummaryDTO,
} from "../../api/types";
import {
  calculateTokenPercent,
  formatTokenCompact,
  formatTokenLocale,
  parseTokenBigInt,
  TOKEN_SEMANTICS,
} from "./token-format";
import { TokenMicroStackedBar } from "./TokenMicroStackedBar";

interface TotalTokensCardProps {
  summary: TokenSummaryDTO;
  coverage: TokenCoverageDTO;
  peakTokens: bigint;
  inputSharePercent: number;
  outputSharePercent: number;
}

function TotalTokensCard({
  summary,
  coverage,
  peakTokens,
  inputSharePercent,
  outputSharePercent,
}: TotalTokensCardProps) {
  return (
    <section
      className="bg-white dark:bg-slate-900 border border-slate-200/80 dark:border-slate-800 rounded-2xl p-5 shadow-xs hover:shadow-md transition-all flex flex-col justify-between space-y-4"
      aria-labelledby="card-total-tokens-title"
    >
      <div>
        <div className="flex items-center justify-between">
          <span
            id="card-total-tokens-title"
            className="text-xs font-semibold uppercase tracking-wider text-slate-500 dark:text-slate-400"
          >
            Total Tokens
          </span>
          <div
            className={`p-2 rounded-xl ${TOKEN_SEMANTICS.total.badgeBgClass}`}
          >
            <Layers className="w-4 h-4" aria-hidden="true" />
          </div>
        </div>

        <div className="mt-2">
          <div className="font-mono tracking-tight font-bold text-2xl text-slate-900 dark:text-slate-100">
            {formatTokenCompact(summary.total_tokens)}
          </div>
          <p className="text-xs text-slate-500 dark:text-slate-400 font-mono mt-0.5">
            {formatTokenLocale(summary.total_tokens)} total tokens
          </p>
        </div>
      </div>

      <div className="space-y-2">
        <TokenMicroStackedBar
          segments={[
            {
              key: "input",
              label: "Input",
              value: summary.input_tokens,
              bgClass: TOKEN_SEMANTICS.fresh.bgClass,
            },
            {
              key: "output",
              label: "Output",
              value: summary.output_tokens,
              bgClass: TOKEN_SEMANTICS.standardOutput.bgClass,
            },
          ]}
          totalValue={summary.total_tokens}
        />
        <div className="flex items-center justify-between text-[11px] font-mono text-slate-500 dark:text-slate-400">
          <span>
            {formatTokenCompact(summary.input_tokens)} In (
            {inputSharePercent.toFixed(1)}%)
          </span>
          <span>
            {formatTokenCompact(summary.output_tokens)} Out (
            {outputSharePercent.toFixed(1)}%)
          </span>
        </div>
      </div>

      <div className="border-t border-slate-100 dark:border-slate-800/80 pt-3 flex flex-col gap-1 text-xs text-slate-600 dark:text-slate-400">
        <div className="flex items-center justify-between">
          <span>Observed Reqs</span>
          <span className="font-mono font-medium text-slate-900 dark:text-slate-200">
            {coverage.observed_requests.toLocaleString()}
          </span>
        </div>
        <div className="flex items-center justify-between">
          <span>Peak Volume</span>
          <span className="font-mono font-medium text-slate-900 dark:text-slate-200">
            {formatTokenCompact(peakTokens)} / bucket
          </span>
        </div>
      </div>
    </section>
  );
}

interface InputTokensCardProps {
  summary: TokenSummaryDTO;
  inputSharePercent: number;
}

function InputTokensCard({ summary, inputSharePercent }: InputTokensCardProps) {
  const unclassifiedInputBigInt = parseTokenBigInt(
    summary.unclassified_input_tokens,
  );

  return (
    <section
      className="bg-white dark:bg-slate-900 border border-slate-200/80 dark:border-slate-800 rounded-2xl p-5 shadow-xs hover:shadow-md transition-all flex flex-col justify-between space-y-4"
      aria-labelledby="card-input-tokens-title"
    >
      <div>
        <div className="flex items-center justify-between">
          <span
            id="card-input-tokens-title"
            className="text-xs font-semibold uppercase tracking-wider text-slate-500 dark:text-slate-400"
          >
            Input Tokens
          </span>
          <div className="flex items-center gap-1.5">
            <span className="px-2 py-0.5 rounded-full text-xs font-semibold bg-sky-50 dark:bg-sky-950/40 text-sky-700 dark:text-sky-300">
              {inputSharePercent.toFixed(1)}% Total
            </span>
            <div
              className={`p-2 rounded-xl ${TOKEN_SEMANTICS.fresh.badgeBgClass}`}
            >
              <ArrowDownLeft className="w-4 h-4" aria-hidden="true" />
            </div>
          </div>
        </div>

        <div className="mt-2">
          <div className="font-mono tracking-tight font-bold text-2xl text-slate-900 dark:text-slate-100">
            {formatTokenCompact(summary.input_tokens)}
          </div>
          <p className="text-xs text-slate-500 dark:text-slate-400 font-mono mt-0.5">
            {formatTokenLocale(summary.input_tokens)} tokens
          </p>
        </div>
      </div>

      <div className="space-y-2">
        <TokenMicroStackedBar
          segments={[
            {
              key: "cache_read",
              label: "Cache Read",
              value: summary.cache_read_input_tokens,
              bgClass: TOKEN_SEMANTICS.cacheRead.bgClass,
            },
            {
              key: "cache_creation",
              label: "Cache Creation",
              value: summary.cache_creation_input_tokens,
              bgClass: TOKEN_SEMANTICS.cacheCreation.bgClass,
            },
            {
              key: "fresh",
              label: "Fresh Input",
              value: summary.fresh_input_tokens,
              bgClass: TOKEN_SEMANTICS.fresh.bgClass,
            },
            {
              key: "unclassified_input",
              label: "Unclassified Input",
              value: summary.unclassified_input_tokens,
              bgClass: TOKEN_SEMANTICS.unclassifiedInput.bgClass,
            },
          ]}
          totalValue={summary.input_tokens}
        />
      </div>

      <div className="border-t border-slate-100 dark:border-slate-800/80 pt-3 flex flex-col gap-1 text-xs text-slate-600 dark:text-slate-400">
        <div className="flex items-center justify-between">
          <span className="flex items-center gap-1.5">
            <span
              className={`inline-block w-2 h-2 rounded-full ${TOKEN_SEMANTICS.cacheRead.bgClass}`}
            />
            Cache Read (Hit)
          </span>
          <span className="font-mono font-medium text-slate-900 dark:text-slate-200">
            {formatTokenCompact(summary.cache_read_input_tokens)} (
            {calculateTokenPercent(
              summary.cache_read_input_tokens,
              summary.input_tokens,
            ).toFixed(1)}
            %)
          </span>
        </div>
        <div className="flex items-center justify-between">
          <span className="flex items-center gap-1.5">
            <span
              className={`inline-block w-2 h-2 rounded-full ${TOKEN_SEMANTICS.cacheCreation.bgClass}`}
            />
            Cache Creation
          </span>
          <span className="font-mono font-medium text-slate-900 dark:text-slate-200">
            {formatTokenCompact(summary.cache_creation_input_tokens)} (
            {calculateTokenPercent(
              summary.cache_creation_input_tokens,
              summary.input_tokens,
            ).toFixed(1)}
            %)
          </span>
        </div>
        <div className="flex items-center justify-between">
          <span className="flex items-center gap-1.5">
            <span
              className={`inline-block w-2 h-2 rounded-full ${TOKEN_SEMANTICS.fresh.bgClass}`}
            />
            Uncached Fresh
          </span>
          <span className="font-mono font-medium text-slate-900 dark:text-slate-200">
            {formatTokenCompact(summary.fresh_input_tokens)} (
            {calculateTokenPercent(
              summary.fresh_input_tokens,
              summary.input_tokens,
            ).toFixed(1)}
            %)
          </span>
        </div>
        {unclassifiedInputBigInt > 0n && (
          <div className="flex items-center justify-between">
            <span className="flex items-center gap-1.5">
              <span
                className={`inline-block w-2 h-2 rounded-full ${TOKEN_SEMANTICS.unclassifiedInput.bgClass}`}
              />
              Unclassified Input
            </span>
            <span className="font-mono font-medium text-slate-900 dark:text-slate-200">
              {formatTokenCompact(summary.unclassified_input_tokens)} (
              {calculateTokenPercent(
                summary.unclassified_input_tokens,
                summary.input_tokens,
              ).toFixed(1)}
              %)
            </span>
          </div>
        )}
      </div>
    </section>
  );
}

interface OutputTokensCardProps {
  summary: TokenSummaryDTO;
  outputSharePercent: number;
}

function OutputTokensCard({
  summary,
  outputSharePercent,
}: OutputTokensCardProps) {
  const unclassifiedOutputBigInt = parseTokenBigInt(
    summary.unclassified_output_tokens,
  );

  return (
    <section
      className="bg-white dark:bg-slate-900 border border-slate-200/80 dark:border-slate-800 rounded-2xl p-5 shadow-xs hover:shadow-md transition-all flex flex-col justify-between space-y-4"
      aria-labelledby="card-output-tokens-title"
    >
      <div>
        <div className="flex items-center justify-between">
          <span
            id="card-output-tokens-title"
            className="text-xs font-semibold uppercase tracking-wider text-slate-500 dark:text-slate-400"
          >
            Output Tokens
          </span>
          <div className="flex items-center gap-1.5">
            <span className="px-2 py-0.5 rounded-full text-xs font-semibold bg-violet-50 dark:bg-violet-950/40 text-violet-700 dark:text-violet-300">
              {outputSharePercent.toFixed(1)}% Total
            </span>
            <div
              className={`p-2 rounded-xl ${TOKEN_SEMANTICS.standardOutput.badgeBgClass}`}
            >
              <ArrowUpRight className="w-4 h-4" aria-hidden="true" />
            </div>
          </div>
        </div>

        <div className="mt-2">
          <div className="font-mono tracking-tight font-bold text-2xl text-slate-900 dark:text-slate-100">
            {formatTokenCompact(summary.output_tokens)}
          </div>
          <p className="text-xs text-slate-500 dark:text-slate-400 font-mono mt-0.5">
            {formatTokenLocale(summary.output_tokens)} tokens
          </p>
        </div>
      </div>

      <div className="space-y-2">
        <TokenMicroStackedBar
          segments={[
            {
              key: "reasoning",
              label: "Reasoning (CoT)",
              value: summary.reasoning_tokens,
              bgClass: TOKEN_SEMANTICS.reasoning.bgClass,
            },
            {
              key: "standard",
              label: "Standard Output",
              value: summary.standard_output_tokens,
              bgClass: TOKEN_SEMANTICS.standardOutput.bgClass,
            },
            {
              key: "unclassified_output",
              label: "Unclassified Output",
              value: summary.unclassified_output_tokens,
              bgClass: TOKEN_SEMANTICS.unclassifiedOutput.bgClass,
            },
          ]}
          totalValue={summary.output_tokens}
        />
      </div>

      <div className="border-t border-slate-100 dark:border-slate-800/80 pt-3 flex flex-col gap-1 text-xs text-slate-600 dark:text-slate-400">
        <div className="flex items-center justify-between">
          <span className="flex items-center gap-1.5">
            <span
              className={`inline-block w-2 h-2 rounded-full ${TOKEN_SEMANTICS.reasoning.bgClass}`}
            />
            Reasoning (CoT)
          </span>
          <span className="font-mono font-medium text-slate-900 dark:text-slate-200">
            {formatTokenCompact(summary.reasoning_tokens)} (
            {calculateTokenPercent(
              summary.reasoning_tokens,
              summary.output_tokens,
            ).toFixed(1)}
            %)
          </span>
        </div>
        <div className="flex items-center justify-between">
          <span className="flex items-center gap-1.5">
            <span
              className={`inline-block w-2 h-2 rounded-full ${TOKEN_SEMANTICS.standardOutput.bgClass}`}
            />
            Standard Output
          </span>
          <span className="font-mono font-medium text-slate-900 dark:text-slate-200">
            {formatTokenCompact(summary.standard_output_tokens)} (
            {calculateTokenPercent(
              summary.standard_output_tokens,
              summary.output_tokens,
            ).toFixed(1)}
            %)
          </span>
        </div>
        {unclassifiedOutputBigInt > 0n && (
          <div className="flex items-center justify-between">
            <span className="flex items-center gap-1.5">
              <span
                className={`inline-block w-2 h-2 rounded-full ${TOKEN_SEMANTICS.unclassifiedOutput.bgClass}`}
              />
              Unclassified Output
            </span>
            <span className="font-mono font-medium text-slate-900 dark:text-slate-200">
              {formatTokenCompact(summary.unclassified_output_tokens)} (
              {calculateTokenPercent(
                summary.unclassified_output_tokens,
                summary.output_tokens,
              ).toFixed(1)}
              %)
            </span>
          </div>
        )}
      </div>
    </section>
  );
}

interface EfficiencyQualityCardProps {
  summary: TokenSummaryDTO;
  coverage: TokenCoverageDTO;
  dataQuality: TokenDataQualityDTO;
  avgTokensPerComparableReq: number;
}

function EfficiencyQualityCard({
  summary,
  coverage,
  dataQuality,
  avgTokensPerComparableReq,
}: EfficiencyQualityCardProps) {
  return (
    <section
      className="bg-white dark:bg-slate-900 border border-slate-200/80 dark:border-slate-800 rounded-2xl p-5 shadow-xs hover:shadow-md transition-all flex flex-col justify-between space-y-4"
      aria-labelledby="card-efficiency-title"
    >
      <div>
        <div className="flex items-center justify-between">
          <span
            id="card-efficiency-title"
            className="text-xs font-semibold uppercase tracking-wider text-slate-500 dark:text-slate-400"
          >
            Efficiency & Quality
          </span>
          <div
            className={`p-2 rounded-xl ${TOKEN_SEMANTICS.cacheRead.badgeBgClass}`}
          >
            <Activity className="w-4 h-4" aria-hidden="true" />
          </div>
        </div>

        <div className="mt-2 space-y-1">
          <div className="flex items-center justify-between">
            <span className="text-sm font-medium text-slate-700 dark:text-slate-300">
              Cache Hit Rate
            </span>
            <span className="font-mono font-bold text-base text-emerald-600 dark:text-emerald-400">
              {(summary.cache_hit_rate * 100).toFixed(1)}%
            </span>
          </div>
          <div className="flex items-center justify-between">
            <span className="text-sm font-medium text-slate-700 dark:text-slate-300">
              Reasoning Ratio
            </span>
            <span className="font-mono font-bold text-base text-fuchsia-600 dark:text-fuchsia-400">
              {(summary.reasoning_ratio * 100).toFixed(1)}%
            </span>
          </div>
        </div>
      </div>

      <div className="space-y-1 text-xs text-slate-600 dark:text-slate-400">
        <div className="flex items-center justify-between">
          <span>Avg / Comparable Req</span>
          <span className="font-mono font-medium text-slate-900 dark:text-slate-200">
            {formatTokenCompact(avgTokensPerComparableReq)} tok
          </span>
        </div>
      </div>

      <div className="border-t border-slate-100 dark:border-slate-800/80 pt-3 flex flex-col gap-1 text-xs text-slate-600 dark:text-slate-400">
        <div className="flex items-center justify-between">
          <span>Coverage</span>
          <span className="font-mono font-medium text-slate-900 dark:text-slate-200">
            {coverage.comparable_requests.toLocaleString()} /{" "}
            {coverage.total_requests.toLocaleString()} (
            {(coverage.rate * 100).toFixed(1)}%)
          </span>
        </div>
        <div className="flex items-center justify-between">
          <span>Observed Quality</span>
          <span className="font-mono font-medium text-slate-900 dark:text-slate-200">
            {coverage.comparable_requests.toLocaleString()} /{" "}
            {coverage.observed_requests.toLocaleString()} (
            {(dataQuality.quality_rate * 100).toFixed(1)}%)
          </span>
        </div>
      </div>
    </section>
  );
}

interface TokenHeroCardsProps {
  summary: TokenSummaryDTO;
  coverage: TokenCoverageDTO;
  dataQuality: TokenDataQualityDTO;
  timeseries: TokenBucketDTO[];
}

export function TokenHeroCards({
  summary,
  coverage,
  dataQuality,
  timeseries,
}: TokenHeroCardsProps) {
  // Peak bucket volume
  const peakTokensBigInt = timeseries.reduce((max, b) => {
    const bTotal = parseTokenBigInt(b.total_tokens);
    return bTotal > max ? bTotal : max;
  }, 0n);

  const totalTokensBigInt = parseTokenBigInt(summary.total_tokens);

  // Average tokens per comparable request
  const avgTokensPerComparableReq =
    coverage.comparable_requests > 0
      ? Number(totalTokensBigInt / BigInt(coverage.comparable_requests))
      : 0;

  const inputSharePercent = calculateTokenPercent(
    summary.input_tokens,
    summary.total_tokens,
  );
  const outputSharePercent = calculateTokenPercent(
    summary.output_tokens,
    summary.total_tokens,
  );

  return (
    <div className="grid grid-cols-1 sm:grid-cols-2 xl:grid-cols-4 gap-4">
      <TotalTokensCard
        summary={summary}
        coverage={coverage}
        peakTokens={peakTokensBigInt}
        inputSharePercent={inputSharePercent}
        outputSharePercent={outputSharePercent}
      />
      <InputTokensCard
        summary={summary}
        inputSharePercent={inputSharePercent}
      />
      <OutputTokensCard
        summary={summary}
        outputSharePercent={outputSharePercent}
      />
      <EfficiencyQualityCard
        summary={summary}
        coverage={coverage}
        dataQuality={dataQuality}
        avgTokensPerComparableReq={avgTokensPerComparableReq}
      />
    </div>
  );
}
