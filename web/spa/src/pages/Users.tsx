import { PageHeader, Page } from '@/components/Shell'
import { Shield } from 'lucide-react'
import { cn } from '@/lib/utils'

const USERS = [
  { id: 'u-1', name: 'A. Rivera', role: 'Operator', mfa: true, status: 'active' },
  { id: 'u-2', name: 'B. Chen', role: 'Supervisor', mfa: true, status: 'active' },
  { id: 'u-3', name: 'C. Diaz', role: 'Viewer', mfa: false, status: 'active' },
  { id: 'u-4', name: 'D. Ewing', role: 'Operator', mfa: false, status: 'invited' },
]

export function UsersPage() {
  return (
    <Page>
      <PageHeader title="Users & Roles" subtitle="Users, roles, permissions and MFA status" />
      <div className="space-y-10">
        <div className="border border-white/10 rounded-3xl bg-black/30 backdrop-blur-sm overflow-hidden shadow-2xl">
          <table className="w-full text-left text-sm">
            <thead className="bg-black/40 text-text-dim text-[10px] font-black uppercase tracking-[0.12em] sticky top-0 z-20">
              <tr>
                <th className="px-8 py-5 font-black">User</th>
                <th className="px-8 py-5 font-black">Role</th>
                <th className="px-8 py-5 font-black">Permissions</th>
                <th className="px-8 py-5 font-black">MFA</th>
                <th className="px-8 py-5 font-black text-right">Status</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-white/10">
              {USERS.map((u) => (
                <tr key={u.id} className="hover:bg-white/5 transition-all duration-300 group border-b border-white/5 last:border-0">
                  <td className="px-8 py-5 text-text font-black">{u.name}</td>
                  <td className="px-8 py-5 text-text-dim">{u.role}</td>
                  <td className="px-8 py-5 text-text-dim">
                    <span className="inline-flex items-center gap-3 px-4 py-2 rounded-full border border-white/10 text-[10px] font-black text-text-dim">
                      <Shield size={15} /> {u.role.toLowerCase()}
                    </span>
                  </td>
                  <td className="px-8 py-5">
                    <span className={cn(
                      'inline-flex items-center gap-3 px-4 py-2 rounded-full border text-[10px] font-black',
                      u.mfa ? 'border-success text-success bg-success/10 shadow-lg shadow-success/20' : 'border-[#f59e0b] text-[#fbbf24] bg-[#fbbf24]/10 shadow-lg shadow-[#fbbf24]/20',
                    )}>
                      {u.mfa ? 'Enabled' : 'Disabled'}
                    </span>
                  </td>
                  <td className="px-8 py-5 text-text-dim text-right">{u.status}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
    </Page>
  )
}
