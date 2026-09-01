import type { FormActionsProps } from "./types";

export function ConfigFormActions({
  isDirty,
  saving,
  onReset,
}: FormActionsProps) {
  return (
    <div className="flex items-center justify-between pt-6 border-t border-border">
      <p className="text-sm text-text-muted">
        Saved changes apply to subsequent requests. Active WebSocket connections
        require reconnection.
      </p>
      <div className="flex gap-3">
        <button
          type="button"
          className="btn btn-secondary"
          onClick={onReset}
          disabled={!isDirty || saving}
        >
          Reset
        </button>
        <button
          type="submit"
          className="btn btn-primary"
          disabled={!isDirty || saving}
        >
          {saving ? (
            <span className="w-4 h-4 border-2 border-white/30 border-t-white rounded-full animate-spin mr-2"></span>
          ) : (
            <span className="mr-2">💾</span>
          )}
          {saving ? "Saving..." : "Save Changes"}
        </button>
      </div>
    </div>
  );
}
