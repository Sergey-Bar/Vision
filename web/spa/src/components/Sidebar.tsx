import { Link, useNavigate } from 'react-router-dom'
import { useState } from 'react'
import { Menu, LayoutGrid, Activity, ClipboardList, Video, ShieldAlert, Users, Settings } from 'lucide-react'
import { cn } from '@/lib/utils'

const NAV = [
  { href: '/', label: 'Live', icon: LayoutGrid },
  { href: '/events', label: 'Events', icon: Activity },
  { href: '/incidents', label: 'Incidents', icon: ClipboardList },
  { href: '/clips', label: 'Clips', icon: Video },
  { href: '/cameras', label: 'Cameras', icon: Video },
  { href: '/sites', label: 'Sites', icon: ShieldAlert },
  { href: '/users', label: 'Users', icon: Users },
  { href: '/audit', label: 'Audit', icon: Activity },
  { href: '/settings', label: 'Settings', icon: Settings },
]

export function Sidebar({ currentPath }: { currentPath: string }) {
  const [collapsed, setCollapsed] = useState(false)
  const navigate = useNavigate()

  return (
    <aside className="flex-shrink-0 border-r border-border bg-surface z-10 transition-all duration-300" style={{ width: collapsed ? 72 : 260 }}>
      <div className="flex items-center justify-between px-4 h-12 border-b border-border">
        {!collapsed && (
          <div className="flex items-center gap-2">
            <div className="w-8 h-8 rounded-sm bg-accent flex items-center justify-center text-[rgb(0,26,38)] font-bold text-xs">V</div>
            <span className="text-sm font-bold text-text tracking-tight">Vision VMS</span>
          </div>
        )}
        <button
          onClick={() => setCollapsed((c) => !c)}
          className="p-2 text-text-muted hover:text-text hover:bg-surface-lift rounded-sm transition-colors"
          aria-label={collapsed ? 'Expand navigation' : 'Collapse navigation'}
        >
          <Menu size={18} />
        </button>
      </div>
      <nav className="flex-1 py-3 overflow-y-auto">
        {NAV.map((item) => {
          const Icon = item.icon
          const active = currentPath === item.href || (item.href !== '/' && currentPath.startsWith(item.href))
          return (
            <Link
              key={item.href}
              to={item.href}
              onClick={() => {
                void navigate(item.href)
              }}
              className={cn(
                'flex items-center gap-3 px-4 py-2.5 text-sm font-medium transition-colors rounded-sm',
                active
                  ? 'bg-accent text-[rgb(0,26,38)]'
                  : 'text-text-muted hover:text-text hover:bg-surface-lift',
              )}
            >
              <Icon size={16} className="shrink-0" />
              {!collapsed && <span>{item.label}</span>}
            </Link>
          )
        })}
      </nav>
    </aside>
  )
}
