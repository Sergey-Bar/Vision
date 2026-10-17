export function EventList({ hits }: { hits: any[] }) {
  if (hits.length === 0) {
    return (
      <div className="card">
        <div className="card-title">Events</div>
        <div className="empty">No matching events.</div>
      </div>
    )
  }
  return (
    <div className="card">
      <div className="card-title">Events</div>
      <div className="list">
        {hits.map((h) => (
          <div key={h.eventId} className="list-item">
            <div style={{ flex: 1 }}>
              <div style={{ fontWeight: 600 }}>{h.eventType}</div>
              <div className="text-sm text-muted">{h.deviceId ?? h.zoneId ?? '—'} &middot; {new Date(h.observedAt).toLocaleString()}</div>
            </div>
            {h.incidentId && <span className="badge badge-low">{h.incidentId.slice(0, 8)}</span>}
          </div>
        ))}
      </div>
    </div>
  )
}
