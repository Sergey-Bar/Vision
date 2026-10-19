import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Bell, Users, ChevronRight } from 'lucide-react'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from './ui/dropdown-menu'

export function TopBar() {
  const navigate = useNavigate()
  const [now, setNow] = useState(new Date())
  useEffect(() => {
    const id = setInterval(() => setNow(new Date()), 1000)
    return () => clearInterval(id)
  }, [])
  const options: Intl.DateTimeFormatOptions = { timeZone: 'Asia/Jerusalem', hour12: false, hour: '2-digit', minute: '2-digit', second: '2-digit' }
  const jerusalem = new Intl.DateTimeFormat('en-US', options).format(now)
  return (
    <header className="h-22 border-b border-white/10 bg-black/50 backdrop-blur-3xl flex items-center justify-between px-10 z-50 sticky top-0">
      <div className="flex items-center gap-5">
        <div className="flex items-center gap-4">
          <div className="w-12 h-12 rounded-2xl bg-accent flex items-center justify-center text-[14px] font-black tracking-wider text-white shadow-lg shadow-accent/50 glow-accent">
            V
          </div>
          <span className="text-lg font-black text-text tracking-tight">Vision VMS</span>
        </div>
      </div>
      <div className="flex items-center gap-8">
        <div className="hidden sm:flex items-center gap-4 px-5 py-2.5 rounded-full border border-white/10 bg-black/40 text-[11px] font-black">
          <span className="w-3 h-3 rounded-full bg-success animate-pulse-green shadow-lg shadow-success/40" />
          <span className="text-text-muted">{jerusalem}</span>
        </div>
        <button className="p-3 text-text-muted hover:text-text hover:bg-white/5 rounded-2xl transition-all duration-300" aria-label="Notifications">
          <Bell size={19} />
        </button>
        <DropdownMenu>
          <DropdownMenuTrigger className="flex items-center gap-4 px-5 py-2.5 rounded-3xl text-[11px] font-black text-text-muted hover:text-text hover:bg-white/5 transition-all duration-300 group">
            <Users size={18} className="shrink-0" />
            <span>Operator</span>
            <ChevronRight size={15} className="opacity-50 group-hover:opacity-100 transition-opacity duration-300" />
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="w-72 bg-black/95 border border-white/10 rounded-3xl p-3 shadow-2xl backdrop-blur-md">
            <DropdownMenuItem className="text-[14px] font-medium py-3">Profile</DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem onClick={() => {
              void navigate('/settings')
            }} className="text-[14px] font-medium py-3">Settings</DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem className="text-danger text-[14px] font-medium py-3">Sign out</DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
    </header>
  )
}
