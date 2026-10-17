import { PageHeader, Page } from '@/components/Shell'
import { Shield } from 'lucide-react'

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
      <div className="space-y-4">
        <div className="border border-border rounded-xl bg-surface overflow-hidden">
          <table className="w-full text-left text-sm">
            <thead className="bg-surface-lift text-text-muted text-xs uppercase tracking-wider">
              <tr>
                <th className="px-4 py-3">User</th>
                <th className="px-4 py-3">Role</th>
                <th className="px-4 py-3">Permissions</th>
                <th className="px-4 py-3">MFA</th>
                <th className="px-4 py-3 text-right">Status</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-border">
              {USERS.map((u) => (
                <tr key={u.id} className="hover:bg-surface-lift transition-colors group">
                  <td className="px-4 py-3 text-text font-medium">{u.name}</td>
                  <td className="px-4 py-3 text-text-muted">{u.role}</td>
                  <td className="px-4 py-3 text-text-muted">
                    <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded-full border border-border text-[10px] text-text-muted">
                      <Shield size={12} /> {u.role.toLowerCase()}
                    </span>
                  </td>
                  <td className="px-4 py-3">
                    <span className={cnStatus(u.mfa)}>{u.mfa ? 'Enabled' : 'Disabled'}</span>
                  </td>
                  <td className="px-4 py-3 text-text-muted text-right">{u.status}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
    </Page>
  )
}

function cnStatus(mfa: boolean) {
  return `inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-[10px] font-medium ${
    mfa ? 'border-success text-success bg-success/10' : 'border-[#f59e0b] text-[#f59e0b] bg-[#4a2a1a]/40'
  }`
}
