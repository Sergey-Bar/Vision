import { useState } from 'react'
import { DashboardHeader } from './components/Header'
import { VideoPlayer } from './components/VideoPlayer'
import { SearchBar } from './components/SearchBar'
import { ChatWidget } from './components/ChatWidget'
import { ClipsPanel } from './components/ClipsPanel'
import { CriticalPanel } from './components/CriticalPanel'
import { EventList } from './components/EventList'

const DEMO_ZONES = [
  { id: 'crosswalk', name: 'Crosswalk', type: 'watch', points: [[0.04, 0.72], [0.82, 0.68], [0.96, 0.92], [0.06, 0.95]] as [number, number][] },
  { id: 'no-zone', name: 'Restricted', type: 'restricted', points: [[0.0, 0.48], [0.24, 0.45], [0.30, 0.74], [0.0, 0.80]] as [number, number][] },
]

export default function App() {
  const [demo, setDemo] = useState(true)
  const [src, setSrc] = useState<string | undefined>(undefined)
  const [searchHits, setSearchHits] = useState<any[]>([])

  return (
    <div className="app">
      <DashboardHeader />
      <div className="shell">
        <aside className="sidebar">
          <div className="brand">
            <div className="brand-mark">V</div>
            <div className="brand-name">Vision VMS</div>
          </div>
          <div style={{ padding: 16 }}>
            <div className="field">
              <label>Source</label>
              <select
                className="input"
                value={demo ? 'demo' : 'file'}
                onChange={(e) => {
                  if (e.target.value === 'demo') {
                    setDemo(true)
                    setSrc(undefined)
                  } else {
                    setDemo(false)
                  }
                }}
              >
                <option value="demo">Demo stream (animated)</option>
                <option value="file">Local file / RTSP</option>
              </select>
            </div>
            {!demo && (
              <div className="field mt-8">
                <label>URL / file path</label>
                <input
                  className="input"
                  placeholder="rtsp://… or /path/to/file.mp4"
                  value={src ?? ''}
                  onChange={(e) => setSrc(e.target.value || undefined)}
                />
              </div>
            )}
            <button className="btn btn-primary w-full mt-8" onClick={() => setDemo(true)}>
              Start Analysis
            </button>
            <hr style={{ border: 0, borderTop: '1px solid var(--color-border)', margin: '24px 0' }} />
            <div style={{ fontSize: 11, color: 'var(--color-text-muted)', lineHeight: 1.5 }}>
              Unified operator console. Live video with detection overlay, free-text
              search, grounded chat, clips and critical streams. Answers cite stored
              incidents and evidence rows.
            </div>
          </div>
        </aside>
        <main className="main">
          <div className="content">
            <div style={{ display: 'flex', flexDirection: 'column', gap: 16, height: '100%', overflowY: 'auto' }}>
              <section style={{ display: 'grid', gridTemplateColumns: '1fr 340px', gap: 16 }}>
                <div>
                  <div className="card">
                    <div className="card-title">Live</div>
                    <div className="video-wrap" style={{ height: 420 }}>
                      <VideoPlayer src={src} detections={[]} zones={DEMO_ZONES} demo={demo} />
                    </div>
                  </div>
                  <SearchBar onResults={setSearchHits} />
                  <EventList hits={searchHits} />
                </div>
                <aside style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
                  <CriticalPanel limit={5} />
                  <ClipsPanel />
                  <ChatWidget />
                </aside>
              </section>
            </div>
          </div>
        </main>
      </div>
    </div>
  )
}
