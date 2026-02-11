import { Button } from "@/components/ui/button"
import { Separator } from "@/components/ui/separator"
import { useGraphStore } from "@/stores/graph"

interface EdgeInspectorProps {
  edgeId: string
}

export function EdgeInspector({ edgeId }: EdgeInspectorProps) {
  const edge = useGraphStore((s) => s.edges.find((e) => e.id === edgeId))
  const nodes = useGraphStore((s) => s.nodes)
  const removeEdges = useGraphStore((s) => s.removeEdges)

  if (!edge) {
    return (
      <div className="flex h-full items-center justify-center p-4 text-xs text-muted-foreground">
        Edge not found
      </div>
    )
  }

  const sourceNode = nodes.find((n) => n.id === edge.source)
  const targetNode = nodes.find((n) => n.id === edge.target)

  const sourcePort = edge.sourceHandle ?? "output"
  const targetPort = edge.targetHandle ?? "input"

  const sourcePortDef = sourceNode?.data.outputPorts.find(
    (p) => p.name === sourcePort,
  )
  const targetPortDef = targetNode?.data.inputPorts.find(
    (p) => p.name === targetPort,
  )

  return (
    <div className="flex h-full flex-col">
      <div className="flex items-center justify-between border-b px-3 py-2">
        <div className="text-xs font-medium">Edge</div>
        <Button
          variant="ghost"
          size="sm"
          className="h-6 text-xs text-destructive"
          onClick={() => removeEdges([edgeId])}
        >
          Delete
        </Button>
      </div>

      <div className="flex-1 overflow-y-auto p-3 space-y-3">
        {/* Source */}
        <div className="space-y-1">
          <span className="text-[10px] font-semibold uppercase text-muted-foreground">
            Source
          </span>
          <div className="rounded border p-2 space-y-1">
            <div className="flex items-center justify-between text-xs">
              <span className="font-medium">
                {sourceNode?.data.label ?? edge.source}
              </span>
              <span className="rounded bg-muted px-1 py-0.5 text-[10px]">
                {sourceNode?.data.kind ?? "unknown"}
              </span>
            </div>
            <div className="text-[10px] text-muted-foreground">
              Port: <span className="font-mono">{sourcePort}</span>
              {sourcePortDef && (
                <span className="ml-1">({sourcePortDef.type})</span>
              )}
            </div>
          </div>
        </div>

        {/* Arrow */}
        <div className="flex justify-center text-muted-foreground">
          <span className="text-sm">&darr;</span>
        </div>

        {/* Target */}
        <div className="space-y-1">
          <span className="text-[10px] font-semibold uppercase text-muted-foreground">
            Target
          </span>
          <div className="rounded border p-2 space-y-1">
            <div className="flex items-center justify-between text-xs">
              <span className="font-medium">
                {targetNode?.data.label ?? edge.target}
              </span>
              <span className="rounded bg-muted px-1 py-0.5 text-[10px]">
                {targetNode?.data.kind ?? "unknown"}
              </span>
            </div>
            <div className="text-[10px] text-muted-foreground">
              Port: <span className="font-mono">{targetPort}</span>
              {targetPortDef && (
                <span className="ml-1">({targetPortDef.type})</span>
              )}
            </div>
          </div>
        </div>

        <Separator />

        {/* Type compatibility indicator */}
        {sourcePortDef && targetPortDef && (
          <div className="flex items-center gap-2 text-xs">
            <span className="text-muted-foreground">Type match:</span>
            <span className="font-mono">
              {sourcePortDef.type} → {targetPortDef.type}
            </span>
            {sourcePortDef.type === targetPortDef.type ||
            targetPortDef.type === "any" ||
            sourcePortDef.type === "any" ||
            targetPortDef.type === "string" ? (
              <span className="text-green-500">\u2713</span>
            ) : (
              <span className="text-destructive">\u2716</span>
            )}
          </div>
        )}
      </div>
    </div>
  )
}
