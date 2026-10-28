import { Suspense, useEffect, useRef, useState } from 'react'
import { Outlet, useLocation, Routes, Route } from 'react-router-dom'
import { TopBar } from './TopBar'
import { Sidebar } from './Sidebar'
import { ErrorBoundary } from './ErrorBoundary'
import { CommandPaletteProvider } from './CommandPalette'
import { LoadingState } from './ui/States'
import { OperationsPage } from '../pages/Operations'
import { LivePage } from '../pages/Live'
import { EventsPage } from '../pages/Events'
import { IncidentsPage } from '../pages/Incidents'
import { IncidentDetailPage } from '../pages/IncidentDetail'
import { ClipsPage } from '../pages/Clips'
import { EvidencePage } from '../pages/Evidence'
import { UploadPage } from '../pages/Upload'
import { SitePage } from '../pages/Site'
import { ChainPage } from '../pages/Chain'
import { DevicesPage } from '../pages/Devices'
import { RecordingsPage } from '../pages/Recordings'
import { SetupPage } from '../pages/Setup'
import { RulesPage } from '../pages/Rules'
import { OpsPage } from '../pages/Ops'
import { UsersPage } from '../pages/Users'
import { AuditPage } from '../pages/Audit'
import { SettingsPage } from '../pages/Settings'
import { NotFound } from '../pages/NotFound'
import { DemoHafniaPage } from '../pages/DemoHafnia'

// The shell.
//
// It is a STAGE, an INSTRUMENT and a RECORD:
//
//   the stage    the top bar and the rail: where you are and what you can do
//   the instrument the screen: one question, one answer, five states
//   the record   what the product actually holds, and what it could not
//                answer
//
// The order matters. The stage is fixed, the instrument scrolls, and the
// record is inside the instrument. A console that scrolls its own chrome
// loses the answer to "where am I" every time the operator scrolls.
export function Shell() {
  const location = useLocation()
  // The navigation is a drawer on a narrow screen, and the state lives here
  // because the button that opens it is on the top bar and the drawer is in
  // the sidebar, and two siblings do not talk to each other.
  const [navOpen, setNavOpen] = useState(false)
  return (
    <CommandPaletteProvider>
      <div className="bg-bg text-text min-h-screen font-sans overflow-x-hidden">
        <TopBar onMenu={() => setNavOpen(true)} />
        <Routes>
          <Route
            element={
              <Layout
                currentPath={location.pathname}
                open={navOpen}
                onClose={() => setNavOpen(false)}
              />
            }
          >
            {/* The home page is the operations page: the operator's first
                question is what needs them now, and the answer is a page rather
                than a click.

                `live` and `events` are the wall and the record. They were pages
                of their own and they are now scroll targets on the same page,
                so a bookmark and the rehearsal's URL list both keep working
                and neither lands the operator on a second version of the
                truth. */}
            <Route index element={<OperationsPage />} />
            <Route path="live" element={<LivePage />} />
            <Route path="events" element={<EventsPage />} />
            <Route path="incidents" element={<IncidentsPage />} />
            <Route path="incidents/:id" element={<IncidentDetailPage />} />
            <Route path="clips" element={<ClipsPage />} />
            <Route path="evidence/:id" element={<EvidencePage />} />
            <Route path="upload" element={<UploadPage />} />
            <Route path="site" element={<SitePage />} />
            <Route path="devices" element={<DevicesPage />} />
            <Route path="recordings" element={<RecordingsPage />} />
            <Route path="setup" element={<SetupPage />} />
            <Route path="chain" element={<ChainPage />} />
            <Route path="rules" element={<RulesPage />} />
            <Route path="ops" element={<OpsPage />} />
            <Route path="users" element={<UsersPage />} />
            <Route path="audit" element={<AuditPage />} />
            <Route path="settings" element={<SettingsPage />} />
            <Route path="demo-hafnia" element={<DemoHafniaPage />} />
          </Route>
          <Route path="*" element={<NotFound />} />
        </Routes>
      </div>
    </CommandPaletteProvider>
  )
}

function Layout({
  currentPath,
  open,
  onClose,
}: {
  currentPath: string
  open: boolean
  onClose: () => void
}) {
  const main = useRef<HTMLElement | null>(null)

  // The focus follows the route, and the scroll returns to the top.
  //
  // A screen change that leaves the keyboard's focus on a link that is no
  // longer there is a screen change a keyboard user cannot follow: the next
  // keypress goes somewhere they cannot see. The focus moves to the new
  // screen's heading, which is the first thing on it.
  useEffect(() => {
    main.current?.scrollTo({ top: 0 })
    const heading = main.current?.querySelector<HTMLElement>('h1')
    if (heading) {
      heading.setAttribute('tabindex', '-1')
      heading.focus({ preventScroll: true })
    }
  }, [currentPath])

  return (
    <div className="flex flex-1 overflow-hidden">
      <Sidebar currentPath={currentPath} open={open} onClose={onClose} />
      <main ref={main} className="flex-1 overflow-y-auto bg-bg relative">
        <ErrorBoundary>
          <Suspense fallback={<LoadingState label="Loading the screen" />}>
            <div key={currentPath} className="screen-enter">
              <Outlet />
            </div>
          </Suspense>
        </ErrorBoundary>
      </main>
    </div>
  )
}

// The page head, and the page.
//
// Every screen uses them, so every screen has the same shape: a title, a
// ledge, and a body that is a sequence of panels. The margin is 40 pixels
// on a wide screen and 20 on a narrow one.
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
