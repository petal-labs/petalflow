import { Link, useRouterState } from '@tanstack/react-router'
import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { cn } from '@/lib/utils'
import { Icon, type IconName } from '@/components/ui/icon'

interface SidebarState {
  collapsed: boolean
  toggle: () => void
}

export const useSidebarStore = create<SidebarState>()(
  persist(
    (set) => ({
      collapsed: false,
      toggle: () => set((state) => ({ collapsed: !state.collapsed })),
    }),
    { name: 'petalflow-sidebar' }
  )
)

interface NavItem {
  id: string
  icon: IconName
  label: string
  path: string
}

const NAV_ITEMS: NavItem[] = [
  { id: 'workflows', icon: 'workflows', label: 'Workflows', path: '/workflows' },
  { id: 'designer', icon: 'designer', label: 'Designer', path: '/designer' },
  { id: 'runs', icon: 'runs', label: 'Runs', path: '/runs' },
  { id: 'tools', icon: 'tools', label: 'Tools', path: '/tools' },
  { id: 'providers', icon: 'providers', label: 'Providers', path: '/providers' },
  { id: 'settings', icon: 'settings', label: 'Settings', path: '/settings' },
]

export function Sidebar() {
  const collapsed = useSidebarStore((s) => s.collapsed)
  const toggle = useSidebarStore((s) => s.toggle)
  const routerState = useRouterState()
  const currentPath = routerState.location.pathname

  const getActiveId = () => {
    for (const item of NAV_ITEMS) {
      if (currentPath.startsWith(item.path)) {
        return item.id
      }
    }
    return 'workflows'
  }

  const activeId = getActiveId()

  return (
    <nav
      className={cn(
        'flex flex-col bg-surface-0 border-r border-border transition-all duration-200 overflow-hidden',
        collapsed ? 'w-14 min-w-14' : 'w-[200px] min-w-[200px]'
      )}
    >
      {/* Logo */}
      <div
        className={cn(
          'flex items-center gap-2.5 border-b border-border min-h-14',
          collapsed ? 'px-3 py-4' : 'px-4 py-4'
        )}
      >
        <div className="w-7 h-7 rounded-lg bg-gradient-to-br from-primary to-teal flex items-center justify-center text-sm text-white font-extrabold shrink-0">
          P
        </div>
        {!collapsed && (
          <span className="font-bold text-[15px] text-foreground whitespace-nowrap">
            PetalFlow
          </span>
        )}
      </div>

      {/* Navigation */}
      <div className="flex-1 p-2">
        {NAV_ITEMS.map((item) => {
          const isActive = activeId === item.id
          return (
            <Link
              key={item.id}
              to={item.path}
              className={cn(
                'flex items-center gap-2.5 w-full rounded-lg cursor-pointer transition-all text-[13px] font-medium',
                collapsed ? 'px-2 py-2.5 justify-center' : 'px-2.5 py-2.5',
                isActive
                  ? 'bg-surface-active text-foreground font-semibold'
                  : 'text-muted-foreground hover:bg-surface-active/50 hover:text-foreground'
              )}
            >
              <Icon name={item.icon} size={18} />
              {!collapsed && item.label}
            </Link>
          )
        })}
      </div>

      {/* Collapse toggle */}
      <button
        onClick={toggle}
        className="p-3.5 border-t border-border text-muted-foreground hover:text-foreground flex justify-center"
      >
        <div
          className={cn(
            'transition-transform duration-200',
            collapsed ? 'rotate-0' : 'rotate-180'
          )}
        >
          <Icon name="chevron" size={16} />
        </div>
      </button>
    </nav>
  )
}
