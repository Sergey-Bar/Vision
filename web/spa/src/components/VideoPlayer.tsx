import { useEffect, useRef, useState } from 'react'

interface Detection {
  id: string
  label: string
  category: string
  color?: string
  vehicleType?: string
  confidence: number
  bbox: [number, number, number, number]
  zone: string
  violation: boolean
}

interface Zone {
  id: string
  name: string
  type: string
  points: [number, number][]
}

interface VideoPlayerProps {
  src?: string
  detections: Detection[]
  zones: Zone[]
  demo?: boolean
}

const BOX_COLORS: Record<string, string> = {
  vehicle: '#22d3ee',
  biological: '#22c55e',
  other: '#f59e0b',
}

export function VideoPlayer({ src, detections, zones, demo }: VideoPlayerProps) {
  const wrapRef = useRef<HTMLDivElement>(null)
  const [dim, setDim] = useState({ w: 1, h: 1 })

  useEffect(() => {
    const el = wrapRef.current
    if (!el) return
    const ro = new ResizeObserver((entries) => {
      const r = entries[0]
      const cr = r.contentRect
      setDim({ w: cr.width, h: cr.height })
    })
    ro.observe(el)
    return () => ro.disconnect()
  }, [])

  const boxStyle = (d: Detection) => {
    const [x, y, w, h] = d.bbox
    const color = d.violation
      ? '#ef4444'
      : BOX_COLORS[d.category] ?? '#f59e0b'
    return {
      position: 'absolute' as const,
      left: `${(x * dim.w) * 100}%`,
      top: `${(y * dim.h) * 100}%`,
      width: `${w * dim.w * 100}%`,
      height: `${h * dim.h * 100}%`,
      border: `2px solid ${color}`,
      borderRadius: '4px',
      color,
      background: 'rgba(0,0,0,0.5)',
      padding: '1px 6px',
      fontSize: '11px',
      fontWeight: 700,
      whiteSpace: 'nowrap' as const,
      pointerEvents: 'none' as React.CSSProperties['pointerEvents'],
      zIndex: 10,
    }
  }

  const zoneStyle = (z: Zone) => {
    const color = z.type === 'restricted' ? '#ef4444' : '#22d3ee'
    return {
      fill: color + '22',
      stroke: color,
      strokeWidth: '2',
    }
  }

  return (
    <div className="video-wrap" ref={wrapRef}>
      {demo && (
        <span className="demo-badge">DEMO</span>
      )}
      {src ? (
        <video
          className="video"
          autoPlay
          muted
          playsInline
          src={src}
          poster=""
        />
      ) : (
        <div className="empty">No video source</div>
      )}
      <div className="overlay">
        {zones.map((z) => (
          <svg
            key={z.id}
            className="absolute inset-0 w-full h-full"
            style={{ pointerEvents: 'none', zIndex: 5 }}
          >
            <polygon
              points={z.points.map((p) => `${p[0] * 100},${p[1] * 100}`).join(' ')}
              style={zoneStyle(z)}
            />
          </svg>
        ))}
        {detections.map((d) => (
          <div key={d.id} className="box" style={boxStyle(d)}>
            {d.label} #{d.id.slice(-4)} &middot; {(d.confidence * 100).toFixed(0)}%
          </div>
        ))}
      </div>
    </div>
  )
}
