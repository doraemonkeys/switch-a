import { FlaskConical, ListFilter, RefreshCw } from "lucide-react";

export type DetectionWorkspace = "rules" | "test";

interface DetectionWorkspaceHeaderProps {
  readonly workspace: DetectionWorkspace;
  readonly onWorkspaceChange: (workspace: DetectionWorkspace) => void;
  readonly enabledCount: number;
  readonly ruleCount: number;
  readonly loaded: boolean;
  readonly busy: boolean;
  readonly refreshing: boolean;
  readonly onRefresh: () => void;
}

export function DetectionWorkspaceHeader({
  workspace,
  onWorkspaceChange,
  enabledCount,
  ruleCount,
  loaded,
  busy,
  refreshing,
  onRefresh,
}: DetectionWorkspaceHeaderProps) {
  return (
    <header className="detection-header">
      <div className="detection-heading">
        <div>
          <span className="detection-eyebrow">TRAFFIC POLICIES</span>
          <h1>Error detection</h1>
          <p>Decide how structured upstream errors are handled.</p>
        </div>
        <div className="detection-header-actions">
          {loaded && (
            <span className="detection-health">
              <span />
              {enabledCount} of {ruleCount} rules enabled
            </span>
          )}
          <button
            type="button"
            disabled={busy}
            onClick={onRefresh}
            className="btn btn-secondary btn-sm"
          >
            <RefreshCw
              className={`h-4 w-4 ${refreshing ? "animate-spin" : ""}`}
              aria-hidden="true"
            />
            Refresh
          </button>
        </div>
      </div>
      <nav
        className="detection-navigation"
        aria-label="Error detection workspace"
      >
        <button
          type="button"
          aria-pressed={workspace === "rules"}
          onClick={() => onWorkspaceChange("rules")}
        >
          <ListFilter size={16} aria-hidden="true" />
          Rules
          {loaded && <span className="detection-count">{ruleCount}</span>}
        </button>
        <button
          type="button"
          aria-pressed={workspace === "test"}
          onClick={() => onWorkspaceChange("test")}
        >
          <FlaskConical size={16} aria-hidden="true" />
          Test Message
        </button>
      </nav>
    </header>
  );
}
