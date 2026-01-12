export function Logs() {
  return (
    <div className="space-y-6">
      {/* Page Header */}
      <div className="flex items-center justify-between">
        <div>
          <h2 className="text-2xl font-bold text-text-primary">Request Logs</h2>
          <p className="text-text-secondary mt-1">查看请求日志记录</p>
        </div>
        <button className="btn btn-secondary btn-sm">
          <span>🔄</span>
          Refresh
        </button>
      </div>

      {/* Filter Bar */}
      <div className="card p-4">
        <div className="flex flex-wrap items-center gap-3">
          <div className="flex-1 min-w-[200px] relative">
            <span className="absolute left-3 top-1/2 -translate-y-1/2 text-text-muted">
              🔍
            </span>
            <input
              type="text"
              placeholder="Search by model, IP, or user..."
              className="input pl-10"
            />
          </div>
          <select className="input w-auto">
            <option value="">All Providers</option>
          </select>
          <select className="input w-auto">
            <option value="">All API Types</option>
            <option value="claude">Claude</option>
            <option value="codex">Codex</option>
            <option value="gemini">Gemini</option>
          </select>
          <select className="input w-auto">
            <option value="">All Status</option>
            <option value="success">✅ Success</option>
            <option value="failed">❌ Failed</option>
          </select>
        </div>
      </div>

      {/* Logs Table */}
      <div className="card overflow-hidden p-0">
        <div className="overflow-x-auto">
          <table className="w-full">
            <thead className="table-header">
              <tr>
                <th className="table-cell text-left text-xs font-medium text-text-secondary uppercase tracking-wider">
                  Time
                </th>
                <th className="table-cell text-left text-xs font-medium text-text-secondary uppercase tracking-wider">
                  Provider
                </th>
                <th className="table-cell text-left text-xs font-medium text-text-secondary uppercase tracking-wider">
                  API Type
                </th>
                <th className="table-cell text-left text-xs font-medium text-text-secondary uppercase tracking-wider">
                  Model
                </th>
                <th className="table-cell text-left text-xs font-medium text-text-secondary uppercase tracking-wider">
                  Status
                </th>
                <th className="table-cell text-left text-xs font-medium text-text-secondary uppercase tracking-wider">
                  Latency
                </th>
                <th className="table-cell text-left text-xs font-medium text-text-secondary uppercase tracking-wider">
                  Client
                </th>
              </tr>
            </thead>
            <tbody className="divide-y divide-border-light">
              <tr>
                <td colSpan={7} className="px-4 py-16">
                  <div className="empty-state">
                    <div className="w-20 h-20 mx-auto mb-4 bg-bg-tertiary rounded-2xl flex items-center justify-center">
                      <span className="text-4xl">📋</span>
                    </div>
                    <p className="font-medium text-text-primary mb-1">
                      No logs recorded yet
                    </p>
                    <p className="text-sm text-text-muted">
                      Logs will appear here once you start proxying requests.
                    </p>
                  </div>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>

      {/* Pagination */}
      <div className="flex items-center justify-between">
        <p className="text-sm text-text-secondary">
          Showing <span className="font-medium text-text-primary">0</span> of{" "}
          <span className="font-medium text-text-primary">0</span> results
        </p>
        <div className="flex items-center gap-2">
          <button className="btn btn-secondary btn-sm" disabled>
            ← Previous
          </button>
          <div className="flex items-center gap-1">
            <button className="w-9 h-9 rounded-lg bg-primary text-white text-sm font-medium cursor-pointer">
              1
            </button>
          </div>
          <button className="btn btn-secondary btn-sm" disabled>
            Next →
          </button>
        </div>
      </div>

      {/* Log Stats */}
      <div className="grid grid-cols-1 md:grid-cols-4 gap-4">
        <LogStatCard label="Total Requests" value="0" icon="📊" />
        <LogStatCard label="Success Rate" value="0%" icon="✅" />
        <LogStatCard label="Avg Latency" value="0ms" icon="⚡" />
        <LogStatCard label="Errors Today" value="0" icon="⚠️" />
      </div>
    </div>
  );
}

interface LogStatCardProps {
  label: string;
  value: string;
  icon: string;
}

function LogStatCard({ label, value, icon }: LogStatCardProps) {
  return (
    <div className="card py-4">
      <div className="flex items-center gap-3">
        <span className="text-xl">{icon}</span>
        <div>
          <p className="text-xs text-text-muted">{label}</p>
          <p className="text-lg font-bold text-text-primary">{value}</p>
        </div>
      </div>
    </div>
  );
}
