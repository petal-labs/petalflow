import { createFileRoute } from '@tanstack/react-router'

export const Route = createFileRoute('/_app/workflows/')({
  component: WorkflowsPage,
})

function WorkflowsPage() {
  return (
    <div className="p-7">
      <h1 className="text-xl font-bold">Workflows</h1>
      <p className="text-muted-foreground mt-1">Workflow library coming in Phase 4</p>
    </div>
  )
}
