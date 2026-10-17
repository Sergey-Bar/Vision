import { PageHeader, Page } from '@/components/Shell'

const SITES = [
  { id: 'site-1', name: 'North Campus', zone: 'Retail', cameras: 8, retention: '30 days' },
  { id: 'site-2', name: 'West Warehouse', zone: 'Warehouse', cameras: 6, retention: '60 days' },
  { id: 'site-3', name: 'Downtown Hub', zone: 'Lobby', cameras: 12, retention: '14 days' },
]

export function SitesPage() {
  return (
    <Page>
      <PageHeader title="Sites" subtitle="Site list, timezones and retention" />
      <div className="space-y-4">
        <div className="border border-border rounded-xl bg-surface overflow-hidden">
          <table className="w-full text-left text-sm">
            <thead className="bg-surface-lift text-text-muted text-xs uppercase tracking-wider">
              <tr>
                <th className="px-4 py-3">Site</th>
                <th className="px-4 py-3">Zone</th>
                <th className="px-4 py-3">Cameras</th>
                <th className="px-4 py-3">Retention</th>
                <th className="px-4 py-3 text-right">Timezone</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-border">
              {SITES.map((s) => (
                <tr key={s.id} className="hover:bg-surface-lift transition-colors group">
                  <td className="px-4 py-3 text-text font-medium">{s.name}</td>
                  <td className="px-4 py-3 text-text-muted">{s.zone}</td>
                  <td className="px-4 py-3 text-text-muted">{s.cameras}</td>
                  <td className="px-4 py-3 text-text-muted">{s.retention}</td>
                  <td className="px-4 py-3 text-text-muted text-right">UTC</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
    </Page>
  )
}
