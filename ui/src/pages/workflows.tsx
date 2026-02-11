import { useCallback, useEffect, useMemo, useState } from "react"
import { useNavigate } from "react-router-dom"
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
import { EmptyState } from "@/components/empty-state"
import { WorkflowCardsSkeleton } from "@/components/loading-skeletons"
import { Button } from "@/components/ui/button"
import { useWorkflowStore } from "@/stores/workflows"
import { RunModal } from "@/components/runner/run-modal"
import { exportWorkflow, importWorkflow } from "@/lib/workflow-io"
import { toast } from "sonner"
import type { Workflow, WorkflowKind, WorkflowSummary } from "@/api/types"

type TypeFilter = "all" | WorkflowKind
type SortBy = "recent" | "name" | "created"

export default function WorkflowsPage() {
  const workflows = useWorkflowStore((s) => s.workflows)
  const loading = useWorkflowStore((s) => s.loading)
  const fetchWorkflows = useWorkflowStore((s) => s.fetchWorkflows)
  const getWorkflow = useWorkflowStore((s) => s.getWorkflow)
  const createWorkflow = useWorkflowStore((s) => s.createWorkflow)
  const duplicateWorkflow = useWorkflowStore((s) => s.duplicateWorkflow)
  const navigate = useNavigate()

  const [search, setSearch] = useState("")
  const [typeFilter, setTypeFilter] = useState<TypeFilter>("all")
  const [sortBy, setSortBy] = useState<SortBy>("recent")

  // Delete dialog state
  const [deleteTarget, setDeleteTarget] = useState<WorkflowSummary | null>(null)
  // Run modal state
  const [runTarget, setRunTarget] = useState<Workflow | null>(null)

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

  const handleRun = useCallback(async (id: string) => {
    try {
      const wf = await getWorkflow(id)
      setRunTarget(wf)
    } catch {
      toast.error("Failed to load workflow for run.")
    }
  }, [getWorkflow])

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

  const handleExport = useCallback(async (id: string) => {
    try {
      const wf = await getWorkflow(id)
      exportWorkflow(wf)
    } catch {
      toast.error("Failed to export workflow.")
    }
  }, [getWorkflow])

  const handleImport = useCallback(async () => {
    try {
      const data = await importWorkflow()
      if (!data) return
      const created = await createWorkflow({
        name: data.name,
        kind: data.kind,
        description: data.description,
        tags: data.tags,
        definition: data.definition,
      })
      toast.success(`Imported "${created.name}".`)
      navigate(`/workflows/${created.id}/edit`)
    } catch {
      toast.error("Failed to import workflow. Check the file format.")
    }
  }, [createWorkflow, navigate])

  return (
    <div className="container mx-auto py-6 space-y-4">
      {/* Header */}
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-bold">Workflows</h1>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" onClick={handleImport}>
            Import
          </Button>
          <NewWorkflowDropdown />
        </div>
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
        <WorkflowCardsSkeleton />
      ) : filtered.length === 0 ? (
        workflows.length === 0 ? (
          <EmptyState
            title="No workflows yet"
            description="Create your first workflow to start building AI pipelines."
            action={{
              label: "New workflow",
              onClick: async () => {
                try {
                  const created = await createWorkflow({
                    name: "Untitled Agent Workflow",
                    kind: "agent_workflow",
                    definition: {},
                  })
                  navigate(`/workflows/${created.id}/edit`)
                } catch {
                  toast.error("Failed to create workflow.")
                }
              },
            }}
          />
        ) : (
          <EmptyState
            title="No matches"
            description="No workflows match your current filters. Try adjusting your search or type filter."
          />
        )
      ) : (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
          {filtered.map((w) => (
            <WorkflowCard
              key={w.id}
              workflow={w}
              onRun={handleRun}
              onDuplicate={handleDuplicate}
              onDelete={handleDelete}
              onExport={handleExport}
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

      {/* Run modal */}
      {runTarget && (
        <RunModal
          open={runTarget !== null}
          onOpenChange={(open) => { if (!open) setRunTarget(null) }}
          workflow={runTarget}
          onStarted={(runId) => {
            setRunTarget(null)
            navigate(`/runs/${runId}`)
          }}
        />
      )}
    </div>
  )
}
