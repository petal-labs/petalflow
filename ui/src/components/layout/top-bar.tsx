import { useSettingsStore } from '@/stores/settings'
import { useWorkflowStore } from '@/stores/workflow'
import { useUIStore } from '@/stores/ui'
import { Icon } from '@/components/ui/icon'
import { WorkflowKindBadge } from '@/components/ui/badge'
import { cn } from '@/lib/utils'

export function TopBar() {
  const theme = useSettingsStore((s) => s.theme)
  const setTheme = useSettingsStore((s) => s.setTheme)

  const activeWorkflow = useWorkflowStore((s) => s.activeWorkflow)
  const isDirty = useWorkflowStore((s) => s.isDirty)
  const activeSource = useWorkflowStore((s) => s.activeSource)
  const validateWorkflow = useWorkflowStore((s) => s.validateWorkflow)
  const validationResult = useWorkflowStore((s) => s.validationResult)

  const openRunModal = useUIStore((s) => s.openRunModal)

  const toggleTheme = () => {
    if (theme === 'dark') {
      setTheme('light')
    } else {
      setTheme('dark')
    }
  }

  const isDark = theme === 'dark' || (theme === 'system' && window.matchMedia('(prefers-color-scheme: dark)').matches)

  const handleValidate = async () => {
    if (activeSource) {
      await validateWorkflow(activeSource)
    }
  }

  const handleRun = () => {
    if (activeWorkflow) {
      openRunModal()
    }
  }

  return (
    <header className="h-[52px] min-h-[52px] flex items-center justify-between px-5 border-b border-border bg-surface-0">
      {/* Left: Active workflow info */}
      <div className="flex items-center gap-2.5">
        {activeWorkflow ? (
          <>
            <Icon name="file" size={15} className="text-muted-foreground" />
            <span className="font-semibold text-sm text-foreground">{activeWorkflow.name}</span>
            <WorkflowKindBadge kind={activeWorkflow.kind} />
            {isDirty && (
              <span
                className="w-[7px] h-[7px] rounded-full bg-amber ml-0.5"
                title="Unsaved changes"
              />
            )}
            {validationResult && !validationResult.valid && (
              <span
                className="w-[7px] h-[7px] rounded-full bg-red ml-0.5"
                title="Validation errors"
              />
            )}
          </>
        ) : (
          <span className="text-sm text-muted-foreground">No workflow selected</span>
        )}
      </div>

      {/* Right: Actions */}
      <div className="flex items-center gap-2">
        <Button
          variant="secondary"
          size="sm"
          onClick={handleValidate}
          disabled={!activeSource}
        >
          Validate
        </Button>
        <Button variant="secondary" size="sm" disabled={!activeSource}>
          Compile
        </Button>
        <Button
          variant="primary"
          size="sm"
          icon="play"
          onClick={handleRun}
          disabled={!activeWorkflow}
        >
          Run
        </Button>

        <div className="w-px h-6 bg-border mx-1" />

        <button
          onClick={toggleTheme}
          className="p-1.5 text-muted-foreground hover:text-foreground transition-colors"
          title={`Switch to ${isDark ? 'light' : 'dark'} mode`}
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

// Button component for the top bar
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
