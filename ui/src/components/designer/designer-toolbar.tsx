import { Button } from "@/components/ui/button"
import { Badge } from "@/components/ui/badge"
import type { WorkflowKind } from "@/api/types"

interface DesignerToolbarProps {
  workflowName: string
  kind: WorkflowKind
  saving: boolean
  dirty: boolean
  onRun: () => void
  onSettings: () => void
}

export function DesignerToolbar({
  workflowName,
  kind,
  saving,
  dirty,
  onRun,
  onSettings,
}: DesignerToolbarProps) {
  return (
    <div className="flex h-12 items-center justify-between border-b px-4">
      <div className="flex items-center gap-3">
        <h1 className="text-sm font-semibold truncate max-w-[300px]">
          {workflowName || "Untitled"}
        </h1>
        <Badge variant="secondary" className="text-[10px]">
          {kind === "agent_workflow" ? "Agent / Task" : "Graph"}
        </Badge>
        <span className="text-xs text-muted-foreground">
          {saving ? "Saving..." : dirty ? "Unsaved changes" : "Saved"}
        </span>
      </div>
      <div className="flex items-center gap-2">
        <Button variant="outline" size="sm" onClick={onSettings}>
          Settings
        </Button>
        <Button size="sm" onClick={onRun}>
          Run
        </Button>
      </div>
    </div>
  )
}
