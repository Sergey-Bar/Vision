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
      <div className="space-y-10">
        <div className="border border-white/10 rounded-3xl bg-black/30 backdrop-blur-sm overflow-hidden shadow-2xl">
          <table className="w-full text-left text-sm">
            <thead className="bg-black/40 text-text-dim text-[10px] font-black uppercase tracking-[0.12em] sticky top-0 z-20">
              <tr>
                <th className="px-8 py-5 font-black">Site</th>
                <th className="px-8 py-5 font-black">Zone</th>
                <th className="px-8 py-5 font-black">Cameras</th>
                <th className="px-8 py-5 font-black">Retention</th>
                <th className="px-8 py-5 font-black text-right">Timezone</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-white/10">
              {SITES.map((s) => (
                <tr key={s.id} className="hover:bg-white/5 transition-all duration-300 group border-b border-white/5 last:border-0">
                  <td className="px-8 py-5 text-text font-black">{s.name}</td>
                  <td className="px-8 py-5 text-text-dim">{s.zone}</td>
                  <td className="px-8 py-5 text-text-dim">{s.cameras}</td>
                  <td className="px-8 py-5 text-text-dim">{s.retention}</td>
                  <td className="px-8 py-5 text-text-dim text-right font-black">UTC</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
    </Page>
  )
}
