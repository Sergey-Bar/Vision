import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from 'react'
import { useNavigate } from 'react-router-dom'
import { Activity, CornerDownLeft, Search } from 'lucide-react'
import { cn } from '@/lib/utils'
import { describeError } from '@/lib/errors'
import { useEvidenceSearch } from '@/hooks/product'

// The command palette.
//
// ONE search. The console used to have two: the Cockpit's panel and the
// Events page's box, over two different queries, with two different empty
// states and two different histories. An operator who searched twice searched
// twice, and the two answers did not have to agree.
//
// The palette is over ONE index -- the product's structured search over the
// canonical event record -- and it is opened three ways: ⌘K from anywhere, `/`
// from anywhere, and the always-visible input in the column. The results are
// the record's moments, and the commands are the places the record can be
// read.
//
// It is a MODAL over the page, not a region of it. A palette that is a
// region is a panel that takes a third of the screen for a thing you use for
// four seconds.

interface PaletteContextValue {
  open: () => void
  close: () => void
  isOpen: boolean
}

const PaletteContext = createContext<PaletteContextValue | null>(null)

/** usePalette opens the command palette. */
export function usePalette(): PaletteContextValue {
  const ctx = useContext(PaletteContext)
  if (!ctx) {
    throw new Error('usePalette was called outside a CommandPaletteProvider')
  }
  return ctx
}

// A command is a place the operator can go, or a thing they can do.
//
// The list is CLOSED and it is here rather than in the route table, because a
// palette that discovers its commands by walking the router is a palette whose
// commands change with the tree, and a command list that changes is a command
// list nobody learns.
const COMMANDS: { id: string; label: string; hint: string; to: string }[] = [
  { id: 'ops', label: 'The operations page', hint: 'the strip, the wall and the column', to: '/console' },
  { id: 'wall', label: 'The camera wall', hint: 'what the VMS is looking at', to: '/console/live' },
  { id: 'record', label: 'The record', hint: 'every event, newest first', to: '/console/events' },
  { id: 'incidents', label: 'Incidents', hint: 'every incident the product raised', to: '/console/incidents' },
  { id: 'clips', label: 'Clips', hint: 'the evidence the product kept', to: '/console/clips' },
  { id: 'site', label: 'The site', hint: 'zones, coverage and hand-over', to: '/console/site' },
  { id: 'rules', label: 'The rule bundles', hint: 'what the rules are, and which is running', to: '/console/rules' },
  { id: 'ops-plane', label: 'Operations', hint: 'dead letters, clock drift, readiness', to: '/console/ops' },
  { id: 'audit', label: 'The audit ledger', hint: 'the hash-chained record', to: '/console/audit' },
  { id: 'settings', label: 'Settings', hint: 'the configuration of this install', to: '/console/settings' },
]

/**
 * CommandPaletteProvider holds the palette and renders it.
 *
 * It is a provider rather than a component the pages import, because the
 * palette is opened from the top bar AND from inside the page, and the two
 * must open the SAME palette: two palettes are two searches.
 */
export function CommandPaletteProvider({ children }: { children: ReactNode }) {
  const [isOpen, setIsOpen] = useState(false)
  const navigate = useNavigate()

  const open = useCallback(() => setIsOpen(true), [])
  const close = useCallback(() => setIsOpen(false), [])

  const value = useMemo<PaletteContextValue>(
    () => ({ open, close, isOpen }),
    [open, close, isOpen],
  )

  return (
    <PaletteContext.Provider value={value}>
      {children}
      {isOpen && (
        <Palette
          onClose={close}
          onNavigate={(to) => {
            close()
            void navigate(to)
          }}
        />
      )}
    </PaletteContext.Provider>
  )
}

function Palette({ onClose, onNavigate }: { onClose: () => void; onNavigate: (to: string) => void }) {
  const [query, setQuery] = useState('')
  const [cursor, setCursor] = useState(0)
  const input = useRef<HTMLInputElement | null>(null)
  const list = useRef<HTMLUListElement | null>(null)

  // The record's answer to the question, and only when there IS a question.
  const trimmed = query.trim()
  const search = useEvidenceSearch(trimmed)

  const commands = useMemo(() => {
    if (trimmed === '') return COMMANDS
    const q = trimmed.toLowerCase()
    return COMMANDS.filter(
      (c) =>
        c.label.toLowerCase().includes(q) ||
        c.hint.toLowerCase().includes(q) ||
        c.id.includes(q),
    )
  }, [trimmed])

  const hits = search.data ?? []

  // The results are the record's moments first when there are any, because a
  // question about a moment is the question the operator is asking; the
  // commands are the fallback for a question that is really a question about
  // where to look.
  const total = hits.length + commands.length
  useEffect(() => {
    setCursor(0)
  }, [trimmed])

  useEffect(() => {
    input.current?.focus()
  }, [])

  // Escape closes it, and the page behind it does not scroll while it is
  // open.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.stopPropagation()
        onClose()
      }
    }
    document.addEventListener('keydown', onKey)
    const previous = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    return () => {
      document.removeEventListener('keydown', onKey)
      document.body.style.overflow = previous
    }
  }, [onClose])

  const move = (delta: number) => {
    if (total === 0) return
    setCursor((c) => (c + delta + total) % total)
  }

  const openSelected = () => {
    if (cursor < hits.length) {
      const hit = hits[cursor]
      if (hit.incidentId) onNavigate(`/console/incidents/${encodeURIComponent(hit.incidentId)}`)
      else onClose()
      return
    }
    const command = commands[cursor - hits.length]
    if (command) onNavigate(command.to)
  }

  const onKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'ArrowDown') {
      e.preventDefault()
      move(1)
    } else if (e.key === 'ArrowUp') {
      e.preventDefault()
      move(-1)
    } else if (e.key === 'Enter') {
      e.preventDefault()
      openSelected()
    }
  }

  // The selected row is scrolled into view, because a cursor that walks off
  // the list is a cursor the reader has lost.
  useEffect(() => {
    const node = list.current?.querySelector<HTMLElement>('[data-selected="true"]')
    node?.scrollIntoView({ block: 'nearest' })
  }, [cursor])

  return (
    <div className="fixed inset-0 z-50 flex items-start justify-center bg-black/70 px-4 pt-[12vh]">
      {/* The backdrop is a BUTTON and not a click handler on the overlay.
          A div with a click handler is not reachable by keyboard, so a
          reader who cannot see the screen cannot dismiss the search. */}
      <button
        type="button"
        aria-label="Close the search"
        onClick={onClose}
        className="absolute inset-0 cursor-default"
      />
      <div
        className="relative w-full max-w-[720px] overflow-hidden rounded-lg border border-line bg-bg-panel"
        role="dialog"
        aria-modal="true"
        aria-label="Search the record"
      >
        <div className="flex items-center gap-3 border-b border-line px-5 py-4">
          <Search size={18} className="shrink-0 text-text-dim" aria-hidden="true" />
          <input
            ref={input}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={onKeyDown}
            placeholder="Who was carrying a bag near the entrance after 14:00?"
            aria-label="Search the record"
            className="flex-1 bg-transparent text-[14px] leading-[20px] text-text outline-none placeholder:text-text-dim"
          />
          <kbd className="shrink-0 rounded border border-line px-2 py-0.5 font-mono text-[10px] text-text-dim">
            ESC
          </kbd>
        </div>

        <ul ref={list} className="max-h-[52vh] overflow-y-auto py-2">
          {trimmed !== '' && search.isError && (
            <li className="px-5 py-4 text-[13px] text-bad-ink">
              <span aria-hidden="true">✕ </span>
              The record did not answer: {describeError(search.error)}
            </li>
          )}
          {trimmed !== '' && search.isPending && (
            <li className="px-5 py-4 text-[13px] text-text-dim">Asking the record…</li>
          )}
          {trimmed !== '' && !search.isPending && !search.isError && hits.length === 0 && (
            <li className="px-5 py-4 text-[13px] leading-[20px] text-text-dim">
              The record holds no moment that answers that. The deterministic path searches the
              canonical event record; it does not guess.
            </li>
          )}

          {hits.map((h, i) => (
            <li key={h.eventId ?? i}>
              <button
                type="button"
                data-selected={i === cursor}
                onMouseEnter={() => setCursor(i)}
                onClick={() => {
                  if (h.incidentId) onNavigate(`/console/incidents/${encodeURIComponent(h.incidentId)}`)
                  else onClose()
                }}
                className={cn(
                  'flex w-full items-center gap-3 px-5 py-2.5 text-left transition-colors duration-1 ease-out',
                  i === cursor ? 'bg-bg-elevated' : 'hover:bg-bg-elevated/60',
                )}
              >
                <Activity size={14} className="shrink-0 text-text-dim" aria-hidden="true" />
                <span className="min-w-0 flex-1 truncate text-[13px] text-text">{h.eventType}</span>
                <span className="min-w-0 flex-1 truncate text-[12px] text-text-dim">
                  {h.deviceId ?? h.siteId ?? ''}
                </span>
                {h.normalizedAt && (
                  <span className="shrink-0 text-[11px] tabular-nums text-text-dim">
                    {new Date(h.normalizedAt).toISOString().slice(11, 19)}
                  </span>
                )}
              </button>
            </li>
          ))}

          {commands.length > 0 && (
            <li className="px-5 pb-1 pt-3 font-mono text-[10px] font-medium uppercase tracking-[0.08em] text-text-dim">
              {hits.length > 0 ? 'Also' : 'Go to'}
            </li>
          )}
          {commands.map((c, i) => {
            const index = hits.length + i
            return (
              <li key={c.id}>
                <button
                  type="button"
                  data-selected={index === cursor}
                  onMouseEnter={() => setCursor(index)}
                  onClick={() => onNavigate(c.to)}
                  className={cn(
                    'flex w-full items-center gap-3 px-5 py-2.5 text-left transition-colors duration-1 ease-out',
                    index === cursor ? 'bg-bg-elevated' : 'hover:bg-bg-elevated/60',
                  )}
                >
                  <CornerDownLeft size={14} className="shrink-0 text-text-dim" aria-hidden="true" />
                  <span className="min-w-0 flex-1 truncate text-[13px] text-text">{c.label}</span>
                  <span className="min-w-0 flex-1 truncate text-[12px] text-text-dim">{c.hint}</span>
                </button>
              </li>
            )
          })}
        </ul>

        <div className="flex items-center gap-4 border-t border-line px-5 py-2.5 text-[11px] text-text-dim">
          <span>↑↓ to move</span>
          <span>⏎ to open</span>
          <span>esc to close</span>
        </div>
      </div>
    </div>
  )
}
