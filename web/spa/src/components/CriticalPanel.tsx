import { useEffect, useState } from 'react'
import { listCritical, CriticalStream } from '../api'

export function CriticalPanel({ limit = 5 }: { limit?: number }) {
  const [streams, setStreams] = useState<CriticalStream[]>([])
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    setLoading(true)
    listCritical(limit)
      .then(setStreams)
      .catch(() => setStreams([]))
      .finally(() => setLoading(false))
  }, [limit])

  if (loading) return <div className="card"><div className="card-title">Critical Streams</div><div className="empty">Loading&hellip;</div></div>
  if (streams.length === 0) {
    return <div className="card"><div className="card-title">Critical Streams</div><div className="empty">No recent stored streams.</div></div>
  }
  return (
    <div className="card">
      <div className="card-title">Critical Streams</div>
      <div className="list">
        {streams.map((s) => (
          <div key={s.incidentId} className="list-item" style={{ cursor: 'pointer' }}>
            <div style={{ flex: 1 }}>
              <div style={{ fontWeight: 600 }}>{s.title}</div>
              <div className="text-sm text-muted">{s.videoId} &middot; {s.severity} &middot; {new Date(s.firstAt).toLocaleTimeString()}</div>
            </div>
            <span className={`badge ${s.severity === 'critical' ? 'badge-critical' : s.severity === 'high' ? 'badge-high' : s.severity === 'medium' ? 'badge-medium' : 'badge-low'}`}>{s.severity}</span>
          </div>
        ))}
      </div>
    </div>
  )
}
