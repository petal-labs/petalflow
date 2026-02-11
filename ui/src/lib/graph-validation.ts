import type { Node, Edge } from "@xyflow/react"
import type { GraphNodeData } from "@/stores/graph"
import type { ValidationDiagnostic } from "@/api/types"

/** Port type compatibility rules (mirrors graph-canvas.tsx). */
function typesCompatible(sourceType: string, targetType: string): boolean {
  if (sourceType === targetType) return true
  if (sourceType === "any" || targetType === "any") return true
  if (targetType === "string") return true
  return false
}

/** Detect cycles using DFS. Returns node IDs involved in cycles. */
function detectCycles(
  nodes: Node<GraphNodeData>[],
  edges: Edge[],
): Set<string> {
  const adj = new Map<string, string[]>()
  for (const n of nodes) adj.set(n.id, [])
  for (const e of edges) {
    adj.get(e.source)?.push(e.target)
  }

  const cycleNodes = new Set<string>()
  const WHITE = 0, GRAY = 1, BLACK = 2
  const color = new Map<string, number>()
  for (const n of nodes) color.set(n.id, WHITE)

  const parent = new Map<string, string | null>()

  function dfs(u: string) {
    color.set(u, GRAY)
    for (const v of adj.get(u) ?? []) {
      if (color.get(v) === GRAY) {
        // Back edge — found a cycle. Trace back.
        cycleNodes.add(v)
        let cur = u
        while (cur !== v) {
          cycleNodes.add(cur)
          cur = parent.get(cur) ?? v
        }
      } else if (color.get(v) === WHITE) {
        parent.set(v, u)
        dfs(v)
      }
    }
    color.set(u, BLACK)
  }

  for (const n of nodes) {
    if (color.get(n.id) === WHITE) {
      parent.set(n.id, null)
      dfs(n.id)
    }
  }

  return cycleNodes
}

/**
 * Run client-side validation on a graph and return diagnostics.
 * Checks: orphaned nodes, type mismatches, missing required inputs, cycles.
 */
export function validateGraph(
  nodes: Node<GraphNodeData>[],
  edges: Edge[],
): ValidationDiagnostic[] {
  const diagnostics: ValidationDiagnostic[] = []

  if (nodes.length === 0) return diagnostics

  // 1. Orphaned nodes (no connected edges)
  const connectedNodeIds = new Set<string>()
  for (const e of edges) {
    connectedNodeIds.add(e.source)
    connectedNodeIds.add(e.target)
  }
  for (const node of nodes) {
    if (!connectedNodeIds.has(node.id)) {
      diagnostics.push({
        severity: "warning",
        message: `Node "${node.data.label}" has no connections`,
        path: `node:${node.id}`,
      })
    }
  }

  // 2. Type mismatches on edges
  for (const edge of edges) {
    const srcNode = nodes.find((n) => n.id === edge.source)
    const tgtNode = nodes.find((n) => n.id === edge.target)
    if (!srcNode || !tgtNode) continue

    const srcPort = srcNode.data.outputPorts.find(
      (p) => p.name === (edge.sourceHandle ?? "output"),
    )
    const tgtPort = tgtNode.data.inputPorts.find(
      (p) => p.name === (edge.targetHandle ?? "input"),
    )

    if (srcPort && tgtPort && !typesCompatible(srcPort.type, tgtPort.type)) {
      diagnostics.push({
        severity: "error",
        message: `Type mismatch: ${srcNode.data.label}.${srcPort.name} (${srcPort.type}) → ${tgtNode.data.label}.${tgtPort.name} (${tgtPort.type})`,
        path: `edge:${edge.id}`,
      })
    }
  }

  // 3. Missing required input ports (not wired)
  const wiredInputs = new Map<string, Set<string>>()
  for (const edge of edges) {
    const handle = edge.targetHandle ?? "input"
    if (!wiredInputs.has(edge.target)) wiredInputs.set(edge.target, new Set())
    wiredInputs.get(edge.target)!.add(handle)
  }
  for (const node of nodes) {
    const wired = wiredInputs.get(node.id) ?? new Set()
    for (const port of node.data.inputPorts) {
      if (port.required && !wired.has(port.name)) {
        diagnostics.push({
          severity: "error",
          message: `Required input "${port.name}" on "${node.data.label}" is not connected`,
          path: `node:${node.id}:port:${port.name}`,
        })
      }
    }
  }

  // 4. Cycle detection
  const cycleNodes = detectCycles(nodes, edges)
  if (cycleNodes.size > 0) {
    const labels = nodes
      .filter((n) => cycleNodes.has(n.id))
      .map((n) => n.data.label)
    diagnostics.push({
      severity: "error",
      message: `Cycle detected involving: ${labels.join(", ")}`,
      path: [...cycleNodes].map((id) => `node:${id}`).join(","),
    })
  }

  return diagnostics
}

/**
 * Return per-node and per-edge visual status for graph rendering.
 */
export interface GraphValidationVisuals {
  /** Node IDs with orphan warning (yellow border). */
  orphanedNodes: Set<string>
  /** Edge IDs with type mismatch (red). */
  mismatchEdges: Set<string>
  /** Node IDs involved in cycles (red highlight). */
  cycleNodes: Set<string>
  /** "nodeId:portName" keys for missing required inputs. */
  missingInputPorts: Set<string>
}

export function getGraphValidationVisuals(
  nodes: Node<GraphNodeData>[],
  edges: Edge[],
): GraphValidationVisuals {
  const orphanedNodes = new Set<string>()
  const mismatchEdges = new Set<string>()
  const missingInputPorts = new Set<string>()

  if (nodes.length === 0) {
    return { orphanedNodes, mismatchEdges, cycleNodes: new Set(), missingInputPorts }
  }

  // Orphaned
  const connectedNodeIds = new Set<string>()
  for (const e of edges) {
    connectedNodeIds.add(e.source)
    connectedNodeIds.add(e.target)
  }
  for (const node of nodes) {
    if (!connectedNodeIds.has(node.id)) orphanedNodes.add(node.id)
  }

  // Type mismatches
  for (const edge of edges) {
    const srcNode = nodes.find((n) => n.id === edge.source)
    const tgtNode = nodes.find((n) => n.id === edge.target)
    if (!srcNode || !tgtNode) continue
    const srcPort = srcNode.data.outputPorts.find(
      (p) => p.name === (edge.sourceHandle ?? "output"),
    )
    const tgtPort = tgtNode.data.inputPorts.find(
      (p) => p.name === (edge.targetHandle ?? "input"),
    )
    if (srcPort && tgtPort && !typesCompatible(srcPort.type, tgtPort.type)) {
      mismatchEdges.add(edge.id)
    }
  }

  // Missing required inputs
  const wiredInputs = new Map<string, Set<string>>()
  for (const edge of edges) {
    const handle = edge.targetHandle ?? "input"
    if (!wiredInputs.has(edge.target)) wiredInputs.set(edge.target, new Set())
    wiredInputs.get(edge.target)!.add(handle)
  }
  for (const node of nodes) {
    const wired = wiredInputs.get(node.id) ?? new Set()
    for (const port of node.data.inputPorts) {
      if (port.required && !wired.has(port.name)) {
        missingInputPorts.add(`${node.id}:${port.name}`)
      }
    }
  }

  // Cycles
  const cycleNodes = detectCycles(nodes, edges)

  return { orphanedNodes, mismatchEdges, cycleNodes, missingInputPorts }
}
