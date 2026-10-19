import { Link, useNavigate } from 'react-router-dom'
import { useState } from 'react'
import { Menu, LayoutGrid, Activity, ClipboardList, Video, ShieldAlert, Users, Settings, ChevronRight, Gauge, MapPin } from 'lucide-react'
import { cn } from '@/lib/utils'

// The navigation.
//
// The first entry is the cockpit, because it is the home page and the
// operator's first question. The live grid is a second entry rather than the
// first, because the grid is one panel of the cockpit and a page of its own
// only when the operator wants the grid full-size.
const NAV = [
  { href: '/', label: 'Cockpit', icon: Gauge },
  { href: '/live', label: 'Live', icon: LayoutGrid },
  { href: '/events', label: 'Events', icon: Activity },
  { href: '/incidents', label: 'Incidents', icon: ClipboardList },
  { href: '/clips', label: 'Clips', icon: Video },
  { href: '/upload', label: 'Upload', icon: Video },
  { href: '/cameras', label: 'Cameras', icon: Video },
  { href: '/site', label: 'Site', icon: MapPin },
  { href: '/sites', label: 'Sites', icon: ShieldAlert },
  { href: '/users', label: 'Users', icon: Users },
  { href: '/audit', label: 'Audit', icon: Activity },
  { href: '/settings', label: 'Settings', icon: Settings },
]

export function Sidebar({ currentPath }: { currentPath: string }) {
  const [collapsed, setCollapsed] = useState(false)
  const navigate = useNavigate()

  return (
    <aside className="flex-shrink-0 border-r border-white/10 bg-black/40 backdrop-blur-md z-40 transition-all duration-500" style={{ width: collapsed ? 80 : 300 }}>
      <div className="flex items-center justify-between px-8 h-22 border-b border-white/10">
        {!collapsed && (
          <div className="flex items-center gap-4">
            <div className="w-12 h-12 rounded-2xl bg-accent flex items-center justify-center text-[14px] font-black tracking-wider text-white shadow-lg shadow-accent/50">
              V
            </div>
          </div>
        )}
        <button
          onClick={() => setCollapsed((c) => !c)}
          className="p-3 text-text-muted hover:text-text hover:bg-white/5 rounded-2xl transition-all duration-300"
          aria-label={collapsed ? 'Expand navigation' : 'Collapse navigation'}
        >
          <Menu size={20} />
        </button>
      </div>
      <nav className="flex-1 py-8 overflow-y-auto">
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
                'flex items-center gap-5 px-8 py-4 text-sm font-bold transition-all duration-300 rounded-2xl',
                active
                  ? 'bg-accent text-white shadow-2xl shadow-accent/50'
                  : 'text-text hover:text-text hover:bg-white/5 hover:border-white/5 border-transparent',
              )}
            >
              <Icon size={19} className="shrink-0" />
              {!collapsed && <span className="font-black">{item.label}</span>}
              {collapsed ? (
                <ChevronRight size={16} className="text-text-dim opacity-50 ml-auto" />
              ) : null}
            </Link>
          )
        })}
      </nav>
      <div className="px-8 py-8 border-t border-white/10">
        <div className="flex items-center gap-4 p-4 rounded-3xl border border-white/5 bg-black/20 hover:bg-white/5 transition-all duration-300 cursor-pointer">
          <ShieldAlert size={19} className="text-accent shadow-lg shadow-accent/30" />
          <span className="text-[12px] font-black text-text">System health</span>
        </div>
      </div>
    </aside>
  )
}
