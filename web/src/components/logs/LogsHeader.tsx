interface LogsHeaderProps {
  loading: boolean;
  onRefresh: () => void;
}

export function LogsHeader({ loading, onRefresh }: LogsHeaderProps) {
  return (
    <div className="flex items-center justify-between">
      <div>
        <h2 className="text-2xl font-bold text-text-primary">Request Logs</h2>
        <p className="text-text-secondary mt-1">
          View and filter request history
        </p>
      </div>
      <button
        onClick={onRefresh}
        className="btn btn-secondary btn-sm"
        disabled={loading}
        title="Refresh logs"
      >
        <span className={loading ? "animate-spin" : ""}>🔄</span>
        Refresh
      </button>
    </div>
  );
}
