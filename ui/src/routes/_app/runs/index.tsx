import { createFileRoute } from '@tanstack/react-router'

export const Route = createFileRoute('/_app/runs/')({
  component: RunsPage,
})

function RunsPage() {
  return (
    <div className="p-7">
      <h1 className="text-xl font-bold">Runs</h1>
      <p className="text-muted-foreground mt-1">Run history coming in Phase 7</p>
    </div>
  )
}
