import { Outlet, useLocation, Routes, Route } from 'react-router-dom'
import { TopBar } from './TopBar'
import { Sidebar } from './Sidebar'
import { LivePage } from '../pages/Live'
import { EventsPage } from '../pages/Events'
import { IncidentsPage } from '../pages/Incidents'
import { ClipsPage } from '../pages/Clips'
import { CamerasPage } from '../pages/Cameras'
import { SitesPage } from '../pages/Sites'
import { UsersPage } from '../pages/Users'
import { AuditPage } from '../pages/Audit'
import { SettingsPage } from '../pages/Settings'
import { NotFound } from '../pages/NotFound'

export function Shell() {
  const location = useLocation()
  return (
    <div className="bg-bg text-text min-h-screen font-sans selection:bg-accent selection:text-[rgb(0,26,38)]">
      <TopBar />
      <Routes>
        <Route element={<Layout currentPath={location.pathname} />}>
          <Route index element={<LivePage />} />
          <Route path="events" element={<EventsPage />} />
          <Route path="incidents" element={<IncidentsPage />} />
          <Route path="clips" element={<ClipsPage />} />
          <Route path="cameras" element={<CamerasPage />} />
          <Route path="sites" element={<SitesPage />} />
          <Route path="users" element={<UsersPage />} />
          <Route path="audit" element={<AuditPage />} />
          <Route path="settings" element={<SettingsPage />} />
        </Route>
        <Route path="*" element={<NotFound />} />
      </Routes>
    </div>
  )
}

function Layout({ currentPath }: { currentPath: string }) {
  return (
    <div className="flex flex-1 overflow-hidden">
      <Sidebar currentPath={currentPath} />
      <main className="flex-1 overflow-y-auto bg-bg relative">
        <Outlet />
      </main>
    </div>
  )
}

export function PageHeader({ title, subtitle }: { title: string; subtitle?: string }) {
  return (
    <div className="flex items-center gap-3 px-4 py-4 border-b border-border bg-surface/50">
      <div className="w-2 h-2 rounded-full bg-accent" />
      <div>
        <h1 className="text-sm font-semibold text-text tracking-tight">{title}</h1>
        {subtitle && <p className="text-xs text-text-muted">{subtitle}</p>}
      </div>
    </div>
  )
}

export function Page({ children }: { children: React.ReactNode }) {
  return <div className="px-4 pb-8">{children}</div>
}
