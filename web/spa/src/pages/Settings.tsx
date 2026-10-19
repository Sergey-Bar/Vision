import { PageHeader, Page } from '@/components/Shell'
import { Bell } from 'lucide-react'

export function SettingsPage() {
  return (
    <Page>
      <PageHeader title="Settings" subtitle="Console preferences" />
      <div className="space-y-10">
        <div className="border border-white/10 rounded-3xl bg-black/30 backdrop-blur-sm p-8 shadow-2xl">
          <div className="flex items-center justify-between py-6 border-b border-white/10">
            <div>
              <div className="text-sm font-black text-text">Theme</div>
              <div className="text-xs text-text-dim font-black">Console is LTR (lang="en")</div>
            </div>
            <div className="flex items-center gap-2">
              <Bell size={19} className="text-text-dim" />
            </div>
          </div>
        </div>
        <div className="text-xs text-text-dim font-black italic">No auth/session mechanism is configured yet; admin routes are not gated.</div>
      </div>
    </Page>
  )
}
