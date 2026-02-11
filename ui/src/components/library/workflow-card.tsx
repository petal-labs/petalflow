import { useNavigate } from "react-router-dom"
import {
  Card,
  CardContent,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import type { WorkflowSummary } from "@/api/types"

interface WorkflowCardProps {
  workflow: WorkflowSummary
  onRun: (id: string) => void
  onDuplicate: (id: string) => void
  onDelete: (id: string) => void
  onExport?: (id: string) => void
}

export function WorkflowCard({
  workflow,
  onRun,
  onDuplicate,
  onDelete,
  onExport,
}: WorkflowCardProps) {
  const navigate = useNavigate()

  const isAgent = workflow.kind === "agent_workflow"
  const stats = isAgent
    ? compact([
        workflow.agent_count != null && `${workflow.agent_count} agent${workflow.agent_count !== 1 ? "s" : ""}`,
        workflow.task_count != null && `${workflow.task_count} task${workflow.task_count !== 1 ? "s" : ""}`,
      ])
    : compact([
        workflow.node_count != null && `${workflow.node_count} node${workflow.node_count !== 1 ? "s" : ""}`,
        workflow.edge_count != null && `${workflow.edge_count} edge${workflow.edge_count !== 1 ? "s" : ""}`,
      ])

  return (
    <Card className="flex flex-col">
      <CardHeader className="pb-2">
        <div className="flex items-start justify-between gap-2">
          <CardTitle className="text-sm font-medium leading-tight line-clamp-2">
            {workflow.name}
          </CardTitle>
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button variant="ghost" size="sm" className="h-6 w-6 p-0 shrink-0">
                &middot;&middot;&middot;
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              <DropdownMenuItem onClick={() => onDuplicate(workflow.id)}>
                Duplicate
              </DropdownMenuItem>
              <DropdownMenuItem onClick={() => navigate(`/runs?workflow_id=${workflow.id}`)}>
                View Run History
              </DropdownMenuItem>
              {onExport && (
                <DropdownMenuItem onClick={() => onExport(workflow.id)}>
                  Export JSON
                </DropdownMenuItem>
              )}
              <DropdownMenuSeparator />
              <DropdownMenuItem
                onClick={() => onDelete(workflow.id)}
                className="text-destructive focus:text-destructive"
              >
                Delete
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
        <Badge variant="secondary" className="w-fit text-[10px]">
          {isAgent ? "Agent / Task" : "Graph"}
        </Badge>
      </CardHeader>

      <CardContent className="flex-1 pb-2">
        {workflow.description && (
          <p className="text-xs text-muted-foreground line-clamp-2 mb-2">
            {workflow.description}
          </p>
        )}
        {stats.length > 0 && (
          <p className="text-xs text-muted-foreground">
            {stats.join(" \u00b7 ")}
          </p>
        )}
        <p className="text-[10px] text-muted-foreground/70 mt-1">
          Updated {formatRelative(workflow.updated_at)}
        </p>
      </CardContent>

      <CardFooter className="gap-1 pt-0">
        <Button
          variant="outline"
          size="sm"
          className="flex-1"
          onClick={() => navigate(`/workflows/${workflow.id}/edit`)}
        >
          Edit
        </Button>
        <Button
          size="sm"
          className="flex-1"
          onClick={() => onRun(workflow.id)}
        >
          Run
        </Button>
      </CardFooter>
    </Card>
  )
}

function compact(items: (string | false | null | undefined)[]): string[] {
  return items.filter((x): x is string => Boolean(x))
}

function formatRelative(iso: string): string {
  const now = Date.now()
  const then = new Date(iso).getTime()
  const diff = now - then

  const minutes = Math.floor(diff / 60_000)
  if (minutes < 1) return "just now"
  if (minutes < 60) return `${minutes}m ago`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h ago`
  const days = Math.floor(hours / 24)
  if (days < 30) return `${days}d ago`
  return new Date(iso).toLocaleDateString()
}
