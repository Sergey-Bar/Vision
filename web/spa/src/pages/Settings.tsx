import { PageHeader, Page } from '@/components/Shell'
import { Bell } from 'lucide-react'

export function SettingsPage() {
  return (
    <Page>
      <PageHeader title="Settings" subtitle="Console preferences" />
      <div className="space-y-4">
        <div className="border border-border rounded-xl bg-surface p-4">
          <div className="flex items-center justify-between py-3 border-b border-border">
            <div>
              <div className="text-sm font-medium text-text">Theme</div>
              <div className="text-xs text-text-muted">Console is LTR (lang="en")</div>
            </div>
            <div className="flex items-center gap-2">
              <Bell size={16} className="text-text-muted" />
            </div>
          </div>
        </div>
        <div className="text-text-muted text-sm italic">No auth/session mechanism is configured yet; admin routes are not gated.</div>
      </div>
    </Page>
  )
}
