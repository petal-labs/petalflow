import { useEffect, useMemo, useState } from "react"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { WorkflowCard } from "@/components/library/workflow-card"
import { NewWorkflowDropdown } from "@/components/library/new-workflow-dropdown"
import { DeleteWorkflowDialog } from "@/components/library/delete-workflow-dialog"
import { useWorkflowStore } from "@/stores/workflows"
import { toast } from "sonner"
import type { WorkflowKind, WorkflowSummary } from "@/api/types"

type TypeFilter = "all" | WorkflowKind
type SortBy = "recent" | "name" | "created"

export default function WorkflowsPage() {
  const workflows = useWorkflowStore((s) => s.workflows)
  const loading = useWorkflowStore((s) => s.loading)
  const fetchWorkflows = useWorkflowStore((s) => s.fetchWorkflows)
  const duplicateWorkflow = useWorkflowStore((s) => s.duplicateWorkflow)

  const [search, setSearch] = useState("")
  const [typeFilter, setTypeFilter] = useState<TypeFilter>("all")
  const [sortBy, setSortBy] = useState<SortBy>("recent")

  // Delete dialog state
  const [deleteTarget, setDeleteTarget] = useState<WorkflowSummary | null>(null)

  useEffect(() => {
    fetchWorkflows()
  }, [fetchWorkflows])

  const filtered = useMemo(() => {
    let result = [...workflows]

    // Text search
    if (search) {
      const q = search.toLowerCase()
      result = result.filter(
        (w) =>
          w.name.toLowerCase().includes(q) ||
          w.description?.toLowerCase().includes(q) ||
          w.tags?.some((t) => t.toLowerCase().includes(q)),
      )
    }

    // Type filter
    if (typeFilter !== "all") {
      result = result.filter((w) => w.kind === typeFilter)
    }

    // Sort
    result.sort((a, b) => {
      switch (sortBy) {
        case "name":
          return a.name.localeCompare(b.name)
        case "created":
          return new Date(b.created_at).getTime() - new Date(a.created_at).getTime()
        case "recent":
        default:
          return new Date(b.updated_at).getTime() - new Date(a.updated_at).getTime()
      }
    })

    return result
  }, [workflows, search, typeFilter, sortBy])

  const handleRun = (id: string) => {
    // TODO: Open run modal (P3+)
    toast.info(`Run modal for ${id} — coming soon.`)
  }

  const handleDuplicate = async (id: string) => {
    try {
      await duplicateWorkflow(id)
      toast.success("Workflow duplicated.")
    } catch {
      toast.error("Failed to duplicate workflow.")
    }
  }

  const handleDelete = (id: string) => {
    const wf = workflows.find((w) => w.id === id)
    if (wf) setDeleteTarget(wf)
  }

  return (
    <div className="container mx-auto py-6 space-y-4">
      {/* Header */}
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-bold">Workflows</h1>
        <NewWorkflowDropdown />
      </div>

      {/* Search & filter bar */}
      <div className="flex flex-wrap items-center gap-2">
        <Input
          placeholder="Search workflows..."
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          className="max-w-xs"
        />
        <Select
          value={typeFilter}
          onValueChange={(v) => setTypeFilter(v as TypeFilter)}
        >
          <SelectTrigger className="w-[140px]">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">All Types</SelectItem>
            <SelectItem value="agent_workflow">Agent / Task</SelectItem>
            <SelectItem value="graph">Graph</SelectItem>
          </SelectContent>
        </Select>
        <Select
          value={sortBy}
          onValueChange={(v) => setSortBy(v as SortBy)}
        >
          <SelectTrigger className="w-[130px]">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="recent">Recent</SelectItem>
            <SelectItem value="name">Name</SelectItem>
            <SelectItem value="created">Created</SelectItem>
          </SelectContent>
        </Select>
      </div>

      {/* Grid */}
      {loading ? (
        <p className="text-sm text-muted-foreground">Loading...</p>
      ) : filtered.length === 0 ? (
        <div className="py-12 text-center">
          <p className="text-muted-foreground">
            {workflows.length === 0
              ? "No workflows yet. Create one to get started."
              : "No workflows match your search."}
          </p>
        </div>
      ) : (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
          {filtered.map((w) => (
            <WorkflowCard
              key={w.id}
              workflow={w}
              onRun={handleRun}
              onDuplicate={handleDuplicate}
              onDelete={handleDelete}
            />
          ))}
        </div>
      )}

      {/* Delete confirmation dialog */}
      <DeleteWorkflowDialog
        workflowId={deleteTarget?.id ?? null}
        workflowName={deleteTarget?.name ?? ""}
        open={deleteTarget !== null}
        onOpenChange={(open) => {
          if (!open) setDeleteTarget(null)
        }}
      />
    </div>
  )
}
