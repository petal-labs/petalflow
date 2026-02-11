import { create } from "zustand"
import {
  type Node,
  type Edge,
  type OnNodesChange,
  type OnEdgesChange,
  type Connection,
  applyNodeChanges,
  applyEdgeChanges,
  addEdge as rfAddEdge,
} from "@xyflow/react"

// ---------------------------------------------------------------------------
// Graph mode editor state
// ---------------------------------------------------------------------------

/** Config stored on each graph node's data. */
export interface GraphNodeData {
  label: string
  kind: string
  category: string
  config: Record<string, unknown>
  inputPorts: PortHandle[]
  outputPorts: PortHandle[]
  [key: string]: unknown
}

export interface PortHandle {
  name: string
  type: string // "string" | "number" | "boolean" | "object" | "array" | "any"
  required?: boolean
}

interface HistoryEntry {
  nodes: Node<GraphNodeData>[]
  edges: Edge[]
}

export interface GraphState {
  nodes: Node<GraphNodeData>[]
  edges: Edge[]

  /** Currently selected node id. */
  selectedNodeId: string | null
  /** Currently selected edge id. */
  selectedEdgeId: string | null

  // React Flow change handlers
  onNodesChange: OnNodesChange<Node<GraphNodeData>>
  onEdgesChange: OnEdgesChange

  // Mutations
  addNode: (node: Node<GraphNodeData>) => void
  removeNodes: (ids: string[]) => void
  updateNodeData: (id: string, data: Partial<GraphNodeData>) => void
  updateNodeConfig: (id: string, config: Record<string, unknown>) => void
  updateNodePosition: (id: string, position: { x: number; y: number }) => void

  addEdgeFromConnection: (connection: Connection) => void
  removeEdges: (ids: string[]) => void

  selectNode: (id: string | null) => void
  selectEdge: (id: string | null) => void

  // Copy / Paste
  clipboard: Node<GraphNodeData>[]
  copySelected: () => void
  paste: () => void

  // Undo / Redo
  undo: () => void
  redo: () => void
  canUndo: boolean
  canRedo: boolean

  // Serialization
  loadFromGraphIR: (graph: Record<string, unknown>) => void
  toGraphIR: () => Record<string, unknown>

  /** Reset all state. */
  reset: () => void
}

let nodeCounter = 0

export function makeGraphNodeId(kind: string) {
  nodeCounter++
  return `${kind}_${nodeCounter}`
}

/** Save current state for undo. */
function pushHistory(history: HistoryEntry[], nodes: Node<GraphNodeData>[], edges: Edge[]): HistoryEntry[] {
  const entry: HistoryEntry = {
    nodes: structuredClone(nodes),
    edges: structuredClone(edges),
  }
  // Cap history at 50 entries
  const next = [...history, entry]
  if (next.length > 50) next.shift()
  return next
}

export const useGraphStore = create<GraphState>((set, get) => ({
  nodes: [],
  edges: [],
  selectedNodeId: null,
  selectedEdgeId: null,
  clipboard: [],
  canUndo: false,
  canRedo: false,

  // Internal undo/redo stacks (not exposed in type but stored via closure)
  ...({} as { _undoStack: HistoryEntry[]; _redoStack: HistoryEntry[] }),

  onNodesChange(changes) {
    set((s) => ({
      nodes: applyNodeChanges(changes, s.nodes) as Node<GraphNodeData>[],
    }))
  },

  onEdgesChange(changes) {
    set((s) => ({
      edges: applyEdgeChanges(changes, s.edges),
    }))
  },

  addNode(node) {
    const { nodes, edges } = get()
    const _undoStack = pushHistory((get() as unknown as { _undoStack: HistoryEntry[] })._undoStack ?? [], nodes, edges)
    set({
      nodes: [...nodes, node],
      _undoStack,
      _redoStack: [],
      canUndo: true,
      canRedo: false,
    } as Partial<GraphState>)
  },

  removeNodes(ids) {
    const { nodes, edges } = get()
    const _undoStack = pushHistory((get() as unknown as { _undoStack: HistoryEntry[] })._undoStack ?? [], nodes, edges)
    const idSet = new Set(ids)
    set({
      nodes: nodes.filter((n) => !idSet.has(n.id)),
      edges: edges.filter((e) => !idSet.has(e.source) && !idSet.has(e.target)),
      selectedNodeId: ids.includes(get().selectedNodeId ?? "") ? null : get().selectedNodeId,
      _undoStack,
      _redoStack: [],
      canUndo: true,
      canRedo: false,
    } as Partial<GraphState>)
  },

  updateNodeData(id, data) {
    set((s) => ({
      nodes: s.nodes.map((n) =>
        n.id === id ? { ...n, data: { ...n.data, ...data } } : n,
      ),
    }))
  },

  updateNodeConfig(id, config) {
    set((s) => ({
      nodes: s.nodes.map((n) =>
        n.id === id
          ? { ...n, data: { ...n.data, config: { ...n.data.config, ...config } } }
          : n,
      ),
    }))
  },

  updateNodePosition(id, position) {
    set((s) => ({
      nodes: s.nodes.map((n) => (n.id === id ? { ...n, position } : n)),
    }))
  },

  addEdgeFromConnection(connection) {
    const { nodes, edges } = get()
    const _undoStack = pushHistory((get() as unknown as { _undoStack: HistoryEntry[] })._undoStack ?? [], nodes, edges)
    set({
      edges: rfAddEdge(connection, edges),
      _undoStack,
      _redoStack: [],
      canUndo: true,
      canRedo: false,
    } as Partial<GraphState>)
  },

  removeEdges(ids) {
    const { nodes, edges } = get()
    const _undoStack = pushHistory((get() as unknown as { _undoStack: HistoryEntry[] })._undoStack ?? [], nodes, edges)
    const idSet = new Set(ids)
    set({
      edges: edges.filter((e) => !idSet.has(e.id)),
      selectedEdgeId: ids.includes(get().selectedEdgeId ?? "") ? null : get().selectedEdgeId,
      _undoStack,
      _redoStack: [],
      canUndo: true,
      canRedo: false,
    } as Partial<GraphState>)
  },

  selectNode(id) {
    set({ selectedNodeId: id, selectedEdgeId: id ? null : get().selectedEdgeId })
  },

  selectEdge(id) {
    set({ selectedEdgeId: id, selectedNodeId: id ? null : get().selectedNodeId })
  },

  copySelected() {
    const { nodes, selectedNodeId } = get()
    if (!selectedNodeId) return
    // Copy all selected nodes (React Flow marks selected nodes)
    const selected = nodes.filter((n) => n.selected || n.id === selectedNodeId)
    set({ clipboard: structuredClone(selected) })
  },

  paste() {
    const { clipboard, nodes, edges } = get()
    if (clipboard.length === 0) return
    const _undoStack = pushHistory((get() as unknown as { _undoStack: HistoryEntry[] })._undoStack ?? [], nodes, edges)
    const offset = 40
    const newNodes = clipboard.map((n) => {
      const id = makeGraphNodeId(n.data.kind.replace(/\./g, "_"))
      return {
        ...structuredClone(n),
        id,
        position: { x: n.position.x + offset, y: n.position.y + offset },
        selected: false,
      }
    })
    set({
      nodes: [...nodes, ...newNodes],
      _undoStack,
      _redoStack: [],
      canUndo: true,
      canRedo: false,
    } as Partial<GraphState>)
  },

  undo() {
    const stack = (get() as unknown as { _undoStack: HistoryEntry[] })._undoStack ?? []
    if (stack.length === 0) return
    const { nodes, edges } = get()
    const entry = stack[stack.length - 1]
    const newUndo = stack.slice(0, -1)
    const redo = (get() as unknown as { _redoStack: HistoryEntry[] })._redoStack ?? []
    const newRedo = [...redo, { nodes: structuredClone(nodes), edges: structuredClone(edges) }]
    set({
      nodes: entry.nodes,
      edges: entry.edges,
      _undoStack: newUndo,
      _redoStack: newRedo,
      canUndo: newUndo.length > 0,
      canRedo: true,
    } as Partial<GraphState>)
  },

  redo() {
    const stack = (get() as unknown as { _redoStack: HistoryEntry[] })._redoStack ?? []
    if (stack.length === 0) return
    const { nodes, edges } = get()
    const entry = stack[stack.length - 1]
    const newRedo = stack.slice(0, -1)
    const undo = (get() as unknown as { _undoStack: HistoryEntry[] })._undoStack ?? []
    const newUndo = [...undo, { nodes: structuredClone(nodes), edges: structuredClone(edges) }]
    set({
      nodes: entry.nodes,
      edges: entry.edges,
      _undoStack: newUndo,
      _redoStack: newRedo,
      canUndo: true,
      canRedo: newRedo.length > 0,
    } as Partial<GraphState>)
  },

  loadFromGraphIR(graph) {
    const rawNodes = (graph.nodes as Array<Record<string, unknown>>) ?? []
    const rawEdges = (graph.edges as Array<Record<string, unknown>>) ?? []

    const nodes: Node<GraphNodeData>[] = rawNodes.map((n, i) => {
      const inputPorts = ((n.input_ports ?? n.inputs) as PortHandle[]) ?? []
      const outputPorts = ((n.output_ports ?? n.outputs) as PortHandle[]) ?? []
      return {
        id: String(n.id),
        type: "graphNode",
        position: (n.position as { x: number; y: number }) ?? { x: 50, y: i * 120 + 50 },
        data: {
          label: String(n.id),
          kind: String(n.kind ?? "unknown"),
          category: String(n.category ?? "Processing"),
          config: (n.config as Record<string, unknown>) ?? {},
          inputPorts,
          outputPorts,
        },
      }
    })

    const edges: Edge[] = rawEdges.map((e, i) => ({
      id: String(e.id ?? `e-${i}`),
      source: String(e.source ?? e.from),
      target: String(e.target ?? e.to),
      sourceHandle: e.source_port ? String(e.source_port) : undefined,
      targetHandle: e.target_port ? String(e.target_port) : undefined,
      label: e.source_port && e.target_port
        ? `${e.source_port} → ${e.target_port}`
        : undefined,
    }))

    // Reset counters
    nodeCounter = nodes.length

    set({
      nodes,
      edges,
      selectedNodeId: null,
      selectedEdgeId: null,
      _undoStack: [],
      _redoStack: [],
      canUndo: false,
      canRedo: false,
    } as Partial<GraphState>)
  },

  toGraphIR() {
    const { nodes, edges } = get()
    return {
      nodes: nodes.map((n) => ({
        id: n.id,
        kind: n.data.kind,
        category: n.data.category,
        position: n.position,
        config: n.data.config,
        input_ports: n.data.inputPorts,
        output_ports: n.data.outputPorts,
      })),
      edges: edges.map((e) => ({
        id: e.id,
        source: e.source,
        target: e.target,
        source_port: e.sourceHandle ?? "output",
        target_port: e.targetHandle ?? "input",
      })),
    }
  },

  reset() {
    nodeCounter = 0
    set({
      nodes: [],
      edges: [],
      selectedNodeId: null,
      selectedEdgeId: null,
      _undoStack: [],
      _redoStack: [],
      canUndo: false,
      canRedo: false,
    } as Partial<GraphState>)
  },
}))
