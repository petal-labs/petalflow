import { useParams } from "react-router-dom"

export default function WorkflowEditorPage() {
  const { id } = useParams<{ id: string }>()

  return (
    <div className="container mx-auto py-6">
      <h1 className="text-2xl font-bold">Workflow Editor</h1>
      <p className="mt-2 text-muted-foreground">Editing workflow: {id}</p>
    </div>
  )
}
