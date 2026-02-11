import { useCallback, useEffect, useMemo, useState } from "react"
import { useNavigate, useSearchParams } from "react-router-dom"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Badge } from "@/components/ui/badge"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { useRunStore } from "@/stores/runs"
import { useWorkflowStore } from "@/stores/workflows"
import type { RunStatus } from "@/api/types"

const statusVariant: Record<RunStatus, "default" | "secondary" | "destructive" | "outline"> = {
  pending: "secondary",
  running: "secondary",
  completed: "default",
  failed: "destructive",
  cancelled: "outline",
}

function formatDate(iso: string): string {
  try {
    return new Date(iso).toLocaleString()
  } catch {
    return iso
  }
}

function formatDuration(ms?: number): string {
  if (ms == null) return "\u2014"
  if (ms < 1000) return `${ms}ms`
  return `${(ms / 1000).toFixed(1)}s`
}

export default function RunsPage() {
  const runs = useRunStore((s) => s.runs)
  const loading = useRunStore((s) => s.loading)
  const fetchRuns = useRunStore((s) => s.fetchRuns)
  const workflows = useWorkflowStore((s) => s.workflows)
  const fetchWorkflows = useWorkflowStore((s) => s.fetchWorkflows)

  const navigate = useNavigate()
  const [searchParams, setSearchParams] = useSearchParams()

  const [search, setSearch] = useState("")
  const [statusFilter, setStatusFilter] = useState<string>("all")
  const [workflowFilter, setWorkflowFilter] = useState<string>(
    searchParams.get("workflow_id") ?? "all",
  )

  useEffect(() => {
    fetchRuns()
    fetchWorkflows()
  }, [fetchRuns, fetchWorkflows])

  // Apply filters
  const filtered = useMemo(() => {
    let result = [...runs]

    if (workflowFilter !== "all") {
      result = result.filter((r) => r.workflow_id === workflowFilter)
    }
    if (statusFilter !== "all") {
      result = result.filter((r) => r.status === statusFilter)
    }
    if (search.trim()) {
      const q = search.toLowerCase()
      result = result.filter(
        (r) =>
          r.run_id.toLowerCase().includes(q) ||
          r.workflow_id.toLowerCase().includes(q),
      )
    }

    // Sort by start time descending
    result.sort(
      (a, b) => new Date(b.started_at).getTime() - new Date(a.started_at).getTime(),
    )

    return result
  }, [runs, search, statusFilter, workflowFilter])

  // Workflow name lookup
  const workflowNames = useMemo(() => {
    const map = new Map<string, string>()
    for (const wf of workflows) {
      map.set(wf.id, wf.name)
    }
    return map
  }, [workflows])

  const handleWorkflowFilter = useCallback(
    (value: string) => {
      setWorkflowFilter(value)
      if (value === "all") {
        searchParams.delete("workflow_id")
      } else {
        searchParams.set("workflow_id", value)
      }
      setSearchParams(searchParams)
    },
    [searchParams, setSearchParams],
  )

  return (
    <div className="container mx-auto py-6 space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-lg font-semibold">Run History</h1>
          <p className="text-xs text-muted-foreground">
            {filtered.length} run{filtered.length !== 1 ? "s" : ""}
            {loading ? " (loading...)" : ""}
          </p>
        </div>
        <Button variant="outline" size="sm" onClick={() => fetchRuns()}>
          Refresh
        </Button>
      </div>

      {/* Filters */}
      <div className="flex items-center gap-2">
        <Input
          placeholder="Search runs..."
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          className="h-8 text-xs w-56"
        />
        <Select value={statusFilter} onValueChange={setStatusFilter}>
          <SelectTrigger className="h-8 text-xs w-36">
            <SelectValue placeholder="Status" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">All statuses</SelectItem>
            <SelectItem value="pending">Pending</SelectItem>
            <SelectItem value="running">Running</SelectItem>
            <SelectItem value="completed">Completed</SelectItem>
            <SelectItem value="failed">Failed</SelectItem>
            <SelectItem value="cancelled">Cancelled</SelectItem>
          </SelectContent>
        </Select>
        <Select value={workflowFilter} onValueChange={handleWorkflowFilter}>
          <SelectTrigger className="h-8 text-xs w-48">
            <SelectValue placeholder="Workflow" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">All workflows</SelectItem>
            {workflows.map((wf) => (
              <SelectItem key={wf.id} value={wf.id}>
                {wf.name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>

      {/* Table */}
      <div className="rounded border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="text-xs w-48">Run ID</TableHead>
              <TableHead className="text-xs">Workflow</TableHead>
              <TableHead className="text-xs w-24">Status</TableHead>
              <TableHead className="text-xs w-44">Started</TableHead>
              <TableHead className="text-xs w-24">Duration</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {filtered.length === 0 && (
              <TableRow>
                <TableCell colSpan={5} className="text-center text-xs text-muted-foreground py-8">
                  {loading ? "Loading..." : "No runs found."}
                </TableCell>
              </TableRow>
            )}
            {filtered.map((run) => (
              <TableRow
                key={run.run_id}
                className="cursor-pointer hover:bg-muted/50"
                onClick={() => navigate(`/runs/${run.run_id}`)}
              >
                <TableCell className="text-xs font-mono">{run.run_id}</TableCell>
                <TableCell className="text-xs">
                  {workflowNames.get(run.workflow_id) ?? run.workflow_id}
                </TableCell>
                <TableCell>
                  <Badge variant={statusVariant[run.status]} className="text-[10px]">
                    {run.status}
                  </Badge>
                </TableCell>
                <TableCell className="text-xs text-muted-foreground">
                  {formatDate(run.started_at)}
                </TableCell>
                <TableCell className="text-xs text-muted-foreground">
                  {formatDuration(run.duration_ms)}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
    </div>
  )
}
