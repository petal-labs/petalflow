import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { useWorkflowStore } from '@/stores/workflow'
import { Icon } from '@/components/ui/icon'
import { WorkflowKindBadge } from '@/components/ui/badge'
import { cn } from '@/lib/utils'

export const Route = createFileRoute('/_app/designer/')({
  component: DesignerPage,
})

function DesignerPage() {
  const activeWorkflow = useWorkflowStore((s) => s.activeWorkflow)
  const activeSource = useWorkflowStore((s) => s.activeSource)
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

  return (
    <div className="flex h-full overflow-hidden">
      {/* Left panel - will be forms in Phase 5, source preview for now */}
      <div className="flex-1 flex flex-col overflow-hidden">
        {/* Workflow info header */}
        <div className="px-5 py-4 border-b border-border bg-surface-0">
          <div className="flex items-center gap-3">
            <Icon name="file" size={16} className="text-muted-foreground" />
            <span className="font-semibold text-foreground">{activeWorkflow.name}</span>
            <WorkflowKindBadge kind={activeWorkflow.kind} />
          </div>
          <p className="text-xs text-muted-foreground mt-1">
            {activeWorkflow.kind === 'agent_workflow'
              ? 'Agent/Task Designer • Structured forms and task graph'
              : 'Graph Designer • Visual node-based editor'}
          </p>
        </div>

        {/* Designer mode tabs - placeholder for Phase 5/6 */}
        <div className="flex border-b border-border bg-surface-0">
          {activeWorkflow.kind === 'agent_workflow' ? (
            <>
              <TabButton active label="Agents" />
              <TabButton label="Tasks" />
              <TabButton label="Execution" />
            </>
          ) : (
            <>
              <TabButton active label="Canvas" />
              <TabButton label="Inspector" />
            </>
          )}
        </div>

        {/* Source preview - will be replaced by actual forms/canvas in Phase 5/6 */}
        <div className="flex-1 overflow-auto p-5">
          <div className="mb-3 text-xs text-muted-foreground uppercase tracking-wide font-semibold">
            Source Preview (Phase 5 will add form editors)
          </div>
          <pre className="text-xs font-mono bg-surface-1 p-4 rounded-lg border border-border overflow-auto whitespace-pre-wrap">
            {activeSource || '(empty workflow)'}
          </pre>
        </div>
      </div>

      {/* Right panel - will be task graph / canvas preview */}
      <div className="w-80 border-l border-border bg-surface-0 flex flex-col">
        <div className="px-4 py-3 border-b border-border">
          <span className="text-xs text-muted-foreground uppercase tracking-wide font-semibold">
            {activeWorkflow.kind === 'agent_workflow' ? 'Task Graph' : 'Minimap'}
          </span>
        </div>
        <div className="flex-1 flex items-center justify-center text-muted-foreground text-sm p-4 text-center">
          <div>
            <Icon name="git" size={32} className="mx-auto mb-2 opacity-50" />
            <p>React Flow visualization will appear here in Phase {activeWorkflow.kind === 'agent_workflow' ? '5' : '6'}.</p>
          </div>
        </div>
      </div>
    </div>
  )
}

function TabButton({ label, active }: { label: string; active?: boolean }) {
  return (
    <button
      className={cn(
        'px-4 py-2.5 text-sm font-medium border-b-2 transition-colors',
        active
          ? 'text-foreground border-primary'
          : 'text-muted-foreground border-transparent hover:text-foreground'
      )}
    >
      {label}
    </button>
  )
}
