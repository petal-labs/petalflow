import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { useWorkflowStore } from '@/stores/workflow'
import { Icon } from '@/components/ui/icon'
import { AgentDesigner } from '@/components/designer'
import { cn } from '@/lib/utils'

export const Route = createFileRoute('/_app/designer/')({
  component: DesignerPage,
})

function DesignerPage() {
  const activeWorkflow = useWorkflowStore((s) => s.activeWorkflow)
  const navigate = useNavigate()

  if (!activeWorkflow) {
    return (
      <div className="flex flex-col items-center justify-center h-full text-center px-4">
        <div className="w-14 h-14 mb-4 rounded-full bg-surface-2 flex items-center justify-center">
          <Icon name="designer" size={24} className="text-muted-foreground" />
        </div>
        <h3 className="text-lg font-semibold text-foreground mb-1">
          No workflow selected
        </h3>
        <p className="text-sm text-muted-foreground mb-4 max-w-sm">
          Select a workflow from the library to start editing, or create a new one.
        </p>
        <button
          onClick={() => navigate({ to: '/workflows' })}
          className={cn(
            'inline-flex items-center gap-1.5 rounded-lg font-semibold transition-all',
            'text-[13px] px-3.5 py-2',
            'bg-primary text-white hover:bg-primary/90'
          )}
        >
          <Icon name="workflows" size={15} />
          Browse Workflows
        </button>
      </div>
    )
  }

  // Render the appropriate designer based on workflow kind
  if (activeWorkflow.kind === 'agent_workflow') {
    return <AgentDesigner />
  }

  // Graph Designer placeholder - will be built in Phase 6
  return (
    <div className="flex h-full overflow-hidden">
      {/* Canvas placeholder */}
      <div className="flex-1 flex flex-col bg-surface-1">
        <div className="px-5 py-4 border-b border-border bg-surface-0">
          <div className="flex items-center gap-3">
            <Icon name="file" size={16} className="text-muted-foreground" />
            <span className="font-semibold text-foreground">{activeWorkflow.name}</span>
            <span className="px-2 py-0.5 rounded-md text-[11px] font-semibold bg-teal-soft text-teal">
              graph
            </span>
          </div>
          <p className="text-xs text-muted-foreground mt-1">
            Graph Designer • Visual node-based editor
          </p>
        </div>
        <div className="flex-1 flex items-center justify-center text-muted-foreground text-sm">
          <div className="text-center">
            <Icon name="git" size={48} className="mx-auto mb-3 opacity-30" />
            <p className="font-medium">React Flow Canvas</p>
            <p className="text-xs mt-1">Coming in Phase 6</p>
          </div>
        </div>
      </div>

      {/* Node inspector placeholder */}
      <div className="w-80 border-l border-border bg-surface-0 flex flex-col">
        <div className="px-4 py-3 border-b border-border">
          <span className="text-xs text-muted-foreground uppercase tracking-wide font-semibold">
            Node Inspector
          </span>
        </div>
        <div className="flex-1 flex items-center justify-center p-4 text-center text-sm text-muted-foreground">
          Select a node to configure
        </div>
      </div>
    </div>
  )
}
