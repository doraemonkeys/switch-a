import type { ClientIdentityView } from "@/api/client-disguise/types";

const CLIENT_NAMES: Record<string, string> = {
  desktop: "Codex Desktop",
  cli: "Codex CLI",
  tui: "Codex TUI",
};
const PLATFORM_NAMES: Record<string, string> = {
  windows: "Windows",
  macos: "macOS",
  linux: "Linux",
};
const SHORT_ID_LENGTH = 8;

export function clientTitle(client: ClientIdentityView): string {
  const request = client.last_request;
  if (!request) return "待识别客户端";
  const name =
    CLIENT_NAMES[request.tuple.client_type] ||
    request.originator ||
    "未知客户端";
  return [name, request.client_version].filter(Boolean).join(" ");
}

export function clientPlatform(client: ClientIdentityView): string {
  const tuple = client.last_request?.tuple;
  return [
    PLATFORM_NAMES[tuple?.platform ?? ""] || tuple?.platform || "系统未知",
    tuple?.arch,
  ]
    .filter(Boolean)
    .join(" · ");
}

export function shortClientID(id: string): string {
  return id.length > SHORT_ID_LENGTH ? id.slice(0, SHORT_ID_LENGTH) + "…" : id;
}

export function requestTime(client: ClientIdentityView): number {
  const value = client.last_request?.observed_at;
  return value ? Date.parse(value) || 0 : 0;
}

export function clientRequestTime(client: ClientIdentityView): string {
  const timestamp = requestTime(client);
  return timestamp
    ? new Date(timestamp).toLocaleString("zh-CN", { hour12: false })
    : "暂无请求记录";
}
