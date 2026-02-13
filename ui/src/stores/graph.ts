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

const fallbackPortSchemas: Record<string, { inputs: PortHandle[]; outputs: PortHandle[] }> = {
  llm_prompt: {
    inputs: [
      { name: "input", type: "string", required: true },
      { name: "context", type: "string" },
    ],
    outputs: [{ name: "output", type: "string" }],
  },
  llm_router: {
    inputs: [{ name: "input", type: "string", required: true }],
    outputs: [
      { name: "output", type: "string" },
      { name: "decision", type: "object" },
    ],
  },
  conditional: {
    inputs: [{ name: "input", type: "any", required: true }],
    outputs: [{ name: "output", type: "any" }],
  },
  gate: {
    inputs: [{ name: "input", type: "any", required: true }],
    outputs: [{ name: "output", type: "any" }],
  },
  human: {
    inputs: [{ name: "input", type: "any", required: true }],
    outputs: [
      { name: "output", type: "any" },
      { name: "response", type: "object" },
    ],
  },
  merge: {
    inputs: [{ name: "input", type: "any", required: true }],
    outputs: [{ name: "output", type: "any" }],
  },
  transform: {
    inputs: [{ name: "input", type: "any", required: true }],
    outputs: [{ name: "output", type: "any" }],
  },
  filter: {
    inputs: [{ name: "input", type: "array", required: true }],
    outputs: [{ name: "output", type: "array" }],
  },
  tool: {
    inputs: [{ name: "input", type: "object", required: true }],
    outputs: [{ name: "output", type: "object" }],
  },
}

function asRecord(value: unknown): Record<string, unknown> | null {
  if (!value || typeof value !== "object" || Array.isArray(value)) return null
  return value as Record<string, unknown>
}

function normalizePorts(value: unknown): PortHandle[] {
  if (!Array.isArray(value)) return []
  const ports: PortHandle[] = []
  for (const rawPort of value) {
    const port = asRecord(rawPort)
    if (!port) continue
    const name = typeof port.name === "string" ? port.name.trim() : ""
    if (!name) continue
    const type =
      typeof port.type === "string" && port.type.trim().length > 0
        ? port.type
        : "any"
    const normalizedPort: PortHandle = { name, type }
    if (typeof port.required === "boolean") {
      normalizedPort.required = port.required
    }
    ports.push(normalizedPort)
  }
  return ports
}

function inferCategory(kind: string): string {
  if (kind === "llm_prompt" || kind === "llm_router") return "LLM"
  if (
    kind === "conditional" ||
    kind === "gate" ||
    kind === "human" ||
    kind === "merge"
  ) {
    return "Control Flow"
  }
  if (
    kind === "transform" ||
    kind === "filter" ||
    kind === "json_parse" ||
    kind === "json_format" ||
    kind === "cache" ||
    kind === "sink"
  ) {
    return "Processing"
  }
  if (kind === "tool" || kind.includes(".")) return "Tools"
  return "Processing"
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
      const kind =
        (typeof n.kind === "string" && n.kind.trim().length > 0
          ? n.kind
          : typeof n.type === "string" && n.type.trim().length > 0
            ? n.type
            : "unknown")
      const category =
        typeof n.category === "string" && n.category.trim().length > 0
          ? n.category
          : inferCategory(kind)
      const fallbackPorts = fallbackPortSchemas[kind]
      const normalizedInputPorts = normalizePorts(n.input_ports ?? n.inputs ?? null)
      const normalizedOutputPorts = normalizePorts(n.output_ports ?? n.outputs ?? null)
      const inputPorts =
        normalizedInputPorts.length > 0
          ? normalizedInputPorts
          : fallbackPorts?.inputs ?? []
      const outputPorts =
        normalizedOutputPorts.length > 0
          ? normalizedOutputPorts
          : fallbackPorts?.outputs ?? []
      return {
        id: String(n.id),
        type: "graphNode",
        position: (n.position as { x: number; y: number }) ?? { x: 50, y: i * 120 + 50 },
        data: {
          label:
            typeof n.label === "string" && n.label.trim().length > 0
              ? n.label
              : String(n.id),
          kind,
          category,
          config: (n.config as Record<string, unknown>) ?? {},
          inputPorts,
          outputPorts,
        },
      }
    })

    const inputHandlesByNode = new Map<string, Set<string>>()
    const outputHandlesByNode = new Map<string, Set<string>>()
    for (const node of nodes) {
      inputHandlesByNode.set(
        node.id,
        new Set(node.data.inputPorts.map((port) => port.name)),
      )
      outputHandlesByNode.set(
        node.id,
        new Set(node.data.outputPorts.map((port) => port.name)),
      )
    }

    const dedupedEdges = new Map<string, Edge>()
    for (const [i, e] of rawEdges.entries()) {
      const source = String(e.source ?? e.from)
      const target = String(e.target ?? e.to)
      const rawSourceHandle = e.source_port
        ? String(e.source_port)
        : e.sourceHandle
          ? String(e.sourceHandle)
          : undefined
      const rawTargetHandle = e.target_port
        ? String(e.target_port)
        : e.targetHandle
          ? String(e.targetHandle)
          : undefined

      const sourceHandles = outputHandlesByNode.get(source)
      const targetHandles = inputHandlesByNode.get(target)
      const sourceHandle = rawSourceHandle
        ? sourceHandles?.has(rawSourceHandle)
          ? rawSourceHandle
          : sourceHandles?.has("output")
            ? "output"
            : undefined
        : undefined
      const targetHandle = rawTargetHandle
        ? targetHandles?.has(rawTargetHandle)
          ? rawTargetHandle
          : targetHandles?.has("input")
            ? "input"
            : undefined
        : undefined

      const edge: Edge = {
        id: String(e.id ?? `e-${i}`),
        source,
        target,
        sourceHandle,
        targetHandle,
        label: sourceHandle && targetHandle
          ? `${sourceHandle} → ${targetHandle}`
          : undefined,
      }
      const edgeKey = `${source}|${sourceHandle ?? ""}|${target}|${targetHandle ?? ""}`
      if (!dedupedEdges.has(edgeKey)) {
        dedupedEdges.set(edgeKey, edge)
      }
    }
    const edges = [...dedupedEdges.values()]

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
