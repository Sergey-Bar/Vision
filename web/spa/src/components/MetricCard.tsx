export function MetricCard({ label, value, sub }: { label: string; value: string | number; sub?: string }) {
  return (
    <div className="card" style={{ padding: 16 }}>
      <div className="text-sm text-muted" style={{ textTransform: 'uppercase', letterSpacing: '0.04em', fontWeight: 600, marginBottom: 8 }}>{label}</div>
      <div style={{ fontSize: 28, fontWeight: 700 }}>{value}</div>
      {sub && <div className="text-sm text-muted mt-2">{sub}</div>}
    </div>
  )
}
