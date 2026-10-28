import { Activity, ChevronRight, Menu, Search } from 'lucide-react'
import { DropdownMenu, DropdownMenuContent, DropdownMenuTrigger } from './ui/dropdown-menu'
import { Wordmark } from './Mark'
import { usePalette } from './CommandPalette'
import { useAIRuntime } from '@/hooks/api'
import { useIncidents, usePrincipal } from '@/hooks/product'
import { useSiteClock } from '@/lib/site'

// The top bar.
//
// There is no user here, and the bar does not pretend there is.
//
// The console has no login: the API runs as a configured principal, and every
// action in the ledger is attributed to that principal rather than to a person.
// So there is no avatar, no "Operator", no profile and no "Sign out" -- a sign-out
// button on a console that never signed in is a control that lies about what the
// system is, and the plan's rule is that the console never implies a person did
// something.
//
// The clock is the SITE's, not the host's, and it carries its offset: a time
// without an offset is a time two people will read differently. When the site
// has not told the console which timezone it is in, the clock says UTC rather
// than quietly using the browser's.
export function TopBar({ onMenu }: { onMenu: () => void }) {
  const { clock, zone, known } = useSiteClock()
  const { data: aiRuntime } = useAIRuntime()
  const { data: incidents } = useIncidents(50)
  const { data: principal } = usePrincipal()
  const palette = usePalette()

  const open = (incidents ?? []).filter((i) => i.state === 'open' || i.state === 'detected').length

  return (
    <header className="h-bar border-b border-line bg-bg flex items-center justify-between px-5 sm:px-6 sticky top-0 z-50">
      <div className="flex items-center gap-4 min-w-0">
        {/* The navigation is a drawer on a narrow screen, and this is the
            button that opens it. It is hidden on the wide screens, where the
            rail is always there. */}
        <button
          onClick={onMenu}
          className="lg:hidden -ml-2 p-2 text-text-muted hover:text-text transition-colors duration-1 ease-out"
          aria-label="Open navigation"
        >
          <Menu size={18} />
        </button>
        <a href="/console" className="flex items-center min-w-0 hover:opacity-90 transition-opacity duration-1 ease-out">
          <Wordmark />
        </a>
      </div>

      <div className="flex items-center gap-5 sm:gap-6">
        <div className="hidden sm:flex items-center gap-2 text-[12px] text-text-dim tabular-nums">
          <span>{clock}</span>
          <span className="text-text-muted">{known ? zone : 'UTC (site zone unknown)'}</span>
        </div>

        {/* The AI runtime. It does not pulse: a pulsing dot beside a number
            that changes is a page that reflows while somebody is reading
            it, and the word beside the dot says the same thing without
            moving. */}
        <div className="hidden sm:flex items-center gap-2 text-[12px] text-text-dim">
          <span aria-hidden="true" className={aiRuntime?.available ? 'text-ok-ink' : 'text-warn-ink'}>
            {aiRuntime?.available ? '●' : '▲'}
          </span>
          <span>{aiRuntime?.available ? 'AI ready' : 'AI unavailable'}</span>
        </div>

        <div className="flex items-center gap-2 text-[12px] text-text-dim">
          <Activity size={14} className="text-text-dim" aria-hidden="true" />
          <span className="tabular-nums">{open}</span>
          <span className="hidden sm:inline">open</span>
        </div>

        {/* ONE search. The button is here because the operator's hands are on
            the keyboard, and the key is ⌘K. The button exists for the ones
            whose hands are not. */}
        <button
          onClick={palette.open}
          className="flex items-center gap-2 rounded border border-line px-3 py-1.5 text-[12px] text-text-dim hover:text-text hover:bg-bg-elevated transition-colors duration-1 ease-out"
          aria-label="Search ⌘K"
        >
          <Search size={14} aria-hidden="true" />
          <span className="hidden sm:inline">Search</span>
          <kbd
            aria-hidden="true"
            className="hidden sm:inline rounded border border-line px-1.5 py-0.5 font-mono text-[10px]"
          >
            ⌘K
          </kbd>
        </button>

        <DropdownMenu>
          <DropdownMenuTrigger className="flex items-center gap-2 text-[12px] text-text-muted hover:text-text transition-colors duration-1 ease-out group">
            <span className="h-7 w-7 rounded border border-line bg-bg-elevated flex items-center justify-center text-[11px] font-medium text-text">
              SP
            </span>
            <span className="hidden sm:inline">Service principal</span>
            <ChevronRight size={14} className="opacity-50 group-hover:opacity-100 transition-opacity duration-1 ease-out" />
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="w-72 rounded-lg border border-line bg-bg-panel p-1.5">
            <div className="px-3 py-2.5">
              <div className="font-mono text-[10px] font-medium uppercase tracking-[0.1em] text-text-dim">
                Acting identity
              </div>
              <div className="mt-1 text-[13px] font-semibold text-text">The product's configured principal</div>
              <div className="mt-1 text-[12px] leading-[18px] text-text-dim">
                This console has no login. Every action in the ledger is attributed to the
                principal the API is configured with, and there is no person to sign out of.
              </div>
              {principal && (
                <dl className="mt-3 grid grid-cols-[88px_1fr] gap-x-3 gap-y-1 text-[12px]">
                  <dt className="font-mono text-[10px] uppercase tracking-[0.06em] text-text-dim">tenant</dt>
                  <dd className="text-text-muted font-mono text-[11px] break-all">{principal.tenantId}</dd>
                  <dt className="font-mono text-[10px] uppercase tracking-[0.06em] text-text-dim">roles</dt>
                  <dd className="text-text-muted">{(principal.roles ?? []).join(', ') || 'none'}</dd>
                  <dt className="font-mono text-[10px] uppercase tracking-[0.06em] text-text-dim">site scope</dt>
                  <dd className="text-text-muted font-mono text-[11px] break-all">
                    {(principal.siteScope ?? []).join(', ') || 'every site in the tenant'}
                  </dd>
                </dl>
              )}
            </div>
            {/* The Settings item is gone. The page has its own route, and a
                dropdown item that does not go to the page is a lie about where
                the page is. */}
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
    </header>
  )
}
