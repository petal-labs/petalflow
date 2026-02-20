import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { useWorkflowStore } from '@/stores/workflow'
import { Icon } from '@/components/ui/icon'
import { AgentDesigner, GraphDesigner } from '@/components/designer'
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

  // Render Graph Designer for graph workflows
  return <GraphDesigner />
}
