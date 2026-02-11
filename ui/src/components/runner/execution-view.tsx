import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { ReactFlow, Background, BackgroundVariant, type Node, type Edge } from "@xyflow/react"
import "@xyflow/react/dist/style.css"
import { Button } from "@/components/ui/button"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Badge } from "@/components/ui/badge"
import { useRunStore } from "@/stores/runs"
import { useRunStream } from "@/hooks/use-run-stream"
import { ConnectionBanner } from "./connection-banner"
import { ReviewGatePanel } from "./review-gate-panel"
import type { Workflow } from "@/api/types"

interface ExecutionViewProps {
  workflow: Workflow
  runId: string
  onComplete: () => void
  onViewTrace: () => void
  onBack: () => void
}

type NodeStatus = "pending" | "running" | "completed" | "failed" | "skipped" | "review"

const statusColors: Record<NodeStatus, { border: string; bg: string }> = {
  pending:   { border: "#6b7280", bg: "#f3f4f6" },
  running:   { border: "#3b82f6", bg: "#eff6ff" },
  completed: { border: "#22c55e", bg: "#f0fdf4" },
  failed:    { border: "#ef4444", bg: "#fef2f2" },
  skipped:   { border: "#9ca3af", bg: "#f9fafb" },
  review:    { border: "#f59e0b", bg: "#fffbeb" },
}

const statusIcons: Record<NodeStatus, string> = {
  pending:   "\u25CB",
  running:   "\u21BB",
  completed: "\u2713",
  failed:    "\u2717",
  skipped:   "\u2014",
  review:    "\u23F8",
}

/** Build React Flow nodes from the compiled graph + live statuses. */
function buildExecutionNodes(
  workflow: Workflow,
  nodeStatuses: Record<string, NodeStatus>,
): Node[] {
  const graph = workflow.compiled ?? workflow.definition
  const rawNodes = (graph.nodes as Array<Record<string, unknown>>) ?? []
  return rawNodes.map((n, i) => {
    const id = String(n.id)
    const status = nodeStatuses[id] ?? "pending"
    const colors = statusColors[status]
    return {
      id,
      type: "default",
      position: (n.position as { x: number; y: number }) ?? { x: 50, y: i * 100 + 50 },
      data: {
        label: `${statusIcons[status]} ${id}`,
      },
      style: {
        border: `2px solid ${colors.border}`,
        background: colors.bg,
        borderRadius: "8px",
        fontSize: "11px",
        padding: "6px 10px",
        ...(status === "skipped" ? { borderStyle: "dashed" } : {}),
      },
    }
  })
}

function buildExecutionEdges(
  workflow: Workflow,
  nodeStatuses: Record<string, NodeStatus>,
): Edge[] {
  const graph = workflow.compiled ?? workflow.definition
  const rawEdges = (graph.edges as Array<Record<string, unknown>>) ?? []
  return rawEdges.map((e, i) => {
    const source = String(e.source ?? e.from)
    const target = String(e.target ?? e.to)
    const sourceStatus = nodeStatuses[source]
    const isActive = sourceStatus === "completed" && nodeStatuses[target] === "running"
    return {
      id: String(e.id ?? `e-${i}`),
      source,
      target,
      animated: isActive,
      style: {
        stroke: isActive ? "#3b82f6" : "#d1d5db",
        strokeWidth: isActive ? 2 : 1,
      },
    }
  })
}

function OutputSection({
  nodeId,
  output,
  status,
}: {
  nodeId: string
  output: string
  status: NodeStatus
}) {
  const [collapsed, setCollapsed] = useState(false)
  const colors = statusColors[status]

  return (
    <div className="border rounded overflow-hidden">
      <button
        type="button"
        className="flex w-full items-center gap-2 px-2 py-1 text-xs hover:bg-muted/30"
        onClick={() => setCollapsed(!collapsed)}
      >
        <span
          className="inline-block h-2 w-2 rounded-full shrink-0"
          style={{ background: colors.border }}
        />
        <span className="font-medium truncate">{nodeId}</span>
        <span className="text-[10px] text-muted-foreground ml-auto">
          {statusIcons[status]} {status}
        </span>
        <span className="text-muted-foreground">{collapsed ? "+" : "\u2212"}</span>
      </button>
      {!collapsed && output && (
        <pre className="px-2 py-1.5 text-[11px] font-mono whitespace-pre-wrap border-t bg-muted/20 max-h-48 overflow-y-auto">
          {output}
        </pre>
      )}
    </div>
  )
}

export function ExecutionView({
  workflow,
  runId,
  onComplete,
  onViewTrace,
  onBack,
}: ExecutionViewProps) {
  const activeRun = useRunStore((s) => s.activeRun)
  const nodeStatuses = useRunStore((s) => s.nodeStatuses)
  const nodeOutputs = useRunStore((s) => s.nodeOutputs)
  const pendingReviews = useRunStore((s) => s.pendingReviews)
  const cancelRun = useRunStore((s) => s.cancelRun)

  const { status: streamStatus, retryCount } = useRunStream(runId)

  const [autoScroll, setAutoScroll] = useState(true)
  const outputEndRef = useRef<HTMLDivElement>(null)

  // Auto-scroll output
  useEffect(() => {
    if (autoScroll && outputEndRef.current) {
      outputEndRef.current.scrollIntoView({ behavior: "smooth" })
    }
  }, [nodeOutputs, autoScroll])

  // Detect completion
  useEffect(() => {
    if (activeRun?.status === "completed" || activeRun?.status === "failed" || activeRun?.status === "cancelled") {
      onComplete()
    }
  }, [activeRun?.status, onComplete])

  const nodes = useMemo(
    () => buildExecutionNodes(workflow, nodeStatuses),
    [workflow, nodeStatuses],
  )
  const edges = useMemo(
    () => buildExecutionEdges(workflow, nodeStatuses),
    [workflow, nodeStatuses],
  )

  const handleCancel = useCallback(async () => {
    try {
      await cancelRun(runId)
    } catch {
      // already cancelled or completed
    }
  }, [cancelRun, runId])

  const isRunning = activeRun?.status === "running" || activeRun?.status === "pending"
  const elapsed = activeRun?.started_at
    ? Math.round((Date.now() - new Date(activeRun.started_at).getTime()) / 1000)
    : 0

  // Build ordered output list from nodes that have output
  const outputNodeIds = useMemo(() => {
    const graph = workflow.compiled ?? workflow.definition
    const rawNodes = (graph.nodes as Array<Record<string, unknown>>) ?? []
    return rawNodes.map((n) => String(n.id)).filter((id) => nodeOutputs[id] || nodeStatuses[id])
  }, [workflow, nodeOutputs, nodeStatuses])

  return (
    <div className="flex h-full flex-col">
      {/* Header */}
      <div className="flex items-center justify-between border-b px-4 py-2">
        <div className="flex items-center gap-3">
          <span className="text-sm font-medium">{workflow.name}</span>
          <Badge
            variant={
              activeRun?.status === "completed"
                ? "default"
                : activeRun?.status === "failed"
                  ? "destructive"
                  : "secondary"
            }
            className="text-[10px]"
          >
            {activeRun?.status ?? "pending"}
          </Badge>
          <span className="text-xs text-muted-foreground font-mono">{runId}</span>
        </div>
        <div className="flex items-center gap-2">
          {isRunning && (
            <span className="text-xs text-muted-foreground">{elapsed}s elapsed</span>
          )}
          {activeRun?.duration_ms != null && (
            <span className="text-xs text-muted-foreground">
              {(activeRun.duration_ms / 1000).toFixed(1)}s
            </span>
          )}
        </div>
      </div>

      <ConnectionBanner status={streamStatus} retryCount={retryCount} />

      {/* Main content: graph + output side by side */}
      <div className="flex flex-1 min-h-0">
        {/* Live graph */}
        <div className="w-1/2 border-r">
          <ReactFlow
            nodes={nodes}
            edges={edges}
            fitView
            fitViewOptions={{ padding: 0.3 }}
            proOptions={{ hideAttribution: true }}
            nodesDraggable={false}
            nodesConnectable={false}
            elementsSelectable={false}
            panOnDrag={false}
            zoomOnScroll={false}
          >
            <Background variant={BackgroundVariant.Dots} gap={16} size={1} />
          </ReactFlow>
        </div>

        {/* Output stream */}
        <div className="w-1/2 flex flex-col">
          <div className="flex items-center justify-between border-b px-3 py-1.5">
            <span className="text-xs font-medium">Output</span>
            <label className="flex items-center gap-1.5 text-[10px] text-muted-foreground cursor-pointer">
              <input
                type="checkbox"
                checked={autoScroll}
                onChange={(e) => setAutoScroll(e.target.checked)}
                className="h-3 w-3"
              />
              Auto-scroll
            </label>
          </div>
          <ScrollArea className="flex-1">
            <div className="p-2 space-y-1.5">
              {outputNodeIds.map((nodeId) => (
                <OutputSection
                  key={nodeId}
                  nodeId={nodeId}
                  output={nodeOutputs[nodeId] ?? ""}
                  status={nodeStatuses[nodeId] ?? "pending"}
                />
              ))}
              {outputNodeIds.length === 0 && isRunning && (
                <div className="flex items-center justify-center py-8 text-xs text-muted-foreground">
                  Waiting for output...
                </div>
              )}
              <div ref={outputEndRef} />
            </div>
          </ScrollArea>
        </div>
      </div>

      {/* Review gates */}
      {pendingReviews.length > 0 && (
        <ReviewGatePanel
          runId={runId}
          reviews={pendingReviews}
          nodeOutputs={nodeOutputs}
        />
      )}

      {/* Footer actions */}
      <div className="flex items-center justify-between border-t px-4 py-2">
        <Button variant="outline" size="sm" onClick={onBack}>
          Back
        </Button>
        <div className="flex items-center gap-2">
          {isRunning && (
            <Button variant="destructive" size="sm" onClick={handleCancel}>
              Cancel Run
            </Button>
          )}
          {!isRunning && (
            <Button variant="outline" size="sm" onClick={onViewTrace}>
              View Trace
            </Button>
          )}
        </div>
      </div>
    </div>
  )
}
