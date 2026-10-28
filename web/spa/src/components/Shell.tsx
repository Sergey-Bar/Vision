import { Suspense, useEffect, useRef, useState } from 'react'
import { TopBar } from './TopBar'
import { Sidebar } from './Sidebar'
import { ErrorBoundary } from './ErrorBoundary'
import { CommandPaletteProvider } from './CommandPalette'
import { LoadingState } from './ui/States'
import { Workspace } from './workspace'

// The shell. The console is ONE screen: every surface is a panel on the same
// page, and the rail switches panels by state. There are no other URLs and no
// other tabs — a console that can be "lost" by navigating to another page is
// not a console.
export function Shell() {
  const [view, setView] = useState('wall')
  const [navOpen, setNavOpen] = useState(false)
  const main = useRef<HTMLElement | null>(null)

  // The focus follows the panel, and the scroll returns to the top.
  useEffect(() => {
    main.current?.scrollTo({ top: 0 })
    const heading = main.current?.querySelector<HTMLElement>('h1')
    if (heading) {
      heading.setAttribute('tabindex', '-1')
      heading.focus({ preventScroll: true })
    }
  }, [view])

  return (
    <CommandPaletteProvider>
      <div className="bg-bg text-text min-h-screen font-sans overflow-x-hidden">
        <TopBar onMenu={() => setNavOpen(true)} />
        <div className="flex flex-1 overflow-hidden">
          <Sidebar
            view={view}
            onNavigate={setView}
            open={navOpen}
            onClose={() => setNavOpen(false)}
          />
          <main ref={main} className="flex-1 overflow-y-auto bg-bg relative">
            <ErrorBoundary>
              <Suspense fallback={<LoadingState label="Loading the screen" />}>
                <div key={view} className="screen-enter">
                  <Workspace view={view} />
                </div>
              </Suspense>
            </ErrorBoundary>
          </main>
        </div>
      </div>
    </CommandPaletteProvider>
  )
}

// The page head, and the page.
export function PageHeader({
  title,
  subtitle,
  eyebrow,
  lede,
  actions,
}: {
  title: string
  subtitle?: string
  eyebrow?: React.ReactNode
  lede?: React.ReactNode
  actions?: React.ReactNode
}) {
  return (
    <div className="px-5 sm:px-6 pb-6 pt-8 flex flex-wrap items-end justify-between gap-4">
      <div className="max-w-[72ch]">
        {eyebrow && (
          <p className="font-mono text-[11px] font-medium uppercase tracking-[0.08em] text-text-dim">
            {eyebrow}
          </p>
        )}
        <h1 className="mt-1 text-[34px] font-semibold leading-[40px] tracking-[-0.025em] text-text outline-none">
          {title}
        </h1>
        {(subtitle ?? lede) && (
          <p className="mt-2 text-[15px] leading-[22px] text-text-muted">{lede ?? subtitle}</p>
        )}
      </div>
      {actions && <div className="flex items-center gap-3">{actions}</div>}
    </div>
  )
}

export function Page({ children }: { children: React.ReactNode }) {
  return <div className="px-5 sm:px-6 pb-20 max-w-[1400px] mx-auto">{children}</div>
}
