import type { Workflow, WorkflowKind } from "@/api/types"

/**
 * Download a workflow definition as a JSON file.
 */
export function exportWorkflow(workflow: Workflow): void {
  const ext = workflow.kind === "agent_workflow" ? "agent" : "graph"
  const filename = `${sanitizeFilename(workflow.name)}.${ext}.json`
  const payload = {
    name: workflow.name,
    kind: workflow.kind,
    description: workflow.description,
    tags: workflow.tags,
    definition: workflow.definition,
  }
  const blob = new Blob([JSON.stringify(payload, null, 2)], {
    type: "application/json",
  })
  const url = URL.createObjectURL(blob)
  const a = document.createElement("a")
  a.href = url
  a.download = filename
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
  URL.revokeObjectURL(url)
}

interface ImportedWorkflow {
  name: string
  kind: WorkflowKind
  description?: string
  tags?: string[]
  definition: Record<string, unknown>
}

/**
 * Open a file picker and import a workflow JSON file.
 * Returns the parsed workflow data, or null if cancelled.
 */
export function importWorkflow(): Promise<ImportedWorkflow | null> {
  return new Promise((resolve) => {
    const input = document.createElement("input")
    input.type = "file"
    input.accept = ".json,.agent.json,.graph.json"
    input.onchange = async () => {
      const file = input.files?.[0]
      if (!file) {
        resolve(null)
        return
      }
      try {
        const text = await file.text()
        const data = JSON.parse(text)

        // Detect kind from file content or filename
        let kind: WorkflowKind = "graph"
        if (data.kind === "agent_workflow" || file.name.includes(".agent.")) {
          kind = "agent_workflow"
        }

        resolve({
          name: data.name ?? file.name.replace(/\.(agent|graph)?\.json$/, ""),
          kind: data.kind ?? kind,
          description: data.description,
          tags: Array.isArray(data.tags) ? data.tags : undefined,
          definition: data.definition ?? data,
        })
      } catch {
        resolve(null)
      }
    }
    input.oncancel = () => resolve(null)
    input.click()
  })
}

function sanitizeFilename(name: string): string {
  return name
    .replace(/[^a-zA-Z0-9_\-.\s]/g, "")
    .replace(/\s+/g, "-")
    .toLowerCase()
    .slice(0, 100) || "workflow"
}
