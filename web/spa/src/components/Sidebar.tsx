import { useEffect, useRef } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import {
  Camera,
  ClipboardList,
  Eye,
  Film,
  Gauge,
  ListChecks,
  Network,
  MapPin,
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

// One entry in the rail.
//
// `paths` names every path that renders the entry's page. It is there
// because the operations page is rendered at three paths, and a rail that
// names one of them is a rail that cannot say where you are.
interface NavItem {
  href: string
  label: string
  icon: LucideIcon
  paths?: string[]
}

interface NavGroup {
  label: string
  items: NavItem[]
}

// The navigation.
//
// It is a RAIL on a wide screen and a DRAWER on a narrow one.
//
// The rail is 232 pixels. It was 300, which is right for a desktop an
// operator watches all day and wrong for the half of the screen a laptop
// leaves: on a narrow viewport the rail took more than half the width and
// pushed the page it was supposed to be navigating out of sight. A navigation
// that hides the thing it navigates is not a navigation, and the fix is a
// drawer with a button that opens it -- which is the Menu button on the top
// bar is for.
//
// The groups are the operator's questions, in the order they are asked:
//
//   WATCH     what is happening right now
//   DECIDE    what needs a decision
//   REVIEW    what is the record
//   CONFIGURE what the system is set up to do
//
// An operator who has never seen the console can find the answer to a
// question by asking the question. A flat list of twelve entries sorted
// alphabetically asks them to know the product's vocabulary first.
//
// The WATCH group is ONE entry. It used to be three -- Cockpit, Live and
// Events -- and the three of them were three screens answering three
// questions that one screen now answers together. Three entries pointing at
// one page is a navigation that cannot say where you are.
// The navigation's entries.
//
// The paths are RELATIVE to the console's own router, and the reason is the
// one that took a browser to find.
//
// The console is served under `/console` and the router is mounted with that
// as its basename, so the router's own paths are `/`, `/live`, `/incidents`
// and so on, and the basename is what puts `/console` in front of them when a
// link is rendered.
//
// The entries used to carry the full `/console/...` path as well. Two things
// then went wrong at once, and BOTH of them were invisible to every test that
// existed, because every one of them rendered the rail with a hand-written
// path:
//
//   the rendered links were `/console/console/incidents` -- the basename
//   prepended to a path that already carried it -- so every entry in the rail
//   was a link to a path the server does not serve, and
//
//   nothing was ever lit, because the router reports `/incidents` for the
//   page at `/console/incidents` and the entries said `/console/incidents`.
//
// A rail that never says where you are, made of links that all 404, is not a
// navigation. It is a list.
//
// So the paths are the ROUTER's paths, the router's basename is what makes
// the URLs, and `currentPath` is the router's own `location.pathname` -- all
// three in the same space, which is the only way the comparison means
// anything.
const GROUPS: NavGroup[] = [
  {
    label: 'Watch',
    items: [
      // The operations page, and the two paths that render it. The wall and
      // the record are SECTIONS of this page, not pages of their own, and the
      // rail says so: one entry, lit for all three paths.
      { href: '/', paths: ['/', '/live', '/events'], label: 'Operations', icon: Gauge },
    ],
  },
  {
    label: 'Decide',
    items: [
      { href: '/incidents', label: 'Incidents', icon: ClipboardList },
      { href: '/clips', label: 'Clips', icon: Eye },
    ],
  },
  {
    label: 'Review',
    items: [
      { href: '/site', label: 'Site', icon: MapPin },
      // The device registry: what the product is responsible for, and what
      // was MEASURED about each thing. It is the first screen of "plug and
      // play" -- add a camera, the product measures it.
      { href: '/devices', label: 'Devices', icon: Camera },
      // The recording: whether the product is actually writing down what the
      // cameras see, and whether the recording is CONTINUOUS. It is the
      // question a VMS exists to answer, and it was a question the console
      // could not ask at all.
      { href: '/recordings', label: 'Recording', icon: Film },
      // The setup path: what is left to do before this site records
      // anything. It is the "plug and play" promise made checkable, and it
      // was a promise the console could not ask about.
      { href: '/setup', label: 'Setup', icon: ListChecks },
      // The chain: which models may answer, in what order, and what left
      // the site. The order is the policy, and it is what an auditor asks
      // about.
      { href: '/chain', label: 'Chain', icon: Network },
      { href: '/demo-hafnia', label: 'Hafnia demo', icon: Zap },
      { href: '/audit', label: 'Audit', icon: ScrollText },
      { href: '/users', label: 'Users', icon: ShieldCheck },
      // The operations page: the dead letters and the clock drift. It is the
      // health of the INGEST, and an operator who cannot see it cannot tell a
      // quiet site from a broken one. It was a route with no rail entry, which
      // is a page nobody finds.
      { href: '/ops', label: 'Ops', icon: Wrench },
    ],
  },
  {
    label: 'Configure',
    items: [
      { href: '/upload', label: 'Ingest', icon: Upload },
      // The rule bundles: what the product decides, and when. It was a route
      // with no rail entry.
      { href: '/rules', label: 'Rules', icon: ClipboardList },
      { href: '/settings', label: 'Settings', icon: Settings },
    ],
  },
]

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

  // The drawer closes on Escape and takes the keyboard's attention back when
  // it closes. A drawer that stays open behind the content it opened over
  // is a drawer that traps the reader in a place they have left.
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

  return (
    <>
      {/* The scrim behind the drawer, on the narrow screens only. */}
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
          'w-[232px]',
        )}
      >
        <nav className="flex h-full flex-col overflow-y-auto py-4">
          {GROUPS.map((group) => (
            <div key={group.label} className="mb-5">
              <p className="px-4 pb-1.5 font-mono text-[10px] font-medium uppercase tracking-[0.1em] text-text-dim">
                {group.label}
              </p>
              <ul>
                {group.items.map((item) => {
                  const Icon = item.icon
                  // EXACT match, plus the descendants of a section.
                  //
                  // The old rule was "starts with", which marked `/site` AND
                  // `/sites` active at the same time, and marked `/site` active
                  // on `/site/anything`. Two items lit at once is a
                  // navigation that cannot say where you are.
                  //
                  // An entry that names more than one path says so EXPLICITLY:
                  // the operations page is rendered at three paths, and the
                  // rail lights one entry for all three rather than three
                  // entries for one page.
                  const paths = item.paths ?? [item.href]
                  const active = paths.some(
                    (p) =>
                      currentPath === p ||
                      currentPath === p + '/' ||
                      (p !== '/' && currentPath.startsWith(p + '/')),
                  )
                  return (
                    <li key={item.href}>
                      <Link
                        to={item.href}
                        onClick={() => {
                          void navigate(item.href)
                          onClose()
                        }}
                        aria-current={active ? 'page' : undefined}
                        className={cn(
                          'group flex items-center gap-3 mx-3 my-0.5 px-3 py-2 rounded-md text-[13px] transition-colors duration-1 ease-out',
                          active
                            ? 'bg-bg-elevated text-text'
                            : 'text-text-muted hover:text-text hover:bg-bg-elevated/70',
                        )}
                      >
                        <Icon
                          size={16}
                          className={cn(
                            'shrink-0',
                            active ? 'text-accent-ink' : 'text-text-dim group-hover:text-text-muted',
                          )}
                          aria-hidden="true"
                        />
                        <span className="font-medium">{item.label}</span>
                      </Link>
                    </li>
                  )
                })}
              </ul>
            </div>
          ))}

          <div className="mt-auto px-5 pt-4 border-t border-line">
            <a
              href="/"
              className="flex items-center gap-2 text-[12px] text-text-dim hover:text-text transition-colors duration-1 ease-out"
            >
              <span aria-hidden="true">←</span>
              The showcase
            </a>
          </div>

          {/* The close button exists only in the drawer. On a wide screen the
              rail is always there and there is nothing to close. */}
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
