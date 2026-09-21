import type {
  ClientFeatures,
  ProfileRevision,
} from "@/api/client-disguise/types";

const FEATURE_LABELS: Record<
  Exclude<keyof ClientFeatures, "headers">,
  string
> = {
  user_agent: "User-Agent",
  originator: "Originator",
  client_version: "客户端版本",
  desktop_build: "Desktop build",
  os_version: "系统版本",
};
const CODEX_VERSION =
  /codex[_ /-]*(?:desktop|cli_rs|cli|tui|exec|browser-use)?[/ ](\d+(?:\.\d+){1,3}(?:[-+][a-z\d.-]+)?)/i;
export const UNOBSERVED = "沿用原请求";

function headerValue(profile: ProfileRevision, name: string) {
  const headers = profile.features.headers ?? {};
  const key = Object.keys(headers)
    .sort()
    .find((key) => key.toLowerCase() === name.toLowerCase());
  return headers[name] ?? (key ? headers[key] : "");
}
export function sampledUserAgent(profile: ProfileRevision) {
  return profile.features.user_agent || headerValue(profile, "User-Agent");
}
export function requestVersion(profile: ProfileRevision) {
  const ua = sampledUserAgent(profile);
  return (
    CODEX_VERSION.exec(ua)?.[1] ||
    profile.client_version ||
    profile.features.client_version ||
    headerValue(profile, "Version") ||
    headerValue(profile, "X-Client-Version")
  );
}
export function featureFields(profile: ProfileRevision) {
  const fields = Object.entries(FEATURE_LABELS).map(([key, label]) => ({
    key,
    label,
    value: profile.features[key as keyof typeof FEATURE_LABELS],
  }));
  return [
    ...fields,
    ...Object.entries(profile.features.headers ?? {})
      .sort(([a], [b]) => a.localeCompare(b))
      .map(([name, value]) => ({
        key: "header:" + name,
        label: "Header · " + name,
        value,
      })),
  ];
}
export function featureDifferences(
  previous: ProfileRevision,
  next: ProfileRevision,
) {
  const before = new Map(
    featureFields(previous).map((field) => [field.key, field]),
  );
  const after = new Map(featureFields(next).map((field) => [field.key, field]));
  return [...new Set([...before.keys(), ...after.keys()])].flatMap((key) => {
    const oldField = before.get(key),
      newField = after.get(key);
    const from = oldField?.value ?? "",
      to = newField?.value ?? "";
    return from === to
      ? []
      : [{ key, label: newField?.label ?? oldField!.label, from, to }];
  });
}
export function featureSummary(profile: ProfileRevision) {
  return [
    profile.features.os_version && "系统 " + profile.features.os_version,
    profile.features.desktop_build &&
      "Desktop build " + profile.features.desktop_build,
    !sampledUserAgent(profile) && "部分特征 · 未采集 User-Agent",
  ]
    .filter(Boolean)
    .join(" · ");
}
