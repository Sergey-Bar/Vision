import { Outlet, useLocation, Routes, Route } from 'react-router-dom'
import { TopBar } from './TopBar'
import { Sidebar } from './Sidebar'
import { LivePage } from '../pages/Live'
import { EventsPage } from '../pages/Events'
import { IncidentsPage } from '../pages/Incidents'
import { ClipsPage } from '../pages/Clips'
import { UploadPage } from '../pages/Upload'
import { CamerasPage } from '../pages/Cameras'
import { SitesPage } from '../pages/Sites'
import { UsersPage } from '../pages/Users'
import { AuditPage } from '../pages/Audit'
import { SettingsPage } from '../pages/Settings'
import { NotFound } from '../pages/NotFound'

export function Shell() {
  const location = useLocation()
  return (
    <div className="bg-bg text-text min-h-screen font-sans selection:bg-accent selection:text-white overflow-x-hidden">
      <TopBar />
      <Routes>
        <Route element={<Layout currentPath={location.pathname} />}>
          <Route index element={<LivePage />} />
          <Route path="events" element={<EventsPage />} />
          <Route path="incidents" element={<IncidentsPage />} />
          <Route path="clips" element={<ClipsPage />} />
          <Route path="upload" element={<UploadPage />} />
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
    <div className="px-10 pb-8 pt-14 page-transition animate-fade-in">
      <div className="flex items-center gap-4 mb-6">
        <span className="w-2.5 h-2.5 rounded-full bg-accent animate-pulse-red shadow-lg shadow-accent/50" />
        <h1 className="text-3xl font-black text-text tracking-tight">{title}</h1>
      </div>
      {subtitle && <p className="text-sm text-text-muted font-medium max-w-2xl">{subtitle}</p>}
    </div>
  )
}

export function Page({ children }: { children: React.ReactNode }) {
  return <div className="px-10 pb-20 max-w-[1400px] mx-auto">{children}</div>
}
