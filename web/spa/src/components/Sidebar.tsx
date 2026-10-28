import { useEffect, useRef } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import {
  Camera,
  ClipboardList,
  Eye,
  Film,
  Gauge,
  ListChecks,
  MapPin,
  MoreHorizontal,
  Network,
  ScrollText,
  Settings,
  ShieldCheck,
  Upload,
  Wrench,
  X,
  Zap,
  type LucideIcon,
} from 'lucide-react'
import { cn } from '@/lib/utils'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from './ui/dropdown-menu'

// One entry in the rail.
interface NavItem {
  href: string
  label: string
  icon: LucideIcon
  paths?: string[]
}

// The unified rail. A real VMS workstation has a small set of primary
// destinations and folds everything else behind a single "more" menu, rather
// than a long list of groups. Six primaries, then a menu for the long tail.
const PRIMARY: NavItem[] = [
  { href: '/', paths: ['/', '/live', '/events'], label: 'Operations', icon: Gauge },
  { href: '/incidents', label: 'Incidents', icon: ClipboardList },
  { href: '/clips', label: 'Clips', icon: Eye },
  { href: '/recordings', label: 'Recording', icon: Film },
  { href: '/devices', label: 'Devices', icon: Camera },
  { href: '/chain', label: 'Chain', icon: Network },
]

const MORE: NavItem[] = [
  { href: '/site', label: 'Site', icon: MapPin },
  { href: '/setup', label: 'Setup', icon: ListChecks },
  { href: '/upload', label: 'Ingest', icon: Upload },
  { href: '/rules', label: 'Rules', icon: ClipboardList },
  { href: '/ops', label: 'Ops', icon: Wrench },
  { href: '/audit', label: 'Audit', icon: ScrollText },
  { href: '/users', label: 'Users', icon: ShieldCheck },
  { href: '/settings', label: 'Settings', icon: Settings },
  { href: '/demo-hafnia', label: 'Hafnia demo', icon: Zap },
]

function isActive(item: NavItem, currentPath: string): boolean {
  const paths = item.paths ?? [item.href]
  return paths.some(
    (p) =>
      currentPath === p ||
      currentPath === p + '/' ||
      (p !== '/' && currentPath.startsWith(p + '/')),
  )
}

export function Sidebar({
  currentPath,
  open,
  onClose,
}: {
  currentPath: string
  open: boolean
  onClose: () => void
}) {
  const navigate = useNavigate()
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
    const first = panel.current?.querySelector<HTMLElement>('a[href]')
    first?.focus()
    return () => document.removeEventListener('keydown', onKey)
  }, [open, onClose])

  const go = (href: string) => {
    void navigate(href)
    onClose()
  }

  return (
    <>
      {open && (
        <div
          className="fixed inset-0 z-40 bg-bg lg:hidden"
          onClick={onClose}
          aria-hidden="true"
        />
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
          {PRIMARY.map((item) => {
            const Icon = item.icon
            const active = isActive(item, currentPath)
            return (
              <Link
                key={item.href}
                to={item.href}
                onClick={() => go(item.href)}
                title={item.label}
                aria-label={item.label}
                aria-current={active ? 'page' : undefined}
                className={cn(
                  'group relative flex h-11 w-11 items-center justify-center rounded-md border-l-2 transition-colors duration-1 ease-out',
                  active
                    ? 'border-accent text-accent-ink bg-bg-elevated'
                    : 'border-transparent text-text-dim hover:text-text hover:bg-bg-elevated/70',
                )}
              >
                <Icon size={18} aria-hidden="true" />
              </Link>
            )
          })}

          <div className="mt-auto mb-1 w-full flex justify-center">
            <DropdownMenu>
              <DropdownMenuTrigger
                title="More"
                aria-label="More"
                className="group relative flex h-11 w-11 items-center justify-center rounded-md border-l-2 border-transparent text-text-dim hover:text-text hover:bg-bg-elevated/70 transition-colors duration-1 ease-out"
              >
                <MoreHorizontal size={18} aria-hidden="true" />
              </DropdownMenuTrigger>
              <DropdownMenuContent align="start" side="right" className="w-56 rounded-lg border border-line bg-bg-panel p-1.5 z-50">
                {MORE.map((item, i) => {
                  const Icon = item.icon
                  const active = isActive(item, currentPath)
                  return (
                    <span key={item.href}>
                      {i === 6 && <DropdownMenuSeparator className="my-1 h-[1px] bg-line" />}
                      <DropdownMenuItem asChild>
                        <Link
                          to={item.href}
                          onClick={() => go(item.href)}
                          className={cn(
                            'flex items-center gap-2.5 rounded px-2.5 py-2 text-[13px] transition-colors duration-1 ease-out',
                            active ? 'bg-bg-elevated text-text' : 'text-text-muted hover:text-text hover:bg-bg-elevated/70',
                          )}
                        >
                          <Icon size={15} className={cn('shrink-0', active ? 'text-accent-ink' : 'text-text-dim')} aria-hidden="true" />
                          <span className="font-medium">{item.label}</span>
                        </Link>
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
