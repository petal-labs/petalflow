import { useState, useRef, useEffect, useMemo, useCallback } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { useSettingsStore } from '@/stores/settings'
import { useWorkflowStore } from '@/stores/workflow'
import { useUIStore } from '@/stores/ui'
import { useAuthStore } from '@/stores/auth'
import { useRunStore } from '@/stores/run'
import { Icon } from '@/components/ui/icon'
import { WorkflowKindBadge } from '@/components/ui/badge'
import { cn } from '@/lib/utils'

function parseNodeLabel(nodeID: string): string {
  const [taskPart, agentPart] = nodeID.split('__')
  if (!taskPart) {
    return nodeID
  }
  return agentPart ? `${taskPart} (${agentPart})` : taskPart
}

function formatDuration(ms: number): string {
  if (ms < 1000) return `${ms}ms`
  if (ms < 60000) return `${(ms / 1000).toFixed(1)}s`
  return `${Math.floor(ms / 60000)}m ${Math.floor((ms % 60000) / 1000)}s`
}

export function TopBar() {
  const navigate = useNavigate()
  const theme = useSettingsStore((s) => s.theme)
  const setTheme = useSettingsStore((s) => s.setTheme)

  const user = useAuthStore((s) => s.user)
  const isAuthenticated = useAuthStore((s) => s.isAuthenticated)
  const logout = useAuthStore((s) => s.logout)
  const runs = useRunStore((s) => s.runs)
  const activeRun = useRunStore((s) => s.activeRun)
  const events = useRunStore((s) => s.events)

  const [showUserMenu, setShowUserMenu] = useState(false)
  const userMenuRef = useRef<HTMLDivElement>(null)
  const [tick, setTick] = useState(() => Date.now())

  // Close menu on click outside
  useEffect(() => {
    function handleClickOutside(e: MouseEvent) {
      if (userMenuRef.current && !userMenuRef.current.contains(e.target as Node)) {
        setShowUserMenu(false)
      }
    }
    document.addEventListener('mousedown', handleClickOutside)
    return () => document.removeEventListener('mousedown', handleClickOutside)
  }, [])

  const activeWorkflow = useWorkflowStore((s) => s.activeWorkflow)
  const isDirty = useWorkflowStore((s) => s.isDirty)
  const activeSource = useWorkflowStore((s) => s.activeSource)
  const validateWorkflow = useWorkflowStore((s) => s.validateWorkflow)
  const persistActiveWorkflow = useWorkflowStore((s) => s.persistActiveWorkflow)
  const validationResult = useWorkflowStore((s) => s.validationResult)
  const [saving, setSaving] = useState(false)
  const [actionNotice, setActionNotice] = useState<string | null>(null)
  const [actionError, setActionError] = useState<string | null>(null)
  const lastAutoSaveAttemptKeyRef = useRef<string | null>(null)
  const savePromiseRef = useRef<Promise<boolean> | null>(null)

  const openRunModal = useUIStore((s) => s.openRunModal)

  const toggleTheme = () => {
    if (theme === 'dark') {
      setTheme('light')
    } else {
      setTheme('dark')
    }
  }

  const isDark = theme === 'dark' || (theme === 'system' && window.matchMedia('(prefers-color-scheme: dark)').matches)

  const runningRun = useMemo(() => {
    if (activeRun?.status === 'running') {
      return activeRun
    }

    let latest: (typeof runs)[number] | null = null
    for (const run of runs) {
      if (run.status !== 'running') {
        continue
      }
      if (!latest || new Date(run.started_at).getTime() > new Date(latest.started_at).getTime()) {
        latest = run
      }
    }
    return latest
  }, [activeRun, runs])

  const currentNode = useMemo(() => {
    if (!runningRun) {
      return null
    }

    const nodeStates = new Map<string, { label: string; startedAt: string; status: 'running' | 'done' }>()
    for (const event of events) {
      if (event.run_id !== runningRun.run_id || !event.node_id) {
        continue
      }

      if (event.event_type === 'node.started') {
        nodeStates.set(event.node_id, {
          label: parseNodeLabel(event.node_id),
          startedAt: event.timestamp,
          status: 'running',
        })
        continue
      }

      if (event.event_type === 'node.finished' || event.event_type === 'node.failed') {
        const previous = nodeStates.get(event.node_id)
        if (previous) {
          nodeStates.set(event.node_id, {
            ...previous,
            status: 'done',
          })
        }
      }
    }

    let newestRunning: { label: string; startedAt: string } | null = null
    for (const candidate of nodeStates.values()) {
      if (candidate.status !== 'running') {
        continue
      }
      if (!newestRunning || new Date(candidate.startedAt).getTime() > new Date(newestRunning.startedAt).getTime()) {
        newestRunning = candidate
      }
    }

    return newestRunning
  }, [events, runningRun])

  const runElapsedMs = useMemo(() => {
    if (!runningRun) {
      return 0
    }
    const start = new Date(runningRun.started_at).getTime()
    return Math.max(0, tick - start)
  }, [runningRun, tick])

  const taskElapsedMs = useMemo(() => {
    if (!currentNode) {
      return 0
    }
    const start = new Date(currentNode.startedAt).getTime()
    return Math.max(0, tick - start)
  }, [currentNode, tick])

  useEffect(() => {
    if (!runningRun) {
      return undefined
    }
    const intervalID = window.setInterval(() => setTick(Date.now()), 1000)
    return () => window.clearInterval(intervalID)
  }, [runningRun])

  const handleValidate = async () => {
    if (activeSource) {
      // Convert to string if needed - backend expects JSON string
      const sourceStr = typeof activeSource === 'string'
        ? activeSource
        : JSON.stringify(activeSource)
      setActionError(null)
      const result = await validateWorkflow(sourceStr)
      if (result.valid) {
        setActionNotice('Validation passed.')
      } else {
        setActionNotice(null)
        setActionError('Validation failed. Fix errors before saving or running.')
      }
    }
  }

  const persistWithLock = useCallback((): Promise<boolean> => {
    if (savePromiseRef.current) {
      return savePromiseRef.current
    }

    setSaving(true)
    const savePromise = persistActiveWorkflow()
      .finally(() => {
        savePromiseRef.current = null
        setSaving(false)
      })
    savePromiseRef.current = savePromise
    return savePromise
  }, [persistActiveWorkflow])

  const handleRun = () => {
    if (!activeWorkflow) {
      return
    }

    setActionError(null)
    setActionNotice(null)

    if (isDirty || savePromiseRef.current) {
      void persistWithLock()
        .then((saved) => {
          if (!saved) {
            setActionError('Cannot run: fix workflow validation errors and try again.')
            return
          }
          openRunModal()
        })
        .catch((err) => {
          setActionError((err as Error).message || 'Failed to save workflow before running.')
        })
      return
    }

    openRunModal()
  }

  const handleSave = async () => {
    setActionError(null)
    setActionNotice(null)
    try {
      const saved = await persistWithLock()
      if (!saved) {
        setActionError('Cannot save: fix workflow validation errors and try again.')
      } else {
        setActionNotice('Saved.')
      }
    } catch (err) {
      setActionError((err as Error).message || 'Failed to save workflow.')
    }
  }

  // Autosave dirty workflows after a short idle period.
  useEffect(() => {
    if (!activeWorkflow || !activeSource || !isDirty || saving) {
      return
    }

    const sourceStr = typeof activeSource === 'string'
      ? activeSource
      : JSON.stringify(activeSource)
    const attemptKey = `${activeWorkflow.id}:${sourceStr}`
    if (lastAutoSaveAttemptKeyRef.current === attemptKey) {
      return
    }

    const timerID = window.setTimeout(() => {
      lastAutoSaveAttemptKeyRef.current = attemptKey
      void persistWithLock()
        .catch(() => {
          setActionError('Autosave failed. Use Save to retry.')
        })
    }, 800)

    return () => {
      window.clearTimeout(timerID)
    }
  }, [activeWorkflow, activeSource, isDirty, persistWithLock, saving])

  return (
    <header className="h-[52px] min-h-[52px] flex items-center gap-3 px-5 border-b border-border bg-surface-0">
      {/* Left: Active workflow info */}
      <div className="flex items-center gap-2.5 min-w-0 flex-1">
        {activeWorkflow ? (
          <>
            <Icon name="file" size={15} className="text-muted-foreground" />
            <span className="font-semibold text-sm text-foreground truncate">{activeWorkflow.name}</span>
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

      {runningRun && (
        <button
          type="button"
          onClick={() => {
            navigate({ to: '/runs', search: { viewRun: runningRun.run_id } })
          }}
          className={cn(
            'hidden lg:flex items-center gap-2 px-3 py-1.5 rounded-lg border border-border bg-surface-1',
            'hover:bg-surface-active transition-colors max-w-[460px]'
          )}
          title={`View run ${runningRun.run_id}`}
        >
          <span className="inline-flex items-center gap-1.5 min-w-0">
            <span className="h-2 w-2 rounded-full bg-blue animate-pulse shrink-0" />
            <span className="text-[11px] text-muted-foreground shrink-0">Now working on:</span>
            <span className="text-xs font-semibold text-foreground truncate">
              {currentNode?.label || 'Starting run...'}
            </span>
          </span>
          <span className="text-[11px] text-muted-foreground shrink-0">
            {formatDuration(currentNode ? taskElapsedMs : runElapsedMs)}
          </span>
        </button>
      )}

      {/* Right: Actions */}
      <div className="flex items-center gap-2 shrink-0">
        <Button
          variant="secondary"
          size="sm"
          onClick={handleValidate}
          disabled={!activeSource}
        >
          Validate
        </Button>
        <Button
          variant="secondary"
          size="sm"
          onClick={handleSave}
          disabled={!activeWorkflow || !activeSource || !isDirty || saving}
        >
          {saving ? 'Saving...' : 'Save'}
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
        {actionNotice && !actionError && (
          <span className="text-xs text-muted-foreground max-w-[180px] truncate" title={actionNotice}>
            {actionNotice}
          </span>
        )}
        {actionError && (
          <span className="text-xs text-red max-w-[280px] truncate" title={actionError}>
            {actionError}
          </span>
        )}

        <div className="w-px h-6 bg-border mx-1" />

        <button
          onClick={toggleTheme}
          className="p-1.5 text-muted-foreground hover:text-foreground transition-colors"
          title={`Switch to ${isDark ? 'light' : 'dark'} mode`}
        >
          <Icon name={isDark ? 'sun' : 'moon'} size={16} />
        </button>

        {/* User Menu */}
        <div className="relative" ref={userMenuRef}>
          <button
            onClick={() => setShowUserMenu(!showUserMenu)}
            className="flex items-center gap-2 p-1 rounded-lg hover:bg-surface-1 transition-colors"
          >
            {isAuthenticated && user?.avatar ? (
              <img
                src={user.avatar}
                alt={user.name}
                className="w-7 h-7 rounded-full"
              />
            ) : (
              <div className="w-7 h-7 rounded-full bg-surface-2 flex items-center justify-center">
                <Icon name="user" size={14} className="text-muted-foreground" />
              </div>
            )}
          </button>

          {showUserMenu && (
            <div className="absolute right-0 top-full mt-1 w-56 py-1 bg-surface-0 border border-border rounded-lg shadow-lg z-50">
              {isAuthenticated && user ? (
                <>
                  <div className="px-3 py-2 border-b border-border">
                    <div className="font-medium text-sm text-foreground">{user.name}</div>
                    <div className="text-xs text-muted-foreground">{user.email}</div>
                  </div>
                  <a
                    href="/settings"
                    className="flex items-center gap-2 px-3 py-2 text-sm text-foreground hover:bg-surface-1 transition-colors"
                    onClick={() => setShowUserMenu(false)}
                  >
                    <Icon name="settings" size={14} />
                    Settings
                  </a>
                  <button
                    onClick={() => {
                      logout()
                      setShowUserMenu(false)
                      window.location.href = '/login'
                    }}
                    className="w-full flex items-center gap-2 px-3 py-2 text-sm text-foreground hover:bg-surface-1 transition-colors"
                  >
                    <Icon name="logout" size={14} />
                    Sign out
                  </button>
                </>
              ) : (
                <>
                  <a
                    href="/login"
                    className="flex items-center gap-2 px-3 py-2 text-sm text-foreground hover:bg-surface-1 transition-colors"
                    onClick={() => setShowUserMenu(false)}
                  >
                    <Icon name="user" size={14} />
                    Sign in
                  </a>
                </>
              )}
            </div>
          )}
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
