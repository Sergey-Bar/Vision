import { useMemo, useState } from 'react'
import { Button } from '@/components/ui/Button'
import { Panel } from '@/components/ui/Panel'
import {
  Camera,
  Eye,
  HelpCircle,
  MonitorPlay,
  ScanLine,
  Settings,
  Upload,
  X,
} from 'lucide-react'
import { classifyScene, extractYouTubeId, localFileToUrl, type SceneResult } from '@/lib/video'

// The VMS workstation. One screen, one 2×2 video wall, and everything an
// operator on a real VMS does lives on it: load a source, read the wall,
// have the scene analysed, and ask for help or change settings — without
// leaving for another page.

type Cell =
  | { kind: 'empty' }
  | { kind: 'file'; src: string; title: string }
  | { kind: 'youtube'; id: string; title: string }

interface WorksheetState {
  cells: Cell[]
  active: number
  analyzing: number | null
  results: (SceneResult | null)[]
}

const EMPTY_CELLS: Cell[] = [
  { kind: 'empty' },
  { kind: 'empty' },
  { kind: 'empty' },
  { kind: 'empty' },
]

const HELP_SECTIONS = [
  {
    title: 'The wall',
    body: 'Four sources, one glance. Load a local file or a YouTube source into any tile; the wall is what the operator watches while it analyses.',
  },
  {
    title: 'Scene analysis',
    body: 'Analyse a loaded source to classify what the scene contains (person, vehicle, animal, bag). Classification runs through the local detector; when no model runtime is installed the result is labelled SIMULATED and is never presented as measured.',
  },
  {
    title: 'Record & replay',
    body: 'The platform ingests events, records evidenced clips and chains them per device-day, so the recording can be proved months later.',
  },
  {
    title: 'Incidents',
    body: 'A rule engine turns events into incidents with an explanation and a verdict; the operator disposes of them, and every decision is replayed from stored facts.',
  },
  {
    title: 'Audit',
    body: 'Every action is written to an append-only, hash-chained ledger. Editing any row fails checkpoint verification.',
  },
]

export function VmsWorkstation() {
  const [st, setSt] = useState<WorksheetState>({
    cells: EMPTY_CELLS,
    active: 0,
    analyzing: null,
    results: [null, null, null, null],
  })
  const [loadFor, setLoadFor] = useState<number | null>(null)
  const [helpOpen, setHelpOpen] = useState(false)
  const [settingsOpen, setSettingsOpen] = useState(false)
  const [youtubeUrl, setYoutubeUrl] = useState('')
  const [mode, setMode] = useState<'file' | 'youtube'>('file')

  const loadedCount = useMemo(() => st.cells.filter((c) => c.kind !== 'empty').length, [st.cells])

  const setCell = (i: number, cell: Cell) => {
    setSt((s) => {
      const cells = s.cells.slice()
      cells[i] = cell
      return { ...s, cells }
    })
    setLoadFor(null)
    setYoutubeUrl('')
  }

  const analyze = async (i: number) => {
    const cell = st.cells[i]
    if (cell.kind === 'empty') return
    setSt((s) => ({ ...s, analyzing: i }))
    const src = cell.kind === 'file' ? cell.src : `https://www.youtube.com/watch?v=${cell.id}`
    const result = await classifyScene(src, cell.title)
    setSt((s) => {
      const results = s.results.slice()
      results[i] = result
      return { ...s, analyzing: null, results }
    })
  }

  const analyzeAll = () => {
    st.cells.forEach((cell, i) => {
      if (cell.kind !== 'empty') void analyze(i)
    })
  }

  return (
    <div className="flex h-full min-h-0">
      {/* The side rail: the operator's tools. */}
      <aside className="hidden md:flex w-56 flex-shrink-0 flex-col gap-2 border-r border-line bg-bg-panel p-3">
        <div className="flex items-center gap-2 px-2 py-1">
          <MonitorPlay size={16} className="text-accent-ink" aria-hidden="true" />
          <span className="font-mono text-[11px] uppercase tracking-[0.08em] text-text-dim">Workstation</span>
        </div>
        <Button variant="secondary" size="sm" className="w-full justify-start" onClick={() => {
          const i = st.cells.findIndex((c) => c.kind === 'empty')
          setLoadFor(i === -1 ? 0 : i)
        }}>
          <Upload size={14} aria-hidden="true" /> Load video
        </Button>
        <Button variant="secondary" size="sm" className="w-full justify-start" disabled={loadedCount === 0} onClick={analyzeAll}>
          <ScanLine size={14} aria-hidden="true" /> Analyse all
        </Button>
        <div className="mt-auto flex flex-col gap-1 border-t border-line pt-3">
          <Button variant="ghost" size="sm" className="w-full justify-start" onClick={() => setHelpOpen(true)}>
            <HelpCircle size={14} aria-hidden="true" /> Help
          </Button>
          <Button variant="ghost" size="sm" className="w-full justify-start" onClick={() => setSettingsOpen(true)}>
            <Settings size={14} aria-hidden="true" /> Settings
          </Button>
        </div>
      </aside>

      {/* The wall. */}
      <div className="min-w-0 flex-1 overflow-y-auto p-5">
        <div className="mb-4 flex flex-wrap items-end justify-between gap-3">
          <div>
            <p className="font-mono text-[11px] uppercase tracking-[0.08em] text-text-dim">
              {loadedCount} of 4 sources {st.analyzing !== null ? '· analysing…' : ''}
            </p>
            <h1 className="mt-1 text-[28px] font-semibold tracking-tight text-text outline-none">VMS Workstation</h1>
          </div>
          <div className="flex items-center gap-2 text-[12px] text-text-muted">
            <Eye size={14} className="text-text-dim" aria-hidden="true" />
            <span>2×2 wall</span>
          </div>
        </div>

        <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
          {st.cells.map((cell, i) => (
            <Tile
              key={i}
              cell={cell}
              analyzing={st.analyzing === i}
              result={st.results[i]}
              onLoad={() => setLoadFor(i)}
              onAnalyze={() => void analyze(i)}
              onReplace={() => setLoadFor(i)}
            />
          ))}
        </div>

        {st.analyzing !== null && (
          <p className="mt-4 text-[13px] text-text-muted" role="status">
            Analysing the scene…
          </p>
        )}
      </div>

      {loadFor !== null && (
        <div className="fixed inset-0 z-50 flex items-center justify-center p-4">
          <button
            aria-label="Close"
            onClick={() => setLoadFor(null)}
            className="absolute inset-0 cursor-default bg-bg/80"
          />
          <Panel className="relative w-full max-w-md" title="Load a video source">
            <div className="flex gap-2">
              <Button variant={mode === 'file' ? 'primary' : 'secondary'} size="sm" onClick={() => setMode('file')}>
                Local file
              </Button>
              <Button variant={mode === 'youtube' ? 'primary' : 'secondary'} size="sm" onClick={() => setMode('youtube')}>
                YouTube
              </Button>
            </div>
            {mode === 'file' ? (
              <div className="mt-4">
                <input
                  type="file"
                  accept="video/*"
                  aria-label="Choose a video file"
                  className="block w-full text-[13px] text-text-dim file:mr-3 file:rounded file:border file:border-line file:bg-bg-elevated file:px-3 file:py-1.5 file:text-[13px] file:text-text"
                  onChange={(e) => {
                    const f = e.target.files?.[0]
                    if (f) setCell(loadFor, { kind: 'file', src: localFileToUrl(f), title: f.name })
                  }}
                />
              </div>
            ) : (
              <div className="mt-4 flex gap-2">
                <input
                  value={youtubeUrl}
                  onChange={(e) => setYoutubeUrl(e.target.value)}
                  placeholder="https://www.youtube.com/watch?v=…"
                  aria-label="YouTube URL"
                  className="min-w-0 flex-1 rounded border border-line bg-bg-elevated px-3 py-2 text-[13px] text-text placeholder:text-text-dim"
                />
                <Button
                  variant="primary"
                  size="sm"
                  disabled={!extractYouTubeId(youtubeUrl)}
                  onClick={() => {
                    const id = extractYouTubeId(youtubeUrl)
                    if (id) setCell(loadFor, { kind: 'youtube', id, title: `YouTube ${id}` })
                  }}
                >
                  Load
                </Button>
              </div>
            )}
          </Panel>
        </div>
      )}

      {helpOpen && (
        <Popup title="Help" onClose={() => setHelpOpen(false)}>
          <div className="space-y-4">
            {HELP_SECTIONS.map((s) => (
              <div key={s.title}>
                <p className="text-[14px] font-semibold text-text">{s.title}</p>
                <p className="mt-1 text-[13px] leading-[20px] text-text-muted">{s.body}</p>
              </div>
            ))}
          </div>
        </Popup>
      )}

      {settingsOpen && (
        <Popup title="Settings" onClose={() => setSettingsOpen(false)}>
          <div className="space-y-3 text-[13px]">
            <p className="text-[13px] leading-[20px] text-text-muted">
              The console holds no state of its own. Settings here are session-scoped
              display preferences; product configuration lives in the site and the
              deployment, not in the browser.
            </p>
            <div className="flex items-center justify-between border-t border-line pt-3">
              <span className="text-text">2×2 wall</span>
              <span className="font-mono text-[12px] text-text-dim">fixed</span>
            </div>
          </div>
        </Popup>
      )}
    </div>
  )
}

function Tile({
  cell,
  analyzing,
  result,
  onLoad,
  onAnalyze,
  onReplace,
}: {
  cell: Cell
  analyzing: boolean
  result: SceneResult | null
  onLoad: () => void
  onAnalyze: () => void
  onReplace: () => void
}) {
  return (
    <div className="group relative aspect-video overflow-hidden rounded-lg border border-line bg-bg-panel">
      {cell.kind === 'empty' ? (
        <button
          onClick={onLoad}
          className="flex h-full w-full flex-col items-center justify-center gap-2 text-text-dim hover:text-text transition-colors duration-1 ease-out"
        >
          <Camera size={26} aria-hidden="true" />
          <span className="text-[13px] font-medium">Load video</span>
        </button>
      ) : cell.kind === 'file' ? (
        <video src={cell.src} controls playsInline className="h-full w-full object-contain">
          <track kind="captions" />
        </video>
      ) : (
        <iframe
          src={`https://www.youtube.com/embed/${cell.id}`}
          title={cell.title}
          className="h-full w-full"
          allow="accelerometer; autoplay; clipboard-write; encrypted-media; gyroscope; picture-in-picture; web-share"
          allowFullScreen
        />
      )}

      {cell.kind !== 'empty' && (
        <div className="absolute inset-x-0 bottom-0 flex items-center justify-between gap-2 bg-bg/80 px-3 py-2 backdrop-blur-sm">
          <span className="truncate font-mono text-[11px] text-text-muted">{cell.title}</span>
          <div className="flex items-center gap-2">
            <Button variant="secondary" size="sm" onClick={onReplace}>
              Replace
            </Button>
            <Button variant="primary" size="sm" disabled={analyzing} onClick={onAnalyze}>
              {analyzing ? 'Analysing…' : 'Analyse'}
            </Button>
          </div>
        </div>
      )}

      {result && result.simulated && (
        <div className="absolute left-3 top-3 flex items-center gap-1.5 rounded border border-warn-ink/40 bg-warn-ink/10 px-2 py-1 text-[10px] font-medium text-warn-ink">
          <ScanLine size={12} aria-hidden="true" />
          SIMULATED · {result.detections.length} object{result.detections.length === 1 ? '' : 's'}
        </div>
      )}

      {result && (
        <div className="absolute inset-0 pointer-events-none">
          {result.detections.map((d, i) => (
            <div
              key={i}
              className="absolute border border-accent-ink/80"
              style={{ left: `${d.box.x}%`, top: `${d.box.y}%`, width: `${d.box.w}%`, height: `${d.box.h}%` }}
            >
              <span className="absolute -top-5 left-0 bg-accent text-accent-fg px-1.5 py-0.5 font-mono text-[10px] font-medium">
                {d.className} {Math.round(d.confidence * 100)}%
              </span>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

function Popup({ title, onClose, children }: { title: string; onClose: () => void; children: React.ReactNode }) {
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4">
      <button
        aria-label="Close"
        onClick={onClose}
        className="absolute inset-0 cursor-default bg-bg/80"
      />
      <Panel className="relative w-full max-w-lg max-h-[85vh] overflow-y-auto" title={title} action={
        <button onClick={onClose} aria-label="Close" className="rounded p-1 text-text-muted hover:text-text hover:bg-bg-elevated">
          <X size={16} />
        </button>
      }>
        {children}
      </Panel>
    </div>
  )
}
