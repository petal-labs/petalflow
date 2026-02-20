import { cn } from '@/lib/utils'

export type BadgeVariant =
  | 'default'
  | 'agent'
  | 'graph'
  | 'success'
  | 'failed'
  | 'canceled'
  | 'running'
  | 'ready'
  | 'unhealthy'
  | 'disabled'
  | 'native'
  | 'mcp'
  | 'http'
  | 'stdio'
  | 'connected'
  | 'disconnected'
  | 'draft'

interface BadgeProps {
  children: React.ReactNode
  variant?: BadgeVariant
  className?: string
}

const variantStyles: Record<BadgeVariant, string> = {
  default: 'bg-muted text-muted-foreground',
  agent: 'bg-accent-soft text-primary',
  graph: 'bg-teal-soft text-teal',
  success: 'bg-green-soft text-green',
  failed: 'bg-red-soft text-red',
  canceled: 'bg-muted text-muted-foreground',
  running: 'bg-accent-soft text-primary',
  ready: 'bg-green-soft text-green',
  unhealthy: 'bg-red-soft text-red',
  disabled: 'bg-muted text-muted-foreground',
  native: 'bg-accent-soft text-primary',
  mcp: 'bg-teal-soft text-teal',
  http: 'bg-amber-soft text-amber',
  stdio: 'bg-muted text-muted-foreground',
  connected: 'bg-green-soft text-green',
  disconnected: 'bg-red-soft text-red',
  draft: 'bg-muted text-muted-foreground',
}

export function Badge({ children, variant = 'default', className }: BadgeProps) {
  return (
    <span
      className={cn(
        'inline-flex items-center gap-1 px-2 py-0.5 rounded-md text-[11px] font-semibold tracking-wide whitespace-nowrap leading-[18px]',
        variantStyles[variant],
        className
      )}
    >
      {children}
    </span>
  )
}

// Convenience badge for workflow kinds
export function WorkflowKindBadge({ kind }: { kind: 'agent_workflow' | 'graph' }) {
  return (
    <Badge variant={kind === 'agent_workflow' ? 'agent' : 'graph'}>
      {kind === 'agent_workflow' ? 'agent' : 'graph'}
    </Badge>
  )
}

// Convenience badge for run status
export function RunStatusBadge({ status }: { status: 'running' | 'success' | 'failed' | 'canceled' }) {
  return <Badge variant={status}>{status}</Badge>
}

// Convenience badge for tool origin
export function ToolOriginBadge({ origin }: { origin: 'native' | 'mcp' | 'http' | 'stdio' }) {
  return <Badge variant={origin}>{origin}</Badge>
}

// Convenience badge for tool/provider status
export function StatusBadge({ status }: { status: 'ready' | 'unhealthy' | 'disabled' | 'connected' | 'disconnected' }) {
  return <Badge variant={status}>{status}</Badge>
}
