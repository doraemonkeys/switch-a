export function Groups() {
  return (
    <div className="space-y-6">
      {/* Page Header */}
      <div className="flex items-center justify-between">
        <div>
          <h2 className="text-2xl font-bold text-text-primary">Groups</h2>
          <p className="text-text-secondary mt-1">管理供应商分组</p>
        </div>
        <button className="btn btn-primary">
          <span>➕</span>
          Add Group
        </button>
      </div>

      {/* Groups Grid */}
      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
        {/* Empty State Card */}
        <div className="card border-dashed border-2 hover:border-primary/50 transition-colors cursor-pointer group">
          <div className="empty-state py-8">
            <div className="w-14 h-14 mx-auto mb-3 bg-bg-tertiary rounded-xl flex items-center justify-center 
                          group-hover:bg-primary-light transition-colors">
              <span className="text-2xl group-hover:scale-110 transition-transform">➕</span>
            </div>
            <p className="font-medium text-text-primary">Create New Group</p>
            <p className="text-sm text-text-muted mt-1">
              Organize providers into groups
            </p>
          </div>
        </div>
      </div>

      {/* Strategy Guide */}
      <div className="card">
        <h3 className="text-lg font-semibold text-text-primary mb-4">
          Strategy Guide
        </h3>
        <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
          <StrategyCard
            name="Priority"
            description="Select providers in order of priority. Best for primary/backup setups."
            icon="🎯"
          />
          <StrategyCard
            name="Random"
            description="Randomly select from available providers. Good for basic load balancing."
            icon="🎲"
          />
          <StrategyCard
            name="Weight"
            description="Select based on configured weights. Fine-tune traffic distribution."
            icon="⚖️"
          />
        </div>
      </div>
    </div>
  )
}

interface StrategyCardProps {
  name: string
  description: string
  icon: string
}

function StrategyCard({ name, description, icon }: StrategyCardProps) {
  return (
    <div className="p-4 rounded-xl bg-bg-secondary border border-border-light hover:border-primary/30 transition-colors">
      <div className="flex items-center gap-3 mb-2">
        <span className="text-xl">{icon}</span>
        <h4 className="font-semibold text-text-primary">{name}</h4>
      </div>
      <p className="text-sm text-text-secondary">{description}</p>
    </div>
  )
}
