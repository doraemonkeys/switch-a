import type {
  ClientTuple,
  DisguiseState,
  ProfileRevision,
  ReferenceSource,
} from "@/api/client-disguise/types";

import { CLIENT_TYPES } from "./clientTypes";

const PLATFORM_NAMES: Record<string, string> = {
  windows: "Windows",
  macos: "macOS",
  linux: "Linux",
};
const ARCH_NAMES: Record<string, string> = { amd64: "x64", arm64: "ARM64" };

export function environmentKey(tuple: ClientTuple) {
  return [tuple.client_type, tuple.platform, tuple.arch].join("/");
}
export function environmentLabel(tuple: ClientTuple) {
  return [
    CLIENT_TYPES[tuple.client_type]?.name ?? tuple.client_type,
    PLATFORM_NAMES[tuple.platform] ?? tuple.platform,
    ARCH_NAMES[tuple.arch] ?? tuple.arch,
  ].join(" · ");
}
export function sourceName(id: string, references: ReferenceSource[]) {
  if (id === "builtin") return "内置配置";
  return references.find((source) => source.id === id)?.name || id;
}
export function captureLabel(profile: ProfileRevision) {
  if (profile.source_id === "builtin") return "内置配置";
  const time = Date.parse(profile.captured_at);
  if (!Number.isFinite(time) || time <= 0) return "采集时间未知";
  return new Date(time).toLocaleString("zh-CN", { hour12: false });
}

function versionParts(version: string) {
  const value = version.trim().replace(/^v/, "").split("+")[0];
  const separator = value.indexOf("-");
  const release = separator < 0 ? value : value.slice(0, separator);
  const prerelease = separator < 0 ? "" : value.slice(separator + 1);
  return { release: release.split(".").map(Number), prerelease };
}
function compareIdentifiers(a: string, b: string) {
  if (a === b) return 0;
  const numericA = /^\d+$/.test(a);
  const numericB = /^\d+$/.test(b);
  if (numericA && numericB) return Number(a) - Number(b);
  if (numericA !== numericB) return numericA ? -1 : 1;
  return a < b ? -1 : 1;
}
// Release precedence must agree with automatic following: alpha.10 follows alpha.9,
// and a later capture of an older release never means a version upgrade.
export function compareVersions(a: string, b: string) {
  const left = versionParts(a),
    right = versionParts(b);
  for (
    let i = 0;
    i < Math.max(left.release.length, right.release.length);
    i++
  ) {
    const difference = (left.release[i] || 0) - (right.release[i] || 0);
    if (difference) return difference;
  }
  if (left.prerelease === right.prerelease) return 0;
  if (!left.prerelease || !right.prerelease) return left.prerelease ? -1 : 1;
  const l = left.prerelease.split("."),
    r = right.prerelease.split(".");
  for (let i = 0; i < Math.min(l.length, r.length); i++) {
    const difference = compareIdentifiers(l[i], r[i]);
    if (difference) return difference;
  }
  return l.length - r.length;
}
function captureOrder(a: ProfileRevision, b: ProfileRevision) {
  return (
    (Date.parse(b.captured_at) || 0) - (Date.parse(a.captured_at) || 0) ||
    (Date.parse(b.created_at) || 0) - (Date.parse(a.created_at) || 0) ||
    a.id.localeCompare(b.id)
  );
}
export function sortedProfiles(profiles: ProfileRevision[]) {
  return [...profiles].sort(
    (a, b) =>
      compareVersions(b.client_version, a.client_version) || captureOrder(a, b),
  );
}
export function environmentProfiles(state: DisguiseState, environment: string) {
  return sortedProfiles(
    state.profiles.filter(
      (profile) => environmentKey(profile.tuple) === environment,
    ),
  );
}
export function profileEnvironments(profiles: ProfileRevision[]) {
  const tuples = new Map(
    profiles.map((profile) => [environmentKey(profile.tuple), profile.tuple]),
  );
  return [...tuples].sort(([, a], [, b]) =>
    environmentLabel(a).localeCompare(environmentLabel(b)),
  );
}
export function referenceHead(
  state: DisguiseState,
  environment: string,
  source: string,
) {
  const track = state.tracks.find(
    (item) => environmentKey(item) === environment && item.source_id === source,
  );
  return state.profiles.find(
    (profile) =>
      profile.id === track?.revision_id &&
      profile.source_id === source &&
      environmentKey(profile.tuple) === environment,
  );
}
export function availableReferences(
  state: DisguiseState,
  environment: string,
  selected: string,
) {
  return state.references.filter(
    (source) =>
      source.id === selected ||
      !state.profiles.some((profile) => profile.source_id === source.id) ||
      state.profiles.some(
        (profile) =>
          profile.source_id === source.id &&
          environmentKey(profile.tuple) === environment,
      ),
  );
}
export interface SnapshotGroup {
  key: string;
  version: string;
  sourceID: string;
  profiles: ProfileRevision[];
  currentID?: string;
}
export function snapshotGroups(
  state: DisguiseState,
  environment: string,
): SnapshotGroup[] {
  const groups = new Map<string, SnapshotGroup>();
  for (const profile of environmentProfiles(state, environment)) {
    const key = JSON.stringify([profile.client_version, profile.source_id]);
    const group: SnapshotGroup = groups.get(key) ?? {
      key,
      version: profile.client_version,
      sourceID: profile.source_id,
      profiles: [],
      currentID: referenceHead(state, environment, profile.source_id)?.id,
    };
    group.profiles.push(profile);
    groups.set(key, group);
  }
  return [...groups.values()];
}
