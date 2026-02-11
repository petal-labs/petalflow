import { useCallback, useEffect, useState } from "react"
import { useNavigate, useParams } from "react-router-dom"
import { ReactFlowProvider } from "@xyflow/react"
import { useRunStore } from "@/stores/runs"
import { useWorkflowStore } from "@/stores/workflows"
import { ExecutionView } from "@/components/runner/execution-view"
import { CompletionView } from "@/components/runner/completion-view"
import { TraceViewer } from "@/components/trace/trace-viewer"
import { RunModal } from "@/components/runner/run-modal"
import { toast } from "sonner"
import type { Workflow } from "@/api/types"

type View = "executing" | "completed" | "trace"

export default function RunDetailPage() {
  const { runId } = useParams<{ runId: string }>()
  const navigate = useNavigate()

  const activeRun = useRunStore((s) => s.activeRun)
  const getRun = useRunStore((s) => s.getRun)
  const clearActiveRun = useRunStore((s) => s.clearActiveRun)
  const startRun = useRunStore((s) => s.startRun)
  const getWorkflow = useWorkflowStore((s) => s.getWorkflow)

  const [view, setView] = useState<View>("executing")
  const [workflow, setWorkflow] = useState<Workflow | null>(null)
  const [loading, setLoading] = useState(true)
  const [rerunOpen, setRerunOpen] = useState(false)

  // Load run + workflow on mount
  useEffect(() => {
    if (!runId) return
    let cancelled = false
    ;(async () => {
      try {
        const run = await getRun(runId)
        if (cancelled) return
        // Determine initial view based on run status
        if (run.status === "completed" || run.status === "failed" || run.status === "cancelled") {
          setView("completed")
        }
        // Load the workflow for graph rendering
        try {
          const wf = await getWorkflow(run.workflow_id)
          if (!cancelled) setWorkflow(wf)
        } catch {
          // workflow may have been deleted — still show what we can
        }
        setLoading(false)
      } catch {
        if (!cancelled) {
          toast.error("Failed to load run.")
          setLoading(false)
        }
      }
    })()
    return () => {
      cancelled = true
      clearActiveRun()
    }
  }, [runId, getRun, getWorkflow, clearActiveRun])

  const handleRerun = useCallback(async () => {
    if (!workflow || !activeRun) return
    try {
      const run = await startRun(workflow.id, {
        inputs: activeRun.inputs,
        trace: true,
      })
      navigate(`/runs/${run.run_id}`, { replace: true })
    } catch {
      toast.error("Failed to re-run workflow.")
    }
  }, [workflow, activeRun, startRun, navigate])

  if (loading) {
    return (
      <div className="flex h-[calc(100vh-3.5rem)] items-center justify-center text-sm text-muted-foreground">
        Loading run...
      </div>
    )
  }

  if (!runId) {
    return (
      <div className="flex h-[calc(100vh-3.5rem)] items-center justify-center text-sm text-muted-foreground">
        No run ID specified.
      </div>
    )
  }

  // Minimal workflow stub if workflow couldn't be loaded
  const wf: Workflow = workflow ?? {
    id: activeRun?.workflow_id ?? "",
    name: activeRun?.workflow_id ?? "Unknown Workflow",
    kind: "graph",
    definition: {},
    created_at: "",
    updated_at: "",
  }

  return (
    <ReactFlowProvider>
      <div className="h-[calc(100vh-3.5rem)]">
        {view === "executing" && (
          <ExecutionView
            workflow={wf}
            runId={runId}
            onComplete={() => setView("completed")}
            onViewTrace={() => setView("trace")}
            onBack={() => navigate("/runs")}
          />
        )}

        {view === "completed" && (
          <CompletionView
            workflow={wf}
            onViewTrace={() => setView("trace")}
            onRerun={handleRerun}
            onRerunWithEdits={() => setRerunOpen(true)}
            onBack={() => navigate("/workflows")}
          />
        )}

        {view === "trace" && (
          <TraceViewer
            runId={runId}
            onBack={() => setView("completed")}
          />
        )}

        {/* Re-run with edits modal */}
        {workflow && (
          <RunModal
            open={rerunOpen}
            onOpenChange={setRerunOpen}
            workflow={wf}
            onStarted={(newRunId) => {
              navigate(`/runs/${newRunId}`, { replace: true })
              setView("executing")
            }}
          />
        )}
      </div>
    </ReactFlowProvider>
  )
}
