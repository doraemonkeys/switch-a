import { Pin, RefreshCw } from "lucide-react";
import type { LoginDraft } from "../loginDraft";

export function ProfileMode({
  draft,
  change,
}: {
  draft: LoginDraft;
  change: (draft: LoginDraft) => void;
}) {
  return (
    <fieldset className="cd-mode-fieldset">
      <legend>环境更新方式</legend>
      <div className="cd-mode-options">
        <label className="cd-mode-option" data-selected={draft.mode === "auto"}>
          <input
            type="radio"
            name="update-mode"
            value="auto"
            checked={draft.mode === "auto"}
            onChange={() => change({ ...draft, mode: "auto" })}
          />
          <RefreshCw size={18} aria-hidden="true" />
          <span>
            <strong>自动跟随</strong>
            <small>采用参考来源后续的匹配快照</small>
          </span>
        </label>
        <label
          className="cd-mode-option"
          data-selected={draft.mode === "pinned"}
        >
          <input
            type="radio"
            name="update-mode"
            value="pinned"
            checked={draft.mode === "pinned"}
            onChange={() => change({ ...draft, mode: "pinned" })}
          />
          <Pin size={18} aria-hidden="true" />
          <span>
            <strong>固定快照</strong>
            <small>保持选定的环境特征</small>
          </span>
        </label>
      </div>
    </fieldset>
  );
}
