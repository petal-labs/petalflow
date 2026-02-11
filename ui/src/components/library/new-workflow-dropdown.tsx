import { useRef } from "react"
import { useNavigate } from "react-router-dom"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { useWorkflowStore } from "@/stores/workflows"
import { toast } from "sonner"

export function NewWorkflowDropdown() {
  const navigate = useNavigate()
  const createWorkflow = useWorkflowStore((s) => s.createWorkflow)
  const fileInput = useRef<HTMLInputElement>(null)

  const handleNew = async (kind: "agent_workflow" | "graph") => {
    try {
      const created = await createWorkflow({
        name: kind === "agent_workflow" ? "Untitled Agent Workflow" : "Untitled Graph",
        kind,
        definition: {},
      })
      navigate(`/workflows/${created.id}/edit`)
    } catch {
      toast.error("Failed to create workflow.")
    }
  }

  const handleImport = () => {
    fileInput.current?.click()
  }

  const handleFileChange = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    if (!file) return

    try {
      const text = await file.text()
      const definition = JSON.parse(text)
      // Detect kind from file content or extension
      const isAgent =
        file.name.includes(".agent.") ||
        definition.agents != null ||
        definition.tasks != null
      const kind = isAgent ? "agent_workflow" as const : "graph" as const
      const name = file.name.replace(/\.(agent|graph)\.(json|yaml|yml)$/i, "") || "Imported Workflow"

      const created = await createWorkflow({
        name,
        kind,
        definition,
      })
      navigate(`/workflows/${created.id}/edit`)
    } catch {
      toast.error("Failed to import workflow. Check the file format.")
    } finally {
      // Reset input so the same file can be re-imported
      e.target.value = ""
    }
  }

  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button size="sm">+ New</Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end">
          <DropdownMenuItem onClick={() => handleNew("agent_workflow")}>
            New Agent / Task Workflow
          </DropdownMenuItem>
          <DropdownMenuItem onClick={() => handleNew("graph")}>
            New Graph Workflow
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem onClick={handleImport}>
            Import from File
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
      <input
        ref={fileInput}
        type="file"
        accept=".json,.yaml,.yml"
        className="hidden"
        onChange={handleFileChange}
      />
    </>
  )
}
