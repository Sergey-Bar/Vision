import { useEffect, useState } from 'react'
import { listClips, Clip } from '../api'

interface ClipsPanelProps {
  videoId?: string
}

export function ClipsPanel({ videoId }: ClipsPanelProps) {
  const [clips, setClips] = useState<Clip[]>([])
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    setLoading(true)
    listClips(videoId)
      .then(setClips)
      .catch(() => setClips([]))
      .finally(() => setLoading(false))
  }, [videoId])

  if (loading) return <div className="card"><div className="card-title">Clips</div><div className="empty">Loading&hellip;</div></div>
  if (clips.length === 0) {
    return <div className="card"><div className="card-title">Clips</div><div className="empty">No completed alert clips yet.</div></div>
  }
  return (
    <div className="card">
      <div className="card-title">Recent Clips</div>
      <div className="list">
        {clips.map((c) => (
          <div key={c.evidenceId} className="list-item">
            <a href={c.uri} target="_blank" rel="noopener noreferrer" style={{ color: 'inherit', textDecoration: 'none', flex: 1 }}>
              <div style={{ fontWeight: 600 }}>{new Date(c.observedAt).toLocaleString()}</div>
              <div className="text-sm text-muted">{c.evidenceId.slice(0, 12)} &middot; {(c.sizeBytes ?? 0) / 1024 / 1024}.toFixed(1) MB &middot; {c.sha256?.slice(0, 12)}&hellip;</div>
            </a>
            <span className="badge badge-low">{c.incidentId ? 'incident' : 'clip'}</span>
          </div>
        ))}
      </div>
    </div>
  )
}
