import { createFileRoute } from '@tanstack/react-router'

export const Route = createFileRoute('/_app/designer/')({
  component: DesignerPage,
})

function DesignerPage() {
  return (
    <div className="flex items-center justify-center h-full text-muted-foreground">
      Select a workflow from the library to start editing.
    </div>
  )
}
