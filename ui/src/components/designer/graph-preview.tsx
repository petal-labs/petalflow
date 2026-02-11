import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import {
  ReactFlow,
  Background,
  type Node,
  type Edge,
  Position,
} from "@xyflow/react"
import "@xyflow/react/dist/style.css"
import { useEditorStore } from "@/stores/editor"
import { useWorkflowStore } from "@/stores/workflows"

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

  // Build a simple fallback layout from the editor state directly
  const fallbackLayout = useMemo(() => {
    const nodes: Node[] = []
    const edges: Edge[] = []
    const ySpacing = 100
    const xOffset = 50

    tasks.forEach((task, i) => {
      const agent = agents.find((a) => a.id === task.agent)
      nodes.push({
        id: task.id,
        position: { x: xOffset, y: i * ySpacing + 50 },
        data: {
          label: `${task.id}${agent ? ` (${agent.role || agent.id})` : ""}`,
        },
        sourcePosition: Position.Bottom,
        targetPosition: Position.Top,
      })

      // Sequential edges
      if (strategy === "sequential" && i > 0) {
        edges.push({
          id: `e-${tasks[i - 1].id}-${task.id}`,
          source: tasks[i - 1].id,
          target: task.id,
        })
      }
    })

    return { nodes, edges }
  }, [agents, tasks, strategy])

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
        const nodes: Node[] = graphNodes.map((n, i) => ({
          id: String(n.id),
          position: { x: 50, y: i * 100 + 50 },
          data: { label: String(n.kind ?? n.id) },
          sourcePosition: Position.Bottom,
          targetPosition: Position.Top,
        }))
        const graphEdges = (graph?.edges as Array<Record<string, unknown>>) ?? []
        const edges: Edge[] = graphEdges.map((e, i) => ({
          id: `compiled-${i}`,
          source: String(e.source ?? e.from),
          target: String(e.target ?? e.to),
        }))
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
  }, [toDefinition, compile, tasks.length, fallbackLayout])

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
        nodesDraggable={false}
        nodesConnectable={false}
        elementsSelectable={false}
        panOnDrag={true}
        zoomOnScroll={true}
        fitView
        fitViewOptions={{ padding: 0.3 }}
        proOptions={{ hideAttribution: true }}
      >
        <Background />
      </ReactFlow>
    </div>
  )
}
