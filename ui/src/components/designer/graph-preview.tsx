import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import {
  ReactFlow,
  Background,
  BackgroundVariant,
  type Node,
  type Edge,
  type NodeProps,
  type NodeTypes,
  Position,
} from "@xyflow/react"
import "@xyflow/react/dist/style.css"
import { useEditorStore } from "@/stores/editor"
import { useWorkflowStore } from "@/stores/workflows"

interface PreviewNodeData extends Record<string, unknown> {
  title: string
  taskId: string
  agentLabel: string
  outputKey: string
  inputCount: number
  reviewRequired: boolean
  summary: string
  runtimeType: string
}

function firstLine(text: string): string {
  const trimmed = text.trim()
  if (trimmed.length === 0) return ""
  const idx = trimmed.indexOf("\n")
  if (idx === -1) return trimmed
  return trimmed.slice(0, idx).trim()
}

function clip(text: string, max = 120): string {
  const clean = text.trim()
  if (clean.length <= max) return clean
  return `${clean.slice(0, Math.max(0, max - 1)).trimEnd()}\u2026`
}

function normalizeTaskIDFromNodeID(nodeID: string): string {
  const trimmed = nodeID.trim()
  if (trimmed.length === 0) return ""
  if (!trimmed.includes("__")) return trimmed
  return trimmed.split("__")[0].trim()
}

function TaskPreviewNode({ data }: NodeProps<Node<PreviewNodeData>>) {
  const label = data.agentLabel.trim() || "Unassigned"
  const outputKey = data.outputKey.trim() || "(auto)"
  const title = data.title.trim() || "Untitled task"
  const runtimeType = data.runtimeType.trim()
  const summary = data.summary.trim()

  return (
    <div className="w-[268px] rounded-lg border border-border/85 bg-card text-card-foreground shadow-[0_8px_20px_-16px_rgba(0,0,0,0.35)]">
      <div className="rounded-t-lg border-b border-border/75 bg-muted/25 px-3 py-2">
        <div className="flex items-center justify-between gap-2">
          <p className="truncate text-[10px] font-mono text-muted-foreground">
            {data.taskId}
          </p>
          <div className="flex items-center gap-1">
            {runtimeType && (
              <span className="rounded-full border border-border/70 bg-background/70 px-1.5 py-0.5 text-[9px] uppercase tracking-wide text-muted-foreground">
                {runtimeType}
              </span>
            )}
            {data.reviewRequired && (
              <span className="rounded-full border border-amber-500/40 bg-amber-500/10 px-1.5 py-0.5 text-[9px] uppercase tracking-wide text-amber-700 dark:text-amber-300">
                Review
              </span>
            )}
          </div>
        </div>
        <p className="mt-1 text-[13px] font-semibold leading-tight">
          {title}
        </p>
      </div>

      <div className="space-y-2 px-3 py-2.5">
        <p className="line-clamp-2 text-[11px] leading-snug text-muted-foreground">
          {summary || "No expected output details yet."}
        </p>
        <div className="flex items-center justify-between gap-2 border-t border-border/65 pt-2 text-[10px] text-muted-foreground">
          <span className="truncate">Agent: <span className="font-medium text-foreground/90">{label}</span></span>
          <span className="truncate font-mono">out: {outputKey}</span>
          <span>{data.inputCount} in</span>
        </div>
      </div>
    </div>
  )
}

const previewNodeTypes: NodeTypes = {
  taskPreview: TaskPreviewNode,
}

export function GraphPreview() {
  const toDefinition = useEditorStore((s) => s.toDefinition)
  const agents = useEditorStore((s) => s.agents)
  const tasks = useEditorStore((s) => s.tasks)
  const strategy = useEditorStore((s) => s.strategy)
  const compile = useWorkflowStore((s) => s.compile)

  const [compiledNodes, setCompiledNodes] = useState<Node[]>([])
  const [compiledEdges, setCompiledEdges] = useState<Edge[]>([])
  const [error, setError] = useState<string | null>(null)
  const debounceRef = useRef<ReturnType<typeof globalThis.setTimeout> | null>(null)
  const tasksByID = useMemo(() => {
    const out = new Map<string, (typeof tasks)[number]>()
    for (const task of tasks) {
      out.set(task.id, task)
    }
    return out
  }, [tasks])

  const agentsByID = useMemo(() => {
    const out = new Map<string, (typeof agents)[number]>()
    for (const agent of agents) {
      out.set(agent.id, agent)
    }
    return out
  }, [agents])

  const buildNodeData = useCallback((nodeID: string, runtimeType: string): PreviewNodeData => {
    const taskID = normalizeTaskIDFromNodeID(nodeID)
    const task = tasksByID.get(taskID)
    const agent = task ? agentsByID.get(task.agent) : undefined
    const title = task ? firstLine(task.description) : firstLine(nodeID)

    return {
      title: clip(title || taskID || nodeID || "Untitled task", 96),
      taskId: taskID || nodeID || "task",
      agentLabel: agent ? (agent.role.trim() || agent.id) : "",
      outputKey: task?.output_key ?? "",
      inputCount: task ? Object.keys(task.inputs ?? {}).length : 0,
      reviewRequired: Boolean(task?.human_review),
      summary: clip(firstLine(task?.expected_output ?? ""), 120),
      runtimeType: runtimeType.trim(),
    }
  }, [agentsByID, tasksByID])

  // Build a simple fallback layout from the editor state directly
  const fallbackLayout = useMemo(() => {
    const nodes: Node[] = []
    const edges: Edge[] = []
    const ySpacing = 170
    const xOffset = 50
    const usedNodeIDs = new Set<string>()
    const taskNodeIDs = tasks.map((task, i) => {
      const baseID = task.id.trim() || `task_${i + 1}`
      let nodeID = baseID
      let suffix = 2
      while (usedNodeIDs.has(nodeID)) {
        nodeID = `${baseID}_${suffix}`
        suffix++
      }
      usedNodeIDs.add(nodeID)
      return nodeID
    })

    tasks.forEach((_, i) => {
      const taskID = taskNodeIDs[i]
      nodes.push({
        id: taskID,
        type: "taskPreview",
        position: { x: xOffset, y: i * ySpacing + 50 },
        data: buildNodeData(taskID, "task"),
        sourcePosition: Position.Bottom,
        targetPosition: Position.Top,
      })

      // Sequential edges
      if (strategy === "sequential" && i > 0) {
        const prevTaskID = taskNodeIDs[i - 1]
        edges.push({
          id: `e-${prevTaskID}-${taskID}`,
          source: prevTaskID,
          target: taskID,
        })
      }
    })

    return { nodes, edges }
  }, [tasks, strategy, buildNodeData])

  const doCompile = useCallback(async () => {
    if (tasks.length === 0) {
      setCompiledNodes([])
      setCompiledEdges([])
      setError(null)
      return
    }

    try {
      const def = toDefinition()
      const result = await compile(def)
      // If compile returns a graph with nodes/edges, use them
      const graph = result.graph as Record<string, unknown>
      const graphNodes = graph?.nodes as Array<Record<string, unknown>> | undefined
      if (graphNodes && graphNodes.length > 0) {
        const usedNodeIDs = new Set<string>()
        const nodes: Node[] = graphNodes.map((n, i) => {
          const rawID = String(n.id ?? "").trim()
          const baseID = rawID || `compiled_${i + 1}`
          let nodeID = baseID
          let suffix = 2
          while (usedNodeIDs.has(nodeID)) {
            nodeID = `${baseID}_${suffix}`
            suffix++
          }
          usedNodeIDs.add(nodeID)

          const rawType = String(n.type ?? n.kind ?? "").trim()
          return {
            id: nodeID,
            type: "taskPreview",
            position: (() => {
              const columns =
                strategy === "sequential"
                  ? 1
                  : graphNodes.length > 6
                    ? 3
                    : graphNodes.length > 3
                      ? 2
                      : 1
              const col = i % columns
              const row = Math.floor(i / columns)
              return { x: 50 + col * 320, y: 50 + row * 170 }
            })(),
            data: buildNodeData(nodeID, rawType),
            sourcePosition: Position.Bottom,
            targetPosition: Position.Top,
          }
        })
        const graphEdges = (graph?.edges as Array<Record<string, unknown>>) ?? []
        const validNodeIDs = new Set(nodes.map((n) => n.id))
        const edges: Edge[] = graphEdges
          .map((e, i) => ({
            id: `compiled-${i}`,
            source: String(e.source ?? e.from ?? "").trim(),
            target: String(e.target ?? e.to ?? "").trim(),
          }))
          .filter(
            (e) =>
              e.source.length > 0 &&
              e.target.length > 0 &&
              validNodeIDs.has(e.source) &&
              validNodeIDs.has(e.target),
          )
        setCompiledNodes(nodes)
        setCompiledEdges(edges)
      } else {
        // Use fallback
        setCompiledNodes(fallbackLayout.nodes)
        setCompiledEdges(fallbackLayout.edges)
      }
      setError(null)
    } catch {
      // Fall back to simple layout on compile error
      setCompiledNodes(fallbackLayout.nodes)
      setCompiledEdges(fallbackLayout.edges)
      setError(null) // Don't show compile errors in preview
    }
  }, [toDefinition, compile, tasks.length, fallbackLayout, buildNodeData, strategy])

  // Debounced compile on every change
  useEffect(() => {
    if (debounceRef.current) clearTimeout(debounceRef.current)
    debounceRef.current = globalThis.setTimeout(doCompile, 500)
    return () => {
      if (debounceRef.current) clearTimeout(debounceRef.current)
    }
  }, [doCompile])

  const displayNodes = compiledNodes.length > 0 ? compiledNodes : fallbackLayout.nodes
  const displayEdges = compiledEdges.length > 0 ? compiledEdges : fallbackLayout.edges

  if (tasks.length === 0) {
    return (
      <div className="flex h-full items-center justify-center text-xs text-muted-foreground">
        Add tasks to see the execution graph
      </div>
    )
  }

  return (
    <div className="h-full w-full">
      {error && (
        <div className="absolute top-2 left-2 z-10 rounded bg-destructive/10 px-2 py-1 text-xs text-destructive">
          {error}
        </div>
      )}
      <ReactFlow
        nodes={displayNodes}
        edges={displayEdges}
        nodeTypes={previewNodeTypes}
        nodesDraggable={true}
        nodesConnectable={false}
        elementsSelectable={true}
        panOnDrag={true}
        zoomOnScroll={true}
        fitView
        fitViewOptions={{ padding: 0.3 }}
        proOptions={{ hideAttribution: true }}
      >
        <Background variant={BackgroundVariant.Dots} gap={18} size={1} />
      </ReactFlow>
    </div>
  )
}
