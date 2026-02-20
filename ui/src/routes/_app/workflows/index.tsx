import { useEffect } from 'react'
import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { useWorkflowStore } from '@/stores/workflow'
import { useUIStore } from '@/stores/ui'
import { Icon } from '@/components/ui/icon'
import { WorkflowKindBadge, Badge } from '@/components/ui/badge'
import { cn } from '@/lib/utils'
import type { Workflow } from '@/lib/api-types'

export const Route = createFileRoute('/_app/workflows/')({
  component: WorkflowsPage,
})

function WorkflowsPage() {
  const workflows = useWorkflowStore((s) => s.workflows)
  const loading = useWorkflowStore((s) => s.loading)
  const error = useWorkflowStore((s) => s.error)
  const fetchWorkflows = useWorkflowStore((s) => s.fetchWorkflows)
  const openCreateModal = useUIStore((s) => s.openCreateWorkflowModal)

  useEffect(() => {
    fetchWorkflows()
  }, [fetchWorkflows])

  return (
    <div className="p-7 max-w-[900px]">
      {/* Header */}
      <div className="flex justify-between items-center mb-6">
        <div>
          <h1 className="text-xl font-bold text-foreground">Workflows</h1>
          <p className="text-sm text-muted-foreground mt-1">
            {workflows.length} workflow{workflows.length !== 1 ? 's' : ''} in library
          </p>
        </div>
        <Button variant="primary" icon="plus" onClick={openCreateModal}>
          New Workflow
        </Button>
      </div>

      {/* Error state */}
      {error && (
        <div className="mb-4 p-3 rounded-lg bg-red-soft text-red text-sm">
          {error}
        </div>
      )}

      {/* Loading state */}
      {loading && workflows.length === 0 && (
        <div className="text-center py-12 text-muted-foreground">
          Loading workflows...
        </div>
      )}

      {/* Empty state */}
      {!loading && workflows.length === 0 && (
        <EmptyState onCreateClick={openCreateModal} />
      )}

      {/* Workflow grid */}
      {workflows.length > 0 && (
        <div className="grid grid-cols-[repeat(auto-fill,minmax(280px,1fr))] gap-3.5">
          {workflows.map((workflow) => (
            <WorkflowCard key={workflow.id} workflow={workflow} />
          ))}
        </div>
      )}

      {/* Create Workflow Modal */}
      <CreateWorkflowModal />
    </div>
  )
}

// Workflow card component
function WorkflowCard({ workflow }: { workflow: Workflow }) {
  const navigate = useNavigate()
  const setActiveWorkflow = useWorkflowStore((s) => s.setActiveWorkflow)

  const handleClick = () => {
    setActiveWorkflow(workflow)
    navigate({ to: '/designer' })
  }

  // Parse the source to get stats
  const stats = getWorkflowStats(workflow)

  return (
    <div
      onClick={handleClick}
      className={cn(
        'bg-surface-1 border border-border rounded-xl p-[18px] cursor-pointer',
        'transition-all duration-150',
        'hover:border-primary hover:shadow-[0_0_0_1px_hsl(var(--primary))]'
      )}
    >
      <div className="flex justify-between items-start mb-2.5">
        <div className="font-bold text-sm text-foreground truncate pr-2">
          {workflow.name}
        </div>
        <WorkflowKindBadge kind={workflow.kind} />
      </div>
      <div className="flex gap-3 text-xs text-muted-foreground">
        {workflow.kind === 'agent_workflow' ? (
          <>
            <span>{stats.agents} agent{stats.agents !== 1 ? 's' : ''}</span>
            <span>{stats.tasks} task{stats.tasks !== 1 ? 's' : ''}</span>
          </>
        ) : (
          <>
            <span>{stats.nodes} node{stats.nodes !== 1 ? 's' : ''}</span>
            <span>{stats.edges} edge{stats.edges !== 1 ? 's' : ''}</span>
          </>
        )}
        <span className="ml-auto">{formatRelativeTime(workflow.updated_at)}</span>
      </div>
    </div>
  )
}

// Empty state when no workflows exist
function EmptyState({ onCreateClick }: { onCreateClick: () => void }) {
  return (
    <div className="text-center py-16 px-4">
      <div className="w-14 h-14 mx-auto mb-4 rounded-full bg-surface-2 flex items-center justify-center">
        <Icon name="workflows" size={24} className="text-muted-foreground" />
      </div>
      <h3 className="text-lg font-semibold text-foreground mb-1">
        No workflows yet
      </h3>
      <p className="text-sm text-muted-foreground mb-6 max-w-sm mx-auto">
        Create your first workflow to get started. Choose between an Agent workflow
        for structured tasks or a Graph workflow for visual pipelines.
      </p>
      <Button variant="primary" icon="plus" onClick={onCreateClick}>
        Create Workflow
      </Button>
    </div>
  )
}

// Create Workflow Modal
function CreateWorkflowModal() {
  const isOpen = useUIStore((s) => s.createWorkflowModalOpen)
  const closeModal = useUIStore((s) => s.closeCreateWorkflowModal)
  const createAgentWorkflow = useWorkflowStore((s) => s.createAgentWorkflow)
  const createGraphWorkflow = useWorkflowStore((s) => s.createGraphWorkflow)
  const setActiveWorkflow = useWorkflowStore((s) => s.setActiveWorkflow)
  const navigate = useNavigate()

  const handleCreateAgent = async () => {
    const workflow = await createAgentWorkflow({
      version: '1.0',
      kind: 'agent_workflow',
      id: `workflow-${Date.now()}`,
      name: 'New Agent Workflow',
      agents: {},
      tasks: {},
      execution: { strategy: 'sequential' },
    })
    closeModal()
    setActiveWorkflow(workflow)
    navigate({ to: '/designer' })
  }

  const handleCreateGraph = async () => {
    const workflow = await createGraphWorkflow({
      id: `graph-${Date.now()}`,
      version: '1.0',
      nodes: [],
      edges: [],
      entry: '',
    })
    closeModal()
    setActiveWorkflow(workflow)
    navigate({ to: '/designer' })
  }

  if (!isOpen) return null

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
      onClick={closeModal}
    >
      <div
        className="bg-surface-0 rounded-xl shadow-lg border border-border p-6 w-full max-w-md"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex justify-between items-center mb-4">
          <h2 className="text-lg font-bold text-foreground">Create Workflow</h2>
          <button
            onClick={closeModal}
            className="p-1 text-muted-foreground hover:text-foreground transition-colors"
          >
            <Icon name="x" size={18} />
          </button>
        </div>

        <p className="text-sm text-muted-foreground mb-6">
          Choose a workflow type to get started.
        </p>

        <div className="space-y-3">
          <WorkflowTypeOption
            title="Agent Workflow"
            description="Structured tasks with agents. Define agents, assign tasks, and configure execution strategy."
            badge="agent"
            onClick={handleCreateAgent}
          />
          <WorkflowTypeOption
            title="Graph Workflow"
            description="Visual node-based pipeline. Connect nodes with edges for maximum flexibility."
            badge="graph"
            onClick={handleCreateGraph}
          />
        </div>

        <div className="mt-6 flex justify-end">
          <Button variant="ghost" onClick={closeModal}>
            Cancel
          </Button>
        </div>
      </div>
    </div>
  )
}

// Workflow type option in create modal
function WorkflowTypeOption({
  title,
  description,
  badge,
  onClick,
}: {
  title: string
  description: string
  badge: 'agent' | 'graph'
  onClick: () => void
}) {
  return (
    <button
      onClick={onClick}
      className={cn(
        'w-full text-left p-4 rounded-lg border border-border bg-surface-1',
        'transition-all duration-150',
        'hover:border-primary hover:shadow-[0_0_0_1px_hsl(var(--primary))]'
      )}
    >
      <div className="flex justify-between items-start mb-1">
        <span className="font-semibold text-sm text-foreground">{title}</span>
        <Badge variant={badge}>{badge}</Badge>
      </div>
      <p className="text-xs text-muted-foreground">{description}</p>
    </button>
  )
}

// Button component (reused from top-bar pattern)
interface ButtonProps {
  children: React.ReactNode
  variant?: 'primary' | 'secondary' | 'ghost'
  icon?: 'plus' | 'play'
  onClick?: () => void
  disabled?: boolean
}

function Button({
  children,
  variant = 'secondary',
  icon,
  onClick,
  disabled,
}: ButtonProps) {
  return (
    <button
      onClick={onClick}
      disabled={disabled}
      className={cn(
        'inline-flex items-center gap-1.5 rounded-lg font-semibold transition-all',
        'text-[13px] px-3.5 py-2',
        disabled && 'opacity-50 cursor-not-allowed',
        variant === 'primary' && 'bg-primary text-white hover:bg-primary/90',
        variant === 'secondary' &&
          'bg-surface-2 text-foreground border border-border hover:bg-surface-active',
        variant === 'ghost' &&
          'bg-transparent text-muted-foreground hover:text-foreground'
      )}
    >
      {icon && <Icon name={icon} size={15} />}
      {children}
    </button>
  )
}

// Helper to extract workflow stats from source
function getWorkflowStats(workflow: Workflow): {
  agents: number
  tasks: number
  nodes: number
  edges: number
} {
  if (workflow.kind === 'agent_workflow') {
    try {
      // Try to parse as JSON first, then YAML structure
      const parsed = JSON.parse(workflow.source)
      return {
        agents: Object.keys(parsed.agents || {}).length,
        tasks: Object.keys(parsed.tasks || {}).length,
        nodes: 0,
        edges: 0,
      }
    } catch {
      // Fallback: count from YAML (simplified)
      const agentMatches = workflow.source.match(/agents:/g)
      const taskMatches = workflow.source.match(/tasks:/g)
      return {
        agents: agentMatches ? 1 : 0,
        tasks: taskMatches ? 1 : 0,
        nodes: 0,
        edges: 0,
      }
    }
  } else {
    // Graph workflow
    if (workflow.compiled) {
      return {
        agents: 0,
        tasks: 0,
        nodes: workflow.compiled.nodes?.length || 0,
        edges: workflow.compiled.edges?.length || 0,
      }
    }
    try {
      const parsed = JSON.parse(workflow.source)
      return {
        agents: 0,
        tasks: 0,
        nodes: parsed.nodes?.length || 0,
        edges: parsed.edges?.length || 0,
      }
    } catch {
      return { agents: 0, tasks: 0, nodes: 0, edges: 0 }
    }
  }
}

// Format relative time (simplified)
function formatRelativeTime(isoString: string): string {
  const date = new Date(isoString)
  const now = new Date()
  const diffMs = now.getTime() - date.getTime()
  const diffSec = Math.floor(diffMs / 1000)
  const diffMin = Math.floor(diffSec / 60)
  const diffHour = Math.floor(diffMin / 60)
  const diffDay = Math.floor(diffHour / 24)

  if (diffSec < 60) return 'just now'
  if (diffMin < 60) return `${diffMin}m ago`
  if (diffHour < 24) return `${diffHour}h ago`
  if (diffDay < 7) return `${diffDay}d ago`

  return date.toLocaleDateString()
}
