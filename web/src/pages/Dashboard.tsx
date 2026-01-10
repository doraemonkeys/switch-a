export function Dashboard() {
  return (
    <div className="space-y-6">
      {/* Page Header */}
      <div className="flex items-center justify-between">
        <div>
          <h2 className="text-2xl font-bold text-text-primary">Dashboard</h2>
          <p className="text-text-secondary mt-1">系统状态总览</p>
        </div>
        <button className="btn btn-secondary btn-sm">
          <span>🔄</span>
          Refresh
        </button>
      </div>

      {/* Stats Grid */}
      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-4">
        <StatCard
          title="Total Providers"
          value="0"
          icon="🔌"
          trend={{ value: 0, label: 'configured' }}
          variant="primary"
        />
        <StatCard
          title="Healthy"
          value="0"
          icon="✅"
          trend={{ value: 0, label: 'available' }}
          variant="success"
        />
        <StatCard
          title="Unhealthy"
          value="0"
          icon="⚠️"
          trend={{ value: 0, label: 'circuit breaker' }}
          variant="danger"
        />
        <StatCard
          title="Requests Today"
          value="0"
          icon="📈"
          trend={{ value: 0, label: 'total requests' }}
          variant="info"
        />
      </div>

      {/* Main Content Grid */}
      <div className="grid grid-cols-1 lg:grid-cols-3 gap-6">
        {/* Providers Status - Takes 2 columns */}
        <div className="lg:col-span-2 card">
          <div className="flex items-center justify-between mb-4">
            <h3 className="text-lg font-semibold text-text-primary">
              Provider Status
            </h3>
            <span className="badge badge-neutral">0 providers</span>
          </div>
          <div className="empty-state">
            <div className="w-16 h-16 mx-auto mb-4 bg-bg-tertiary rounded-full flex items-center justify-center">
              <span className="text-3xl">🔌</span>
            </div>
            <p className="font-medium text-text-primary mb-1">No providers configured</p>
            <p className="text-sm text-text-muted">
              Go to Providers page to add your first provider.
            </p>
            <button className="btn btn-primary btn-sm mt-4">
              + Add Provider
            </button>
          </div>
        </div>

        {/* Quick Actions */}
        <div className="card">
          <h3 className="text-lg font-semibold text-text-primary mb-4">
            Quick Actions
          </h3>
          <div className="space-y-2">
            <QuickActionButton icon="➕" label="Add Provider" />
            <QuickActionButton icon="📁" label="Create Group" />
            <QuickActionButton icon="⚙️" label="Edit Config" />
            <QuickActionButton icon="📋" label="View Logs" />
          </div>
        </div>
      </div>

      {/* Recent Errors */}
      <div className="card">
        <div className="flex items-center justify-between mb-4">
          <h3 className="text-lg font-semibold text-text-primary">
            Recent Errors
          </h3>
          <button className="text-sm text-primary hover:text-primary-hover font-medium">
            View All →
          </button>
        </div>
        <div className="empty-state py-8">
          <div className="w-12 h-12 mx-auto mb-3 bg-success-light rounded-full flex items-center justify-center">
            <span className="text-2xl">✨</span>
          </div>
          <p className="text-sm">No errors recorded. Everything is running smoothly!</p>
        </div>
      </div>
    </div>
  )
}

interface StatCardProps {
  title: string
  value: string
  icon: string
  trend: { value: number; label: string }
  variant: 'primary' | 'success' | 'danger' | 'info'
}

function StatCard({ title, value, icon, trend, variant }: StatCardProps) {
  const variantStyles = {
    primary: {
      bg: 'bg-primary-light',
      icon: 'text-primary',
      accent: 'bg-primary',
    },
    success: {
      bg: 'bg-success-light',
      icon: 'text-emerald-600',
      accent: 'bg-success',
    },
    danger: {
      bg: 'bg-danger-light',
      icon: 'text-red-600',
      accent: 'bg-danger',
    },
    info: {
      bg: 'bg-info-light',
      icon: 'text-blue-600',
      accent: 'bg-info',
    },
  }

  const styles = variantStyles[variant]

  return (
    <div className="card relative overflow-hidden">
      {/* Accent bar */}
      <div className={`absolute top-0 left-0 w-full h-1 ${styles.accent}`} />
      
      <div className="flex items-start justify-between">
        <div>
          <p className="text-sm font-medium text-text-secondary">{title}</p>
          <p className="text-3xl font-bold text-text-primary mt-1">{value}</p>
          <p className="text-xs text-text-muted mt-2">{trend.label}</p>
        </div>
        <div className={`w-12 h-12 rounded-xl flex items-center justify-center ${styles.bg}`}>
          <span className={`text-xl ${styles.icon}`}>{icon}</span>
        </div>
      </div>
    </div>
  )
}

interface QuickActionButtonProps {
  icon: string
  label: string
}

function QuickActionButton({ icon, label }: QuickActionButtonProps) {
  return (
    <button className="w-full flex items-center gap-3 px-4 py-3 rounded-lg border border-border 
                       hover:bg-bg-hover hover:border-primary/30 transition-all duration-200 group">
      <span className="text-lg group-hover:scale-110 transition-transform">{icon}</span>
      <span className="text-sm font-medium text-text-primary">{label}</span>
      <span className="ml-auto text-text-muted group-hover:text-primary transition-colors">→</span>
    </button>
  )
}
