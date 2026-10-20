import { useEffect, useState } from 'react'
import { Activity, ChevronRight, Menu } from 'lucide-react'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from './ui/dropdown-menu'
import { useAIRuntime } from '@/hooks/api'
import { useIncidents } from '@/hooks/product'

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
// without an offset is a time two people will read differently.
export function TopBar({ onMenu }: { onMenu: () => void }) {
  const [now, setNow] = useState(new Date())
  useEffect(() => {
    const id = setInterval(() => setNow(new Date()), 1000)
    return () => clearInterval(id)
  }, [])

  const zone = Intl.DateTimeFormat().resolvedOptions().timeZone
  const clock = new Intl.DateTimeFormat('en-GB', {
    timeZone: zone,
    hour12: false,
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    timeZoneName: 'shortOffset',
  }).format(now)

  const { data: aiRuntime } = useAIRuntime()
  const { data: incidents } = useIncidents(50)
  const open = (incidents ?? []).filter((i) => i.state === 'open' || i.state === 'detected').length

  return (
    <header className="h-22 border-b border-white/10 bg-black/50 backdrop-blur-3xl flex items-center justify-between px-5 sm:px-10 z-50 sticky top-0">
      <div className="flex items-center gap-5">
        {/* The navigation is a drawer on a narrow screen, and this is the button
            that opens it. It is hidden on the wide screens, where the rail is
            always there. */}
        <button
          onClick={onMenu}
          className="lg:hidden p-3 -ml-3 text-text-muted hover:text-text hover:bg-white/5 rounded-2xl transition-all duration-300"
          aria-label="Open navigation"
        >
          <Menu size={20} />
        </button>
        <div className="flex items-center gap-4">
          <div className="w-12 h-12 rounded-2xl bg-accent flex items-center justify-center text-[14px] font-black tracking-wider text-white shadow-lg shadow-accent/50 glow-accent">
            V
          </div>
          <span className="text-lg font-black text-text tracking-tight hidden sm:inline">Vision VMS</span>
        </div>
      </div>
      <div className="flex items-center gap-8">
        <div className="hidden sm:flex items-center gap-4 px-5 py-2.5 rounded-full border border-white/10 bg-black/40 text-[11px] font-black">
          <span
            className={`w-3 h-3 rounded-full animate-pulse-green shadow-lg ${
              aiRuntime?.available ? 'bg-success shadow-success/40' : 'bg-warning shadow-warning/40'
            }`}
          />
          <span className="text-text-muted">{clock}</span>
        </div>
        <div className="hidden sm:flex items-center gap-4 px-5 py-2.5 rounded-full border border-white/10 bg-black/40 text-[11px] font-black">
          <Activity size={15} className="text-accent" />
          <span className="text-text-muted">{open} open</span>
        </div>
        <DropdownMenu>
          <DropdownMenuTrigger className="flex items-center gap-4 px-5 py-2.5 rounded-3xl text-[11px] font-black text-text-muted hover:text-text hover:bg-white/5 transition-all duration-300 group">
            <span className="w-8 h-8 rounded-xl bg-white/5 border border-white/10 flex items-center justify-center text-[10px] font-black text-text-dim">
              SP
            </span>
            <span>Service principal</span>
            <ChevronRight size={15} className="opacity-50 group-hover:opacity-100 transition-opacity duration-300" />
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="w-72 bg-black/95 border border-white/10 rounded-3xl p-3 shadow-2xl backdrop-blur-md">
            <div className="px-4 py-3">
              <div className="text-[10px] font-black uppercase tracking-[0.12em] text-text-dim">Acting identity</div>
              <div className="mt-1 text-[13px] font-black text-text">The product's configured principal</div>
              <div className="mt-1 text-[11px] text-text-dim">
                This console has no login. Every action in the ledger is attributed to the
                principal the API is configured with, and there is no person to sign out of.
              </div>
            </div>
            <DropdownMenuSeparator />
            <DropdownMenuItem className="text-[14px] font-medium py-3">Settings</DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
    </header>
  )
}
