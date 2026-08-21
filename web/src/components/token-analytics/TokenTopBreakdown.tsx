import type { TokenModelRankDTO, TokenProviderRankDTO } from "../../api/types";
import { formatTokenCompact, TOKEN_SEMANTICS } from "./token-format";
import {
  TokenMicroStackedBar,
  type StackedBarSegment,
} from "./TokenMicroStackedBar";

interface TokenTopBreakdownProps {
  byProvider: TokenProviderRankDTO[];
  byModel: TokenModelRankDTO[];
}

function buildItemSegments(
  item: TokenProviderRankDTO | TokenModelRankDTO,
): StackedBarSegment[] {
  return [
    {
      key: "fresh",
      label: "Fresh Input",
      value: item.fresh_input_tokens,
      bgClass: TOKEN_SEMANTICS.fresh.bgClass,
    },
    {
      key: "cache_read",
      label: "Cache Read",
      value: item.cache_read_input_tokens,
      bgClass: TOKEN_SEMANTICS.cacheRead.bgClass,
    },
    {
      key: "cache_creation",
      label: "Cache Creation",
      value: item.cache_creation_input_tokens,
      bgClass: TOKEN_SEMANTICS.cacheCreation.bgClass,
    },
    {
      key: "unclassified_input",
      label: "Unclassified Input",
      value: item.unclassified_input_tokens,
      bgClass: TOKEN_SEMANTICS.unclassifiedInput.bgClass,
    },
    {
      key: "standard",
      label: "Standard Output",
      value: item.standard_output_tokens,
      bgClass: TOKEN_SEMANTICS.standardOutput.bgClass,
    },
    {
      key: "reasoning",
      label: "Reasoning CoT",
      value: item.reasoning_tokens,
      bgClass: TOKEN_SEMANTICS.reasoning.bgClass,
    },
    {
      key: "unclassified_output",
      label: "Unclassified Output",
      value: item.unclassified_output_tokens,
      bgClass: TOKEN_SEMANTICS.unclassifiedOutput.bgClass,
    },
  ];
}

export function TokenTopBreakdown({
  byProvider,
  byModel,
}: TokenTopBreakdownProps) {
  return (
    <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
      {/* TOP PROVIDERS */}
      <section
        className="bg-white dark:bg-slate-900 border border-slate-200/80 dark:border-slate-800 rounded-2xl p-5 shadow-xs space-y-4"
        aria-labelledby="top-providers-heading"
      >
        <div className="flex items-center justify-between border-b border-slate-100 dark:border-slate-800 pb-3">
          <h3
            id="top-providers-heading"
            className="text-base font-semibold text-slate-900 dark:text-slate-100 flex items-center gap-2"
          >
            <span>🏢</span>
            <span>Top Providers</span>
          </h3>
          <span className="text-xs text-slate-400 dark:text-slate-500 font-mono">
            {byProvider.length} active
          </span>
        </div>

        {byProvider.length === 0 ? (
          <div className="py-8 text-center text-xs text-slate-400 dark:text-slate-500">
            No provider usage recorded in this time window.
          </div>
        ) : (
          <div className="space-y-4">
            {byProvider.map((p, index) => {
              const displayName =
                p.provider_name || p.provider_id || "Unknown Provider";
              const segments = buildItemSegments(p);

              return (
                <div key={p.provider_id || index} className="space-y-1.5">
                  <div className="flex items-center justify-between text-xs">
                    <span className="font-medium text-slate-800 dark:text-slate-200 truncate pr-2">
                      <span className="text-slate-400 dark:text-slate-500 font-mono mr-1.5">
                        {index + 1}.
                      </span>
                      {displayName}
                    </span>
                    <span className="font-mono text-slate-500 dark:text-slate-400 shrink-0">
                      <strong className="text-slate-900 dark:text-slate-100">
                        {formatTokenCompact(p.total_tokens)}
                      </strong>{" "}
                      ({(p.share * 100).toFixed(1)}%) •{" "}
                      {p.request_count.toLocaleString()} reqs
                    </span>
                  </div>

                  <TokenMicroStackedBar
                    segments={segments}
                    totalValue={p.total_tokens}
                    heightClass="h-2.5"
                  />
                </div>
              );
            })}
          </div>
        )}
      </section>

      {/* TOP MODELS */}
      <section
        className="bg-white dark:bg-slate-900 border border-slate-200/80 dark:border-slate-800 rounded-2xl p-5 shadow-xs space-y-4"
        aria-labelledby="top-models-heading"
      >
        <div className="flex items-center justify-between border-b border-slate-100 dark:border-slate-800 pb-3">
          <h3
            id="top-models-heading"
            className="text-base font-semibold text-slate-900 dark:text-slate-100 flex items-center gap-2"
          >
            <span>🤖</span>
            <span>Top Models</span>
          </h3>
          <span className="text-xs text-slate-400 dark:text-slate-500 font-mono">
            {byModel.length} active
          </span>
        </div>

        {byModel.length === 0 ? (
          <div className="py-8 text-center text-xs text-slate-400 dark:text-slate-500">
            No model usage recorded in this time window.
          </div>
        ) : (
          <div className="space-y-4">
            {byModel.map((m, index) => {
              const segments = buildItemSegments(m);

              return (
                <div key={m.model || index} className="space-y-1.5">
                  <div className="flex items-center justify-between text-xs">
                    <span className="font-medium font-mono text-slate-800 dark:text-slate-200 truncate pr-2">
                      <span className="text-slate-400 dark:text-slate-500 mr-1.5">
                        {index + 1}.
                      </span>
                      {m.model || "Unknown Model"}
                    </span>
                    <span className="font-mono text-slate-500 dark:text-slate-400 shrink-0">
                      <strong className="text-slate-900 dark:text-slate-100">
                        {formatTokenCompact(m.total_tokens)}
                      </strong>{" "}
                      ({(m.share * 100).toFixed(1)}%) •{" "}
                      {m.request_count.toLocaleString()} reqs
                    </span>
                  </div>

                  <TokenMicroStackedBar
                    segments={segments}
                    totalValue={m.total_tokens}
                    heightClass="h-2.5"
                  />
                </div>
              );
            })}
          </div>
        )}
      </section>
    </div>
  );
}
