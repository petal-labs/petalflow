import { createFileRoute } from '@tanstack/react-router'

export const Route = createFileRoute('/_app/tools/')({
  component: ToolsPage,
})

function ToolsPage() {
  return (
    <div className="p-7">
      <h1 className="text-xl font-bold">Tools</h1>
      <p className="text-muted-foreground mt-1">Tool registry coming in Phase 8</p>
    </div>
  )
}
