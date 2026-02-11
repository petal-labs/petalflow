import { useCallback, useMemo } from "react"
import { Button } from "@/components/ui/button"
import { Badge } from "@/components/ui/badge"
import { Separator } from "@/components/ui/separator"
import { useRunStore } from "@/stores/runs"
import { toast } from "sonner"
import type { Workflow } from "@/api/types"

interface CompletionViewProps {
  workflow: Workflow
  onViewTrace: () => void
  onRerun: () => void
  onRerunWithEdits: () => void
  onBack: () => void
}

export function CompletionView({
  workflow,
  onViewTrace,
  onRerun,
  onRerunWithEdits,
  onBack,
}: CompletionViewProps) {
  const activeRun = useRunStore((s) => s.activeRun)
  const nodeStatuses = useRunStore((s) => s.nodeStatuses)
  const nodeOutputs = useRunStore((s) => s.nodeOutputs)

  const finalOutput = useMemo(() => {
    if (!activeRun) return ""
    // Use the run's outputs if available
    if (activeRun.outputs) {
      const vals = Object.values(activeRun.outputs)
      if (vals.length === 1 && typeof vals[0] === "string") return vals[0]
      return JSON.stringify(activeRun.outputs, null, 2)
    }
    // Fallback: last node's output
    const graph = workflow.compiled ?? workflow.definition
    const rawNodes = (graph.nodes as Array<Record<string, unknown>>) ?? []
    if (rawNodes.length > 0) {
      const lastNodeId = String(rawNodes[rawNodes.length - 1].id)
      return nodeOutputs[lastNodeId] ?? ""
    }
    return ""
  }, [activeRun, workflow, nodeOutputs])

  // Metrics
  const metrics = useMemo(() => {
    const statusCounts = { completed: 0, failed: 0, skipped: 0 }
    for (const s of Object.values(nodeStatuses)) {
      if (s === "completed") statusCounts.completed++
      else if (s === "failed") statusCounts.failed++
      else if (s === "skipped") statusCounts.skipped++
    }
    return {
      duration: activeRun?.duration_ms
        ? `${(activeRun.duration_ms / 1000).toFixed(1)}s`
        : "N/A",
      tokensIn: activeRun?.tokens_in ?? 0,
      tokensOut: activeRun?.tokens_out ?? 0,
      ...statusCounts,
    }
  }, [activeRun, nodeStatuses])

  const handleCopy = useCallback(() => {
    navigator.clipboard.writeText(finalOutput).then(
      () => toast.success("Copied to clipboard."),
      () => toast.error("Failed to copy."),
    )
  }, [finalOutput])

  const handleDownload = useCallback(() => {
    const blob = new Blob([finalOutput], { type: "text/markdown" })
    const url = URL.createObjectURL(blob)
    const a = document.createElement("a")
    a.href = url
    a.download = `${workflow.name.replace(/\s+/g, "_")}_output.md`
    a.click()
    URL.revokeObjectURL(url)
  }, [finalOutput, workflow.name])

  if (!activeRun) return null

  const isError = activeRun.status === "failed"

  return (
    <div className="flex h-full flex-col">
      {/* Header */}
      <div className="flex items-center justify-between border-b px-4 py-3">
        <div className="flex items-center gap-3">
          <span className="text-sm font-medium">{workflow.name}</span>
          <Badge
            variant={isError ? "destructive" : "default"}
            className="text-[10px]"
          >
            {activeRun.status}
          </Badge>
          <span className="text-xs text-muted-foreground font-mono">
            {activeRun.run_id}
          </span>
        </div>
      </div>

      {/* Metrics */}
      <div className="grid grid-cols-2 sm:grid-cols-4 gap-3 px-4 py-3 border-b">
        <MetricCard label="Duration" value={metrics.duration} />
        <MetricCard
          label="Tokens"
          value={
            metrics.tokensIn + metrics.tokensOut > 0
              ? `${metrics.tokensIn.toLocaleString()} in / ${metrics.tokensOut.toLocaleString()} out`
              : "N/A"
          }
        />
        <MetricCard
          label="Nodes"
          value={`${metrics.completed} ok${metrics.failed ? ` / ${metrics.failed} failed` : ""}`}
        />
        <MetricCard
          label="Status"
          value={activeRun.status}
          color={isError ? "text-destructive" : "text-green-600 dark:text-green-400"}
        />
      </div>

      {/* Error display */}
      {isError && activeRun.error && (
        <div className="mx-4 mt-3 rounded border border-destructive/30 bg-destructive/5 p-3">
          <div className="text-xs font-medium text-destructive mb-1">Error</div>
          <div className="text-xs font-mono">
            {activeRun.error.code}: {activeRun.error.message}
          </div>
        </div>
      )}

      {/* Final output */}
      <div className="flex-1 min-h-0 flex flex-col mx-4 my-3">
        <div className="flex items-center justify-between mb-2">
          <span className="text-xs font-medium">Final Output</span>
          <div className="flex items-center gap-1.5">
            <Button variant="outline" size="sm" className="h-6 text-[10px]" onClick={handleCopy}>
              Copy
            </Button>
            <Button variant="outline" size="sm" className="h-6 text-[10px]" onClick={handleDownload}>
              Download .md
            </Button>
          </div>
        </div>
        <div className="flex-1 min-h-0 rounded border bg-muted/20 overflow-y-auto">
          <pre className="p-3 text-xs font-mono whitespace-pre-wrap">
            {finalOutput || (isError ? "(No output — run failed)" : "(No output)")}
          </pre>
        </div>
      </div>

      <Separator />

      {/* Actions */}
      <div className="flex items-center justify-between px-4 py-2">
        <Button variant="outline" size="sm" onClick={onBack}>
          Back to Library
        </Button>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" onClick={onViewTrace}>
            View Trace
          </Button>
          <Button variant="outline" size="sm" onClick={onRerunWithEdits}>
            Re-run with edits
          </Button>
          <Button size="sm" onClick={onRerun}>
            Re-run
          </Button>
        </div>
      </div>
    </div>
  )
}

function MetricCard({
  label,
  value,
  color,
}: {
  label: string
  value: string
  color?: string
}) {
  return (
    <div className="rounded border p-2">
      <div className="text-[10px] text-muted-foreground">{label}</div>
      <div className={`text-xs font-medium ${color ?? ""}`}>{value}</div>
    </div>
  )
}
