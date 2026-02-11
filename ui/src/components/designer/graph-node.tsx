import { memo } from "react"
import { Handle, Position, type NodeProps } from "@xyflow/react"
import type { GraphNodeData, PortHandle } from "@/stores/graph"

/** Color map for port types. */
const portColors: Record<string, string> = {
  string: "#3b82f6",   // blue
  number: "#8b5cf6",   // violet
  boolean: "#f59e0b",  // amber
  object: "#10b981",   // emerald
  array: "#ec4899",    // pink
  any: "#6b7280",      // gray
}

function portColor(type: string): string {
  return portColors[type] ?? portColors.any
}

/** Icons per category. */
const categoryIcons: Record<string, string> = {
  LLM: "\u{1F9E0}",
  Processing: "\u2699\uFE0F",
  Tools: "\u{1F527}",
  "Control Flow": "\u{1F500}",
}

function PortHandles({
  ports,
  position,
  prefix,
  missingPorts,
}: {
  ports: PortHandle[]
  position: Position
  prefix: "in" | "out"
  missingPorts?: string[]
}) {
  if (ports.length === 0) {
    // Default single handle
    return (
      <Handle
        type={prefix === "in" ? "target" : "source"}
        position={position}
        id={prefix === "in" ? "input" : "output"}
        className="!h-2.5 !w-2.5 !border-2 !border-background"
        style={{ background: portColor("any") }}
      />
    )
  }

  const count = ports.length
  return (
    <>
      {ports.map((port, i) => {
        const offset = count === 1 ? 50 : 20 + (i / (count - 1)) * 60
        const isMissing = missingPorts?.includes(port.name)
        const bg = isMissing ? "#ef4444" : portColor(port.type)
        const style =
          position === Position.Left || position === Position.Right
            ? { top: `${offset}%`, background: bg }
            : { left: `${offset}%`, background: bg }

        return (
          <Handle
            key={`${prefix}-${port.name}`}
            type={prefix === "in" ? "target" : "source"}
            position={position}
            id={port.name}
            className="!h-2.5 !w-2.5 !border-2 !border-background"
            style={style}
            title={`${port.name} (${port.type})${port.required ? " *" : ""}`}
          />
        )
      })}
    </>
  )
}

function GraphNodeComponent({ data, selected }: NodeProps & { data: GraphNodeData }) {
  const icon = categoryIcons[data.category] ?? "\u25A0"
  const isOrphaned = (data as Record<string, unknown>)._orphaned === true
  const inCycle = (data as Record<string, unknown>)._inCycle === true
  const missingInputs = ((data as Record<string, unknown>)._missingInputs as string[]) ?? []

  let borderClass = "border-border"
  if (selected) borderClass = "ring-2 ring-primary border-primary"
  else if (inCycle) borderClass = "ring-2 ring-red-500 border-red-500"
  else if (isOrphaned) borderClass = "ring-1 ring-yellow-500 border-yellow-500"

  return (
    <div
      className={`
        rounded-lg border bg-card text-card-foreground shadow-sm
        min-w-[140px] max-w-[200px]
        ${borderClass}
      `}
    >
      {/* Header */}
      <div className="flex items-center gap-1.5 border-b px-2.5 py-1.5">
        <span className="text-sm">{icon}</span>
        <span className="truncate text-xs font-medium">{data.label}</span>
      </div>

      {/* Body */}
      <div className="px-2.5 py-1.5">
        <span className="rounded bg-muted px-1 py-0.5 text-[10px] text-muted-foreground">
          {data.kind}
        </span>
      </div>

      {/* Port labels */}
      {(data.inputPorts.length > 0 || data.outputPorts.length > 0) && (
        <div className="flex justify-between gap-2 px-2.5 pb-1.5 text-[9px] text-muted-foreground">
          <div className="space-y-0.5">
            {data.inputPorts.map((p) => (
              <div key={p.name} className="flex items-center gap-1">
                <span
                  className="inline-block h-1.5 w-1.5 rounded-full"
                  style={{ background: portColor(p.type) }}
                />
                <span>{p.name}</span>
              </div>
            ))}
          </div>
          <div className="space-y-0.5 text-right">
            {data.outputPorts.map((p) => (
              <div key={p.name} className="flex items-center justify-end gap-1">
                <span>{p.name}</span>
                <span
                  className="inline-block h-1.5 w-1.5 rounded-full"
                  style={{ background: portColor(p.type) }}
                />
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Handles */}
      <PortHandles ports={data.inputPorts} position={Position.Left} prefix="in" missingPorts={missingInputs} />
      <PortHandles ports={data.outputPorts} position={Position.Right} prefix="out" />
    </div>
  )
}

export const GraphNode = memo(GraphNodeComponent)
