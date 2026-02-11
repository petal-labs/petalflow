import { useCallback, useEffect, useRef, useState } from "react"
import { Badge } from "@/components/ui/badge"
import { useEditorStore } from "@/stores/editor"
import { useGraphStore } from "@/stores/graph"
import { useWorkflowStore } from "@/stores/workflows"
import { validateGraph } from "@/lib/graph-validation"
import type { ValidationDiagnostic } from "@/api/types"

interface IssuesPanelProps {
  mode?: "agent_workflow" | "graph"
  onNavigate?: (path: string) => void
}

export function IssuesPanel({ mode = "agent_workflow", onNavigate }: IssuesPanelProps) {
  const toDefinition = useEditorStore((s) => s.toDefinition)
  const tasks = useEditorStore((s) => s.tasks)
  const agents = useEditorStore((s) => s.agents)
  const validate = useWorkflowStore((s) => s.validate)

  const graphNodes = useGraphStore((s) => s.nodes)
  const graphEdges = useGraphStore((s) => s.edges)

  const [issues, setIssues] = useState<ValidationDiagnostic[]>([])
  const [collapsed, setCollapsed] = useState(false)
  const debounceRef = useRef<ReturnType<typeof globalThis.setTimeout> | null>(null)

  const doValidateAgent = useCallback(async () => {
    if (tasks.length === 0 && agents.length === 0) {
      setIssues([])
      return
    }
    try {
      const def = toDefinition()
      const result = await validate(def)
      setIssues(result.diagnostics)
    } catch {
      // validation endpoint unavailable — skip
    }
  }, [toDefinition, validate, tasks.length, agents.length])

  const doValidateGraph = useCallback(() => {
    const diags = validateGraph(graphNodes, graphEdges)
    setIssues(diags)
  }, [graphNodes, graphEdges])

  const doValidate = mode === "graph" ? doValidateGraph : doValidateAgent

  useEffect(() => {
    if (debounceRef.current) clearTimeout(debounceRef.current)
    // Use longer debounce for large workflows (50+ nodes) per spec §9.5
    const isLargeGraph = mode === "graph" && graphNodes.length >= 50
    const delay = isLargeGraph ? 1500 : mode === "graph" ? 500 : 800
    debounceRef.current = globalThis.setTimeout(doValidate, delay)
    return () => {
      if (debounceRef.current) clearTimeout(debounceRef.current)
    }
  }, [doValidate, mode, graphNodes.length])

  const errors = issues.filter((i) => i.severity === "error")
  const warnings = issues.filter((i) => i.severity === "warning")

  if (issues.length === 0) return null

  return (
    <div className="border-t">
      <button
        type="button"
        className="flex w-full items-center justify-between px-4 py-1.5 text-xs hover:bg-muted/50"
        onClick={() => setCollapsed(!collapsed)}
      >
        <div className="flex items-center gap-2">
          <span className="font-medium">Issues</span>
          {errors.length > 0 && (
            <Badge variant="destructive" className="text-[10px] h-4 px-1">
              {errors.length} error{errors.length !== 1 ? "s" : ""}
            </Badge>
          )}
          {warnings.length > 0 && (
            <Badge variant="secondary" className="text-[10px] h-4 px-1 bg-yellow-500/10 text-yellow-600">
              {warnings.length} warning{warnings.length !== 1 ? "s" : ""}
            </Badge>
          )}
        </div>
        <span className="text-muted-foreground">{collapsed ? "+" : "-"}</span>
      </button>
      {!collapsed && (
        <div className="max-h-32 overflow-y-auto px-4 pb-2 space-y-1">
          {issues.map((issue, i) => (
            <div
              key={i}
              className="flex items-start gap-2 text-[11px] cursor-pointer hover:bg-muted/30 rounded px-1 py-0.5"
              onClick={() => issue.path && onNavigate?.(issue.path)}
            >
              <span
                className={
                  issue.severity === "error"
                    ? "text-destructive"
                    : issue.severity === "warning"
                      ? "text-yellow-600"
                      : "text-blue-500"
                }
              >
                {issue.severity === "error"
                  ? "\u2716"
                  : issue.severity === "warning"
                    ? "\u26A0"
                    : "\u2139"}
              </span>
              <span className="flex-1">{issue.message}</span>
              {issue.path && (
                <span className="text-muted-foreground font-mono shrink-0">
                  {issue.path}
                </span>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

/** Hook to get error count for badge display. */
export function useValidationErrorCount(): number {
  // This is a simplified version — in production we'd share the validation state.
  return 0
}
