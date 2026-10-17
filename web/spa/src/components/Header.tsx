export function DashboardHeader() {
  return (
    <div className="header">
      <div style={{ display: 'flex', alignItems: 'center', gap: 16 }}>
        <div className="brand-mark">V</div>
        <div className="brand-name">Vision VMS</div>
      </div>
      <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
        <span className="text-sm text-muted">UTC {new Date().toLocaleTimeString('en-US', { timeZone: 'UTC', hour12: false })}</span>
      </div>
    </div>
  )
}
