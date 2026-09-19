import { useId, useState } from "react";
import { ChevronDown, RefreshCw, Search } from "lucide-react";
import type {
  ClientIdentityView,
  ReferenceSource,
} from "@/api/client-disguise/types";
import {
  clientPlatform,
  clientRequestTime,
  clientTitle,
  referenceClientOptions,
  requestTime,
  shortClientID,
} from "./clientPresentation";
import "./reference-client-picker.css";

function ReferenceSourceLabels({ sources }: { sources: ReferenceSource[] }) {
  return (
    <span className="cd-reference-sources">
      {sources.map((source) => (
        <span key={source.id} className="cd-reference-source">
          <strong>{source.name}</strong>
          <span>
            Source ID <code>{source.id}</code>
          </span>
        </span>
      ))}
    </span>
  );
}

export function ReferenceClientPicker({
  clients,
  references,
  value,
  onChange,
  refresh,
  refreshing,
}: {
  clients: ClientIdentityView[];
  references: ReferenceSource[];
  value: string;
  onChange: (id: string) => void;
  refresh: () => void;
  refreshing: boolean;
}) {
  const id = useId();
  const [query, setQuery] = useState("");
  const options = referenceClientOptions(clients, references);
  const latestTime = options.length ? requestTime(options[0].client) : 0;
  const selected = options.find(({ client }) => client.client_id === value);
  const search = query.trim().toLowerCase();
  const filtered = options.filter(({ client, sources }) =>
    [
      client.client_id,
      clientTitle(client),
      clientPlatform(client),
      client.last_request?.user_agent,
      ...sources.flatMap((source) => [source.name, source.id]),
    ]
      .join(" ")
      .toLowerCase()
      .includes(search),
  );

  return (
    <div className="cd-reference-picker">
      <div className="cd-reference-picker-heading">
        <span id={id}>Reference client</span>
        <button
          type="button"
          className="cd-reference-refresh"
          onClick={refresh}
          disabled={refreshing}
        >
          <RefreshCw size={13} aria-hidden="true" />
          {refreshing ? "刷新中…" : "刷新客户端"}
        </button>
      </div>
      <p id={id + "-help"} className="cd-field-help">
        已关联的客户端显示参考源名称和 Source ID，按最近请求排序。
      </p>
      <label className="cd-reference-search">
        <Search size={15} aria-hidden="true" />
        <input
          type="search"
          aria-label="搜索参考客户端"
          placeholder="搜索参考源名称、Source ID、客户端或系统"
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          onKeyDown={(event) => {
            // Filtering is independent of committing the reference source form.
            if (event.key === "Enter") event.preventDefault();
          }}
        />
      </label>
      <div
        className="cd-reference-options"
        role="radiogroup"
        aria-labelledby={id}
        aria-describedby={id + "-help"}
        aria-busy={refreshing}
      >
        {filtered.map(({ client, sources }) => (
          <label
            key={client.client_id}
            className="cd-reference-option"
            data-selected={client.client_id === value}
          >
            <input
              type="radio"
              name={id}
              value={client.client_id}
              checked={client.client_id === value}
              onChange={() => onChange(client.client_id)}
            />
            <span className="cd-reference-option-body">
              <span className="cd-reference-option-heading">
                {sources.length > 0 ? (
                  <ReferenceSourceLabels sources={sources} />
                ) : (
                  <strong>{clientTitle(client)}</strong>
                )}
                {latestTime > 0 && requestTime(client) === latestTime && (
                  <span className="cd-reference-latest">最近请求</span>
                )}
              </span>
              {sources.length > 0 && (
                <span className="cd-reference-client-type">
                  {clientTitle(client)}
                </span>
              )}
              <span className="cd-reference-platform">
                {clientPlatform(client)}{" "}
                <span title={client.client_id}>
                  · Client ID {shortClientID(client.client_id)}
                </span>
              </span>
              <span className="cd-reference-time">
                {client.last_request ? (
                  <time dateTime={client.last_request.observed_at}>
                    {clientRequestTime(client)}
                  </time>
                ) : (
                  "暂无请求记录"
                )}
              </span>
            </span>
          </label>
        ))}
        {!filtered.length && (
          <p className="cd-reference-empty">
            {clients.length
              ? "没有匹配的客户端，试试其他关键词。"
              : "暂无客户端。先从要参考的客户端发送一次请求，再刷新。"}
          </p>
        )}
      </div>
      {selected && (
        <details className="cd-reference-selection">
          <summary>
            <span>
              已选：
              {selected.sources.length > 0
                ? selected.sources.map((source) => source.name).join("、")
                : clientTitle(selected.client)}
            </span>
            <ChevronDown size={14} aria-hidden="true" />
          </summary>
          <dl className="cd-detail-grid">
            {selected.sources.length > 0 && (
              <div>
                <dt>Reference sources</dt>
                <dd>
                  <ReferenceSourceLabels sources={selected.sources} />
                </dd>
              </div>
            )}
            <div>
              <dt>Client ID</dt>
              <dd>{selected.client.client_id}</dd>
            </div>
            <div>
              <dt>最近请求时间</dt>
              <dd>{clientRequestTime(selected.client)}</dd>
            </div>
            <div>
              <dt>User-Agent</dt>
              <dd>{selected.client.last_request?.user_agent || "暂无记录"}</dd>
            </div>
            <div>
              <dt>Originator</dt>
              <dd>{selected.client.last_request?.originator || "暂无记录"}</dd>
            </div>
          </dl>
        </details>
      )}
      {value && !selected && (
        <p className="cd-field-help">所选客户端已不在列表中，请重新选择。</p>
      )}
      <p className="cd-field-help">
        客户端按 API Key 区分；共用 Key 的设备会显示为同一身份。时间随 HTTP
        请求、WebSocket 建连及新一轮请求更新。
      </p>
    </div>
  );
}
