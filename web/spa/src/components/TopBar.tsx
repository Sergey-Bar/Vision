import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Bell, Menu, Users } from 'lucide-react'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from './ui/dropdown-menu'

export function TopBar() {
  const navigate = useNavigate()
  const [now, setNow] = useState(new Date())
  useEffect(() => {
    const id = setInterval(() => setNow(new Date()), 1000)
    return () => clearInterval(id)
  }, [])
  const utc = now.toISOString().substr(11, 8)
  return (
    <header className="h-14 border-b border-border bg-surface/80 backdrop-blur-md flex items-center justify-between px-4 z-20 sticky top-0">
      <div className="flex items-center gap-3">
        <button
          onClick={() => {
            void navigate('/')
          }}
          className="-ml-1 p-2 text-text-muted hover:text-text hover:bg-surface-lift rounded-sm transition-colors"
          aria-label="Open main menu"
        >
          <Menu size={18} />
        </button>
        <div className="flex items-center gap-2">
          <div className="w-7 h-7 rounded-sm bg-accent flex items-center justify-center text-[rgb(0,26,38)] font-bold text-[10px]">V</div>
          <span className="text-sm font-bold text-text tracking-tight">Vision VMS</span>
        </div>
      </div>
      <div className="flex items-center gap-3">
        <div className="hidden md:flex items-center gap-2 px-3 py-1.5 rounded-full border border-border bg-surface-lift text-xs">
          <span className="w-2 h-2 rounded-full bg-success" />
          <span className="text-text-muted">UTC {utc}</span>
        </div>
        <button className="p-2 text-text-muted hover:text-text hover:bg-surface-lift rounded-sm transition-colors" aria-label="Notifications">
          <Bell size={16} />
        </button>
        <DropdownMenu>
          <DropdownMenuTrigger className="flex items-center gap-2 px-3 py-1.5 text-xs font-medium text-text-muted hover:text-text hover:bg-surface-lift rounded-sm transition-colors">
            <Users size={16} className="shrink-0" />
            <span>Operator</span>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="w-48">
            <DropdownMenuItem>Profile</DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem onClick={() => {
              void navigate('/settings')
            }}>Settings</DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem>Sign out</DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
    </header>
  )
}
