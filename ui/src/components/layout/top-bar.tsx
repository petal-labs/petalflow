import { useSettingsStore } from '@/stores/settings'
import { Icon } from '@/components/ui/icon'
import { cn } from '@/lib/utils'

export function TopBar() {
  const theme = useSettingsStore((s) => s.theme)
  const setTheme = useSettingsStore((s) => s.setTheme)

  const toggleTheme = () => {
    if (theme === 'dark') {
      setTheme('light')
    } else {
      setTheme('dark')
    }
  }

  const isDark = theme === 'dark' || (theme === 'system' && window.matchMedia('(prefers-color-scheme: dark)').matches)

  return (
    <header className="h-[52px] min-h-[52px] flex items-center justify-between px-5 border-b border-border bg-surface-0">
      {/* Left: Active workflow info (placeholder for now) */}
      <div className="flex items-center gap-2.5">
        {/* Workflow info will be populated when a workflow is active */}
      </div>

      {/* Right: Actions */}
      <div className="flex items-center gap-2">
        <Button variant="secondary" size="sm">
          Validate
        </Button>
        <Button variant="secondary" size="sm">
          Compile
        </Button>
        <Button variant="primary" size="sm" icon="play">
          Run
        </Button>

        <div className="w-px h-6 bg-border mx-1" />

        <button
          onClick={toggleTheme}
          className="p-1.5 text-muted-foreground hover:text-foreground"
        >
          <Icon name={isDark ? 'sun' : 'moon'} size={16} />
        </button>

        <div className="w-7 h-7 rounded-full bg-surface-2 flex items-center justify-center">
          <Icon name="user" size={14} className="text-muted-foreground" />
        </div>
      </div>
    </header>
  )
}

// Simple button component for the top bar
interface ButtonProps {
  children: React.ReactNode
  variant?: 'primary' | 'secondary' | 'ghost'
  size?: 'sm' | 'md'
  icon?: 'play'
  onClick?: () => void
  disabled?: boolean
}

function Button({ children, variant = 'secondary', size = 'md', icon, onClick, disabled }: ButtonProps) {
  return (
    <button
      onClick={onClick}
      disabled={disabled}
      className={cn(
        'inline-flex items-center gap-1.5 rounded-lg font-semibold transition-all',
        size === 'sm' ? 'text-xs px-2.5 py-1.5' : 'text-[13px] px-3.5 py-2',
        disabled && 'opacity-50 cursor-not-allowed',
        variant === 'primary' && 'bg-primary text-white hover:bg-primary/90',
        variant === 'secondary' && 'bg-surface-2 text-foreground border border-border hover:bg-surface-active',
        variant === 'ghost' && 'bg-transparent text-muted-foreground hover:text-foreground'
      )}
    >
      {icon && <Icon name={icon} size={size === 'sm' ? 13 : 15} />}
      {children}
    </button>
  )
}
