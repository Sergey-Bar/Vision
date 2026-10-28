import type { LucideIcon } from 'lucide-react'
import {
  Camera,
  ClipboardList,
  Eye,
  Film,
  Gauge,
  ListChecks,
  MapPin,
  MonitorPlay,
  Network,
  ScrollText,
  Settings,
  ShieldCheck,
  Upload,
  Wrench,
  Zap,
} from 'lucide-react'
import { VmsWorkstation } from '@/pages/VmsWorkstation'
import { OperationsPage } from '@/pages/Operations'
import { IncidentsPage } from '@/pages/Incidents'
import { ClipsPage } from '@/pages/Clips'
import { DevicesPage } from '@/pages/Devices'
import { RecordingsPage } from '@/pages/Recordings'
import { SitePage } from '@/pages/Site'
import { AuditPage } from '@/pages/Audit'
import { UsersPage } from '@/pages/Users'
import { OpsPage } from '@/pages/Ops'
import { RulesPage } from '@/pages/Rules'
import { ChainPage } from '@/pages/Chain'
import { SetupPage } from '@/pages/Setup'
import { UploadPage } from '@/pages/Upload'
import { SettingsPage } from '@/pages/Settings'
import { DemoHafniaPage } from '@/pages/DemoHafnia'

// The one screen. Every surface the console has is a panel on this screen,
// and the rail switches between them by state — there are no other URLs and
// no other tabs.
export interface WorkspaceView {
  id: string
  label: string
  icon: LucideIcon
  element: React.ReactNode
}

export const WORKSPACE_VIEWS: WorkspaceView[] = [
  { id: 'wall', label: 'Workstation', icon: MonitorPlay, element: <VmsWorkstation /> },
  { id: 'operations', label: 'Operations', icon: Gauge, element: <OperationsPage /> },
  { id: 'incidents', label: 'Incidents', icon: ClipboardList, element: <IncidentsPage /> },
  { id: 'clips', label: 'Clips', icon: Eye, element: <ClipsPage /> },
  { id: 'devices', label: 'Devices', icon: Camera, element: <DevicesPage /> },
  { id: 'recording', label: 'Recording', icon: Film, element: <RecordingsPage /> },
  { id: 'site', label: 'Site', icon: MapPin, element: <SitePage /> },
  { id: 'chain', label: 'Chain', icon: Network, element: <ChainPage /> },
  { id: 'audit', label: 'Audit', icon: ScrollText, element: <AuditPage /> },
  { id: 'users', label: 'Users', icon: ShieldCheck, element: <UsersPage /> },
  { id: 'ops', label: 'Ops', icon: Wrench, element: <OpsPage /> },
  { id: 'rules', label: 'Rules', icon: ClipboardList, element: <RulesPage /> },
  { id: 'setup', label: 'Setup', icon: ListChecks, element: <SetupPage /> },
  { id: 'ingest', label: 'Ingest', icon: Upload, element: <UploadPage /> },
  { id: 'demo', label: 'Hafnia demo', icon: Zap, element: <DemoHafniaPage /> },
  { id: 'settings', label: 'Settings', icon: Settings, element: <SettingsPage /> },
]

export function Workspace({ view }: { view: string }) {
  const active = WORKSPACE_VIEWS.find((v) => v.id === view) ?? WORKSPACE_VIEWS[0]
  return <div className="min-h-full">{active.element}</div>
}
