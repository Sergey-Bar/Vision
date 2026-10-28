import { useEffect, useRef } from 'react'
import { MoreHorizontal, X } from 'lucide-react'
import { cn } from '@/lib/utils'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from './ui/dropdown-menu'
import { WORKSPACE_VIEWS } from './workspace'

// The rail is one screen. Its buttons switch the panel that fills the screen;
// they do not navigate anywhere. Six primaries, then a single "more" menu for
// the long tail.
const PRIMARY = ['wall', 'incidents', 'clips', 'recording', 'devices', 'chain'] as const
const MORE = ['operations', 'site', 'setup', 'ingest', 'rules', 'ops', 'audit', 'users', 'demo', 'settings'] as const

export function Sidebar({
  view,
  onNavigate,
  open,
  onClose,
}: {
  view: string
  onNavigate: (id: string) => void
  open: boolean
  onClose: () => void
}) {
  const panel = useRef<HTMLElement | null>(null)

  useEffect(() => {
    if (!open) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        onClose()
        return
      }
      if (e.key !== 'Tab' || !panel.current) return
      const focusable = panel.current.querySelectorAll<HTMLElement>(
        'a[href], button:not([disabled]), input, select, textarea, [tabindex]:not([tabindex="-1"])',
      )
      if (focusable.length === 0) return
      const first = focusable[0]
      const last = focusable[focusable.length - 1]
      if (e.shiftKey && document.activeElement === first) {
        e.preventDefault()
        last.focus()
      } else if (!e.shiftKey && document.activeElement === last) {
        e.preventDefault()
        first.focus()
      }
    }
    document.addEventListener('keydown', onKey)
    const first = panel.current?.querySelector<HTMLElement>('button')
    first?.focus()
    return () => document.removeEventListener('keydown', onKey)
  }, [open, onClose])

  const pick = (id: string) => {
    onNavigate(id)
    onClose()
  }

  const itemClass = (active: boolean) =>
    cn(
      'group relative flex h-11 w-11 items-center justify-center rounded-md border-l-2 transition-colors duration-1 ease-out',
      active
        ? 'border-accent text-accent-ink bg-bg-elevated'
        : 'border-transparent text-text-dim hover:text-text hover:bg-bg-elevated/70',
    )

  return (
    <>
      {open && (
        <div className="fixed inset-0 z-40 bg-bg lg:hidden" onClick={onClose} aria-hidden="true" />
      )}
      <aside
        ref={panel}
        aria-label="Console navigation"
        className={cn(
          'flex-shrink-0 border-r border-line bg-bg z-50 transition-transform duration-3 ease-out',
          'fixed inset-y-0 left-0 lg:static lg:translate-x-0',
          open ? 'translate-x-0' : '-translate-x-full',
          'w-16',
        )}
      >
        <nav className="flex h-full flex-col items-center overflow-y-auto py-4 gap-1">
          {PRIMARY.map((id) => {
            const v = WORKSPACE_VIEWS.find((w) => w.id === id)
            if (!v) return null
            const Icon = v.icon
            const active = view === id
            return (
              <button
                key={id}
                onClick={() => pick(id)}
                title={v.label}
                aria-label={v.label}
                aria-current={active ? 'page' : undefined}
                className={itemClass(active)}
              >
                <Icon size={18} aria-hidden="true" />
              </button>
            )
          })}

          <div className="mt-auto mb-1 w-full flex justify-center">
            <DropdownMenu>
              <DropdownMenuTrigger
                title="More"
                aria-label="More"
                className={itemClass(false)}
              >
                <MoreHorizontal size={18} aria-hidden="true" />
              </DropdownMenuTrigger>
              <DropdownMenuContent align="start" side="right" className="w-56 rounded-lg border border-line bg-bg-panel p-1.5 z-50">
                {MORE.map((id, i) => {
                  const v = WORKSPACE_VIEWS.find((w) => w.id === id)
                  if (!v) return null
                  const Icon = v.icon
                  const active = view === id
                  return (
                    <span key={id}>
                      {i === 4 && <DropdownMenuSeparator className="my-1 h-[1px] bg-line" />}
                      <DropdownMenuItem asChild>
                        <button
                          onClick={() => pick(id)}
                          className={cn(
                            'flex w-full items-center gap-2.5 rounded px-2.5 py-2 text-left text-[13px] transition-colors duration-1 ease-out',
                            active ? 'bg-bg-elevated text-text' : 'text-text-muted hover:text-text hover:bg-bg-elevated/70',
                          )}
                        >
                          <Icon size={15} className={cn('shrink-0', active ? 'text-accent-ink' : 'text-text-dim')} aria-hidden="true" />
                          <span className="font-medium">{v.label}</span>
                        </button>
                      </DropdownMenuItem>
                    </span>
                  )
                })}
              </DropdownMenuContent>
            </DropdownMenu>
          </div>

          <a
            href="/"
            title="The showcase"
            aria-label="The showcase"
            className="flex h-11 w-11 items-center justify-center rounded-md text-text-dim hover:text-text transition-colors duration-1 ease-out"
          >
            <span aria-hidden="true">←</span>
          </a>

          <button
            onClick={onClose}
            className="lg:hidden absolute top-3 right-3 p-2 text-text-muted hover:text-text"
            aria-label="Close navigation"
          >
            <X size={18} />
          </button>
        </nav>
      </aside>
    </>
  )
}
