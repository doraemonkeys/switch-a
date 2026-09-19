import { useId, useState } from "react";
import type {
  DisguiseState,
  ProfileRevision,
} from "@/api/client-disguise/types";
import {
  captureLabel,
  snapshotGroups,
  sourceName,
  type SnapshotGroup,
} from "./profileCatalog";
import { featureDifferences, featureSummary } from "./profileFeatures";
import { ProfileComparison } from "./ProfileComparison";

const INLINE_DIFFERENCE_COUNT = 2;

function SnapshotChoice({
  profile,
  baseline,
  current,
  selected,
  source,
  name,
  select,
}: {
  profile: ProfileRevision;
  baseline?: ProfileRevision;
  current: boolean;
  selected: boolean;
  source: string;
  name: string;
  select: (id: string) => void;
}) {
  const differences = baseline ? featureDifferences(baseline, profile) : [];
  const summary = featureSummary(profile);
  const extraChanges =
    differences.length > INLINE_DIFFERENCE_COUNT
      ? `等 ${differences.length} 项`
      : "";
  const change = differences.length
    ? "同组差异：" +
      differences
        .slice(0, INLINE_DIFFERENCE_COUNT)
        .map((field) => field.label)
        .join("、") +
      extraChanges
    : "环境特征相同，仅采集记录不同";
  return (
    <div className="cd-snapshot-choice" data-selected={selected}>
      <label>
        <input
          type="radio"
          name={name}
          value={profile.id}
          checked={selected}
          onChange={() => select(profile.id)}
          aria-label={`${profile.client_version} · ${source} · ${captureLabel(profile)} · ${summary || profile.features.originator}`}
        />
        <span className="cd-snapshot-copy">
          <span className="cd-snapshot-title">
            {source}
            {current && <span className="cd-snapshot-badge">来源当前</span>}
            {selected && <span className="cd-snapshot-badge">已选</span>}
          </span>
          <small>{captureLabel(profile)}</small>
          {summary && <small>{summary}</small>}
          {baseline && <small className="cd-snapshot-change">{change}</small>}
        </span>
      </label>
      {baseline && <ProfileComparison profile={profile} baseline={baseline} />}
    </div>
  );
}

function RevisionGroup({
  group,
  state,
  revisionID,
  select,
  name,
}: {
  group: SnapshotGroup;
  state: DisguiseState;
  revisionID: string;
  select: (id: string) => void;
  name: string;
}) {
  const [expanded, setExpanded] = useState(false);
  const historyID = useId();
  const primary =
    group.profiles.find((profile) => profile.id === group.currentID) ??
    group.profiles[0];
  const history = group.profiles.filter((profile) => profile.id !== primary.id);
  const remaining = history.filter((profile) => profile.id !== revisionID);
  const selectedHistory = history.find((profile) => profile.id === revisionID);
  const source = sourceName(group.sourceID, state.references);
  const choice = (profile: ProfileRevision, baseline?: ProfileRevision) => (
    <SnapshotChoice
      key={profile.id}
      profile={profile}
      baseline={baseline}
      current={profile.id === group.currentID}
      selected={profile.id === revisionID}
      source={source}
      name={name}
      select={select}
    />
  );
  return (
    <div className="cd-snapshot-group">
      <div className="cd-snapshot-group-title">
        <strong>{group.version}</strong>
        <span className="cd-snapshot-badge">
          {group.version.includes("-") ? "预发布" : "正式版"}
        </span>
        <small>{group.profiles.length} 个快照</small>
      </div>
      {choice(primary, history[0])}
      {selectedHistory && choice(selectedHistory, primary)}
      {remaining.length > 0 && (
        <>
          <button
            type="button"
            className="cd-snapshot-history"
            aria-expanded={expanded}
            aria-controls={historyID}
            onClick={() => setExpanded(!expanded)}
          >
            {expanded
              ? "收起同版本历史"
              : `查看同版本历史（${remaining.length}）`}
          </button>
          <div id={historyID} hidden={!expanded}>
            {expanded && remaining.map((profile) => choice(profile, primary))}
          </div>
        </>
      )}
    </div>
  );
}
export function SnapshotPicker({
  state,
  environment,
  revisionID,
  select,
}: {
  state: DisguiseState;
  environment: string;
  revisionID: string;
  select: (id: string) => void;
}) {
  const name = useId();
  const groups = snapshotGroups(state, environment);
  return (
    <fieldset className="cd-snapshot-picker" aria-label="环境快照">
      <legend>
        环境快照 <span>版本号为采集时版本</span>
      </legend>
      {groups.map((group) => (
        <RevisionGroup
          key={group.key}
          group={group}
          state={state}
          revisionID={revisionID}
          select={select}
          name={name}
        />
      ))}
      {groups.length === 0 && (
        <p className="cd-field-help">该环境没有可用快照。</p>
      )}
    </fieldset>
  );
}
