export function Providers() {
  return (
    <div className="space-y-6">
      {/* Page Header */}
      <div className="flex items-center justify-between">
        <div>
          <h2 className="text-2xl font-bold text-text-primary">Providers</h2>
          <p className="text-text-secondary mt-1">管理 AI 供应商配置</p>
        </div>
        <button className="btn btn-primary">
          <span>➕</span>
          Add Provider
        </button>
      </div>

      {/* Filter Bar */}
      <div className="flex items-center gap-3">
        <div className="flex-1 relative">
          <span className="absolute left-3 top-1/2 -translate-y-1/2 text-text-muted">
            🔍
          </span>
          <input
            type="text"
            placeholder="Search providers..."
            className="input pl-10"
          />
        </div>
        <select className="input w-auto">
          <option value="">All Groups</option>
        </select>
        <select className="input w-auto">
          <option value="">All Status</option>
          <option value="healthy">Healthy</option>
          <option value="unhealthy">Unhealthy</option>
          <option value="disabled">Disabled</option>
        </select>
      </div>

      {/* Providers Table */}
      <div className="card overflow-hidden p-0">
        <table className="w-full">
          <thead className="table-header">
            <tr>
              <th className="table-cell text-left text-xs font-medium text-text-secondary uppercase tracking-wider">
                Provider
              </th>
              <th className="table-cell text-left text-xs font-medium text-text-secondary uppercase tracking-wider">
                Group
              </th>
              <th className="table-cell text-left text-xs font-medium text-text-secondary uppercase tracking-wider">
                API Types
              </th>
              <th className="table-cell text-left text-xs font-medium text-text-secondary uppercase tracking-wider">
                Status
              </th>
              <th className="table-cell text-left text-xs font-medium text-text-secondary uppercase tracking-wider">
                Success Rate
              </th>
              <th className="table-cell text-right text-xs font-medium text-text-secondary uppercase tracking-wider">
                Actions
              </th>
            </tr>
          </thead>
          <tbody>
            <tr>
              <td colSpan={6} className="px-4 py-16">
                <div className="empty-state">
                  <div className="w-20 h-20 mx-auto mb-4 bg-bg-tertiary rounded-2xl flex items-center justify-center">
                    <span className="text-4xl">🔌</span>
                  </div>
                  <p className="font-medium text-text-primary mb-1">
                    No providers configured yet
                  </p>
                  <p className="text-sm text-text-muted mb-4">
                    Add your first AI provider to start proxying requests.
                  </p>
                  <button className="btn btn-primary">
                    <span>➕</span>
                    Add Provider
                  </button>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      {/* Help Card */}
      <div className="card bg-primary-light border-primary/20">
        <div className="flex items-start gap-4">
          <div className="w-10 h-10 bg-primary rounded-lg flex items-center justify-center shrink-0">
            <span className="text-white">💡</span>
          </div>
          <div>
            <h4 className="font-semibold text-text-primary">Getting Started</h4>
            <p className="text-sm text-text-secondary mt-1">
              Configure your AI providers with their base URL and API key. You
              can group providers and set up load balancing strategies to
              automatically switch between them.
            </p>
          </div>
        </div>
      </div>
    </div>
  );
}
