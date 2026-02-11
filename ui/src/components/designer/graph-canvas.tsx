import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import {
  ReactFlow,
  Background,
  MiniMap,
  Controls,
  type Connection,
  type Edge,
  type NodeMouseHandler,
  type EdgeMouseHandler,
  BackgroundVariant,
  useReactFlow,
} from "@xyflow/react"
import "@xyflow/react/dist/style.css"
import { useGraphStore, makeGraphNodeId, type GraphNodeData } from "@/stores/graph"
import { getGraphValidationVisuals } from "@/lib/graph-validation"
import { GraphNode } from "./graph-node"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Button } from "@/components/ui/button"

/** Port type compatibility rules. */
function typesCompatible(sourceType: string, targetType: string): boolean {
  if (sourceType === targetType) return true
  if (sourceType === "any" || targetType === "any") return true
  // string accepts any type (implicit conversion)
  if (targetType === "string") return true
  return false
}

export function GraphCanvas() {
  const nodes = useGraphStore((s) => s.nodes)
  const edges = useGraphStore((s) => s.edges)
  const selectedNodeId = useGraphStore((s) => s.selectedNodeId)
  const selectedEdgeId = useGraphStore((s) => s.selectedEdgeId)
  const onNodesChange = useGraphStore((s) => s.onNodesChange)
  const onEdgesChange = useGraphStore((s) => s.onEdgesChange)
  const addEdgeFromConnection = useGraphStore((s) => s.addEdgeFromConnection)
  const addNode = useGraphStore((s) => s.addNode)
  const removeNodes = useGraphStore((s) => s.removeNodes)
  const removeEdges = useGraphStore((s) => s.removeEdges)
  const selectNode = useGraphStore((s) => s.selectNode)
  const selectEdge = useGraphStore((s) => s.selectEdge)
  const undo = useGraphStore((s) => s.undo)
  const redo = useGraphStore((s) => s.redo)
  const copySelected = useGraphStore((s) => s.copySelected)
  const paste = useGraphStore((s) => s.paste)

  const [deleteConfirm, setDeleteConfirm] = useState<{
    type: "node" | "edge"
    ids: string[]
    label: string
  } | null>(null)

  const reactFlowWrapper = useRef<HTMLDivElement>(null)
  const { screenToFlowPosition } = useReactFlow()

  const nodeTypes = useMemo(() => ({ graphNode: GraphNode }), [])

  // Keyboard shortcuts: Ctrl+Z/Y undo/redo, Ctrl+C/V copy/paste
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      // Skip when typing in an input/textarea
      const tag = (e.target as HTMLElement).tagName
      if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT") return

      if ((e.ctrlKey || e.metaKey) && e.key === "z" && !e.shiftKey) {
        e.preventDefault()
        undo()
      }
      if (
        ((e.ctrlKey || e.metaKey) && e.key === "y") ||
        ((e.ctrlKey || e.metaKey) && e.shiftKey && e.key === "z")
      ) {
        e.preventDefault()
        redo()
      }
      if ((e.ctrlKey || e.metaKey) && e.key === "c") {
        e.preventDefault()
        copySelected()
      }
      if ((e.ctrlKey || e.metaKey) && e.key === "v") {
        e.preventDefault()
        paste()
      }
      if (e.key === "Delete" || e.key === "Backspace") {
        const { selectedNodeId: nid, selectedEdgeId: eid, nodes: ns } = useGraphStore.getState()
        if (nid) {
          const node = ns.find((n) => n.id === nid)
          setDeleteConfirm({
            type: "node",
            ids: [nid],
            label: node?.data.label ?? nid,
          })
        } else if (eid) {
          setDeleteConfirm({ type: "edge", ids: [eid], label: eid })
        }
      }
    }
    window.addEventListener("keydown", handleKeyDown)
    return () => window.removeEventListener("keydown", handleKeyDown)
  }, [undo, redo, copySelected, paste])

  /** Validate connection before allowing edge creation. */
  const isValidConnection = useCallback(
    (connection: Connection | Edge) => {
      if (connection.source === connection.target) return false

      const srcHandle = connection.sourceHandle ?? null
      const tgtHandle = connection.targetHandle ?? null

      const exists = edges.some(
        (e) =>
          e.source === connection.source &&
          e.target === connection.target &&
          e.sourceHandle === srcHandle &&
          e.targetHandle === tgtHandle,
      )
      if (exists) return false

      const sourceNode = nodes.find((n) => n.id === connection.source)
      const targetNode = nodes.find((n) => n.id === connection.target)
      if (!sourceNode || !targetNode) return false

      const sourcePort = sourceNode.data.outputPorts.find(
        (p) => p.name === srcHandle,
      ) ?? { type: "any" }
      const targetPort = targetNode.data.inputPorts.find(
        (p) => p.name === tgtHandle,
      ) ?? { type: "any" }

      return typesCompatible(sourcePort.type, targetPort.type)
    },
    [nodes, edges],
  )

  const onConnect = useCallback(
    (connection: Connection) => {
      addEdgeFromConnection(connection)
    },
    [addEdgeFromConnection],
  )

  const onNodeClick: NodeMouseHandler = useCallback(
    (_event, node) => {
      selectNode(node.id)
    },
    [selectNode],
  )

  const onEdgeClick: EdgeMouseHandler = useCallback(
    (_event, edge) => {
      selectEdge(edge.id)
    },
    [selectEdge],
  )

  const onPaneClick = useCallback(() => {
    selectNode(null)
    selectEdge(null)
  }, [selectNode, selectEdge])

  /** Handle drop from node palette. */
  const onDragOver = useCallback((e: React.DragEvent) => {
    e.preventDefault()
    e.dataTransfer.dropEffect = "move"
  }, [])

  const onDrop = useCallback(
    (e: React.DragEvent) => {
      e.preventDefault()
      const raw = e.dataTransfer.getData("application/petalflow-node")
      if (!raw) return

      try {
        const item = JSON.parse(raw) as {
          kind: string
          category: string
          label: string
          inputPorts: GraphNodeData["inputPorts"]
          outputPorts: GraphNodeData["outputPorts"]
        }
        const position = screenToFlowPosition({
          x: e.clientX,
          y: e.clientY,
        })
        const id = makeGraphNodeId(item.kind.replace(/\./g, "_"))
        addNode({
          id,
          type: "graphNode",
          position,
          data: {
            label: id,
            kind: item.kind,
            category: item.category,
            config: {},
            inputPorts: item.inputPorts,
            outputPorts: item.outputPorts,
          },
        })
      } catch {
        // invalid drag data
      }
    },
    [screenToFlowPosition, addNode],
  )

  // Compute validation visuals
  const validation = useMemo(
    () => getGraphValidationVisuals(nodes, edges),
    [nodes, edges],
  )

  // Style edges based on selection + validation
  const styledEdges = useMemo(() => {
    return edges.map((e) => {
      const isMismatch = validation.mismatchEdges.has(e.id)
      const isSelected = e.id === selectedEdgeId
      return {
        ...e,
        animated: isSelected,
        style: {
          stroke: isMismatch
            ? "#ef4444"
            : isSelected
              ? "hsl(var(--primary))"
              : undefined,
          strokeWidth: isSelected || isMismatch ? 2 : 1,
        },
      }
    })
  }, [edges, selectedEdgeId, validation.mismatchEdges])

  // Style nodes based on selection + validation
  const styledNodes = useMemo(() => {
    return nodes.map((n) => ({
      ...n,
      selected: n.id === selectedNodeId,
      data: {
        ...n.data,
        _orphaned: validation.orphanedNodes.has(n.id),
        _inCycle: validation.cycleNodes.has(n.id),
        _missingInputs: [...validation.missingInputPorts]
          .filter((k) => k.startsWith(`${n.id}:`))
          .map((k) => k.split(":")[1]),
      },
    }))
  }, [nodes, selectedNodeId, validation])

  return (
    <div className="h-full w-full" ref={reactFlowWrapper}>
      <ReactFlow
        nodes={styledNodes}
        edges={styledEdges}
        onNodesChange={onNodesChange}
        onEdgesChange={onEdgesChange}
        onConnect={onConnect}
        onNodeClick={onNodeClick}
        onEdgeClick={onEdgeClick}
        onPaneClick={onPaneClick}
        onDragOver={onDragOver}
        onDrop={onDrop}
        isValidConnection={isValidConnection}
        nodeTypes={nodeTypes}
        fitView
        fitViewOptions={{ padding: 0.3 }}
        snapToGrid
        snapGrid={[16, 16]}
        deleteKeyCode={null}
        multiSelectionKeyCode="Shift"
        proOptions={{ hideAttribution: true }}
        defaultEdgeOptions={{
          type: "smoothstep",
        }}
        connectionLineStyle={{ stroke: "hsl(var(--primary))", strokeWidth: 2 }}
      >
        <Background variant={BackgroundVariant.Dots} gap={16} size={1} />
        <MiniMap
          className="!bg-muted/50 !border-border"
          maskColor="hsl(var(--muted) / 0.5)"
          nodeColor={(node) => {
            const data = node.data as GraphNodeData
            const colors: Record<string, string> = {
              LLM: "#3b82f6",
              Processing: "#10b981",
              Tools: "#f59e0b",
              "Control Flow": "#ec4899",
            }
            return colors[data.category] ?? "#6b7280"
          }}
        />
        <Controls className="!border-border !bg-card !shadow-sm" />
      </ReactFlow>

      {/* Delete confirmation dialog */}
      <Dialog
        open={deleteConfirm !== null}
        onOpenChange={(open) => { if (!open) setDeleteConfirm(null) }}
      >
        <DialogContent className="max-w-sm">
          <DialogHeader>
            <DialogTitle>
              Delete {deleteConfirm?.type === "node" ? "Node" : "Edge"}
            </DialogTitle>
            <DialogDescription>
              Are you sure you want to delete{" "}
              <span className="font-medium">{deleteConfirm?.label}</span>?
              {deleteConfirm?.type === "node" &&
                " Connected edges will also be removed."}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button
              variant="outline"
              size="sm"
              onClick={() => setDeleteConfirm(null)}
            >
              Cancel
            </Button>
            <Button
              variant="destructive"
              size="sm"
              onClick={() => {
                if (deleteConfirm?.type === "node") {
                  removeNodes(deleteConfirm.ids)
                } else if (deleteConfirm?.type === "edge") {
                  removeEdges(deleteConfirm.ids)
                }
                setDeleteConfirm(null)
              }}
            >
              Delete
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
