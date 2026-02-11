import { useCallback, useEffect, useMemo, useState } from "react"
import { Button } from "@/components/ui/button"
import { Badge } from "@/components/ui/badge"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Separator } from "@/components/ui/separator"
import { api } from "@/api/client"
import { toast } from "sonner"
import type { Trace, TraceSpan } from "@/api/types"

interface TraceViewerProps {
  runId: string
  onBack: () => void
}

/** Color by node type / span context. */
function spanColor(span: TraceSpan): string {
  if (span.status === "error") return "#ef4444"
  if (span.metadata.tool_name) return "#22c55e"
  if (span.metadata.provider) return "#3b82f6"
  if (span.node_type === "gate") return "#f59e0b"
  return "#6b7280"
}

function spanLabel(span: TraceSpan): string {
  if (span.metadata.tool_name) {
    const action = span.metadata.action_name ? `.${span.metadata.action_name}` : ""
    return `tool:${span.metadata.tool_name}${action}`
  }
  return span.node_id
}

export function TraceViewer({ runId, onBack }: TraceViewerProps) {
  const [trace, setTrace] = useState<Trace | null>(null)
  const [loading, setLoading] = useState(true)
  const [selectedSpanId, setSelectedSpanId] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    ;(async () => {
      try {
        const data = await api.get<Trace>(`/api/runs/${encodeURIComponent(runId)}/trace`)
        if (!cancelled) {
          setTrace(data)
          setLoading(false)
        }
      } catch {
        if (!cancelled) {
          toast.error("Failed to load trace.")
          setLoading(false)
        }
      }
    })()
    return () => { cancelled = true }
  }, [runId])

  const selectedSpan = useMemo(
    () => trace?.spans.find((s) => s.span_id === selectedSpanId) ?? null,
    [trace, selectedSpanId],
  )

  // Build tree of spans (top-level + children)
  const spanTree = useMemo(() => {
    if (!trace) return []
    const topLevel = trace.spans.filter((s) => !s.parent_span_id)
    const children = new Map<string, TraceSpan[]>()
    for (const s of trace.spans) {
      if (s.parent_span_id) {
        const list = children.get(s.parent_span_id) ?? []
        list.push(s)
        children.set(s.parent_span_id, list)
      }
    }
    return topLevel.map((s) => ({
      span: s,
      children: children.get(s.span_id) ?? [],
    }))
  }, [trace])

  if (loading) {
    return (
      <div className="flex h-full items-center justify-center text-sm text-muted-foreground">
        Loading trace...
      </div>
    )
  }

  if (!trace) {
    return (
      <div className="flex h-full flex-col items-center justify-center gap-2 text-sm text-muted-foreground">
        <span>No trace data available.</span>
        <Button variant="outline" size="sm" onClick={onBack}>Back</Button>
      </div>
    )
  }

  const totalDuration = trace.duration_ms

  return (
    <div className="flex h-full flex-col">
      {/* Header */}
      <div className="flex items-center justify-between border-b px-4 py-2">
        <div className="flex items-center gap-3">
          <span className="text-sm font-medium">Trace</span>
          <span className="text-xs text-muted-foreground font-mono">{runId}</span>
          <Badge
            variant={trace.status === "completed" ? "default" : "destructive"}
            className="text-[10px]"
          >
            {trace.status}
          </Badge>
        </div>
        <span className="text-xs text-muted-foreground">
          {(totalDuration / 1000).toFixed(1)}s
        </span>
      </div>

      {/* Timeline */}
      <div className="border-b overflow-x-auto">
        <div className="min-w-[600px] px-4 py-3 space-y-1">
          {/* Time axis */}
          <div className="flex items-center justify-between text-[9px] text-muted-foreground mb-2">
            <span>0s</span>
            <span>{(totalDuration / 4000).toFixed(1)}s</span>
            <span>{(totalDuration / 2000).toFixed(1)}s</span>
            <span>{((totalDuration * 3) / 4000).toFixed(1)}s</span>
            <span>{(totalDuration / 1000).toFixed(1)}s</span>
          </div>

          {spanTree.map(({ span, children }) => {
            const traceStart = new Date(trace.started_at).getTime()
            const spanStart = new Date(span.started_at).getTime()
            const leftPct = ((spanStart - traceStart) / totalDuration) * 100
            const widthPct = Math.max((span.duration_ms / totalDuration) * 100, 1)

            return (
              <div key={span.span_id}>
                {/* Parent span bar */}
                <TimelineBar
                  span={span}
                  leftPct={leftPct}
                  widthPct={widthPct}
                  indent={0}
                  selected={selectedSpanId === span.span_id}
                  onClick={() => setSelectedSpanId(span.span_id)}
                />
                {/* Child span bars */}
                {children.map((child) => {
                  const childStart = new Date(child.started_at).getTime()
                  const cLeftPct = ((childStart - traceStart) / totalDuration) * 100
                  const cWidthPct = Math.max((child.duration_ms / totalDuration) * 100, 0.5)
                  return (
                    <TimelineBar
                      key={child.span_id}
                      span={child}
                      leftPct={cLeftPct}
                      widthPct={cWidthPct}
                      indent={1}
                      selected={selectedSpanId === child.span_id}
                      onClick={() => setSelectedSpanId(child.span_id)}
                    />
                  )
                })}
              </div>
            )
          })}
        </div>
      </div>

      {/* Span detail */}
      <ScrollArea className="flex-1">
        {selectedSpan ? (
          <SpanDetail span={selectedSpan} />
        ) : (
          <div className="flex items-center justify-center py-8 text-xs text-muted-foreground">
            Click a span in the timeline to view details
          </div>
        )}
      </ScrollArea>

      {/* Footer */}
      <div className="border-t px-4 py-2">
        <Button variant="outline" size="sm" onClick={onBack}>
          Back
        </Button>
      </div>
    </div>
  )
}

function TimelineBar({
  span,
  leftPct,
  widthPct,
  indent,
  selected,
  onClick,
}: {
  span: TraceSpan
  leftPct: number
  widthPct: number
  indent: number
  selected: boolean
  onClick: () => void
}) {
  const color = spanColor(span)

  return (
    <div
      className={`relative h-5 cursor-pointer hover:opacity-80 ${indent ? "ml-4" : ""}`}
      onClick={onClick}
      title={`${spanLabel(span)} (${(span.duration_ms / 1000).toFixed(2)}s)`}
    >
      <div
        className="absolute top-0.5 h-4 rounded-sm flex items-center px-1.5 overflow-hidden"
        style={{
          left: `${leftPct}%`,
          width: `${widthPct}%`,
          minWidth: "24px",
          background: color + (selected ? "dd" : "33"),
          border: `1px solid ${color}${selected ? "ff" : "66"}`,
        }}
      >
        <span className="text-[9px] truncate font-medium" style={{ color }}>
          {spanLabel(span)} ({(span.duration_ms / 1000).toFixed(1)}s)
        </span>
      </div>
    </div>
  )
}

function SpanDetail({ span }: { span: TraceSpan }) {
  const [expandedSections, setExpandedSections] = useState<Set<string>>(
    new Set(["inputs", "outputs"]),
  )

  const toggle = useCallback((section: string) => {
    setExpandedSections((prev) => {
      const next = new Set(prev)
      if (next.has(section)) next.delete(section)
      else next.add(section)
      return next
    })
  }, [])

  return (
    <div className="p-4 space-y-3">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <div className="text-xs font-medium">{span.node_id}</div>
          <div className="text-[10px] text-muted-foreground">{span.node_type}</div>
        </div>
        <div className="flex items-center gap-2">
          <span className="text-xs">{(span.duration_ms / 1000).toFixed(2)}s</span>
          <Badge
            variant={span.status === "ok" ? "default" : "destructive"}
            className="text-[10px]"
          >
            {span.status}
          </Badge>
        </div>
      </div>

      <Separator />

      {/* Metadata */}
      {(span.metadata.provider || span.metadata.tool_name) && (
        <div className="grid grid-cols-2 gap-2 text-xs">
          {span.metadata.provider && (
            <div>
              <span className="text-muted-foreground">Provider: </span>
              {span.metadata.provider}
            </div>
          )}
          {span.metadata.model && (
            <div>
              <span className="text-muted-foreground">Model: </span>
              {span.metadata.model}
            </div>
          )}
          {span.metadata.tool_name && (
            <div>
              <span className="text-muted-foreground">Tool: </span>
              {span.metadata.tool_name}
              {span.metadata.action_name && `.${span.metadata.action_name}`}
            </div>
          )}
          {span.metadata.tokens_in != null && (
            <div>
              <span className="text-muted-foreground">Tokens: </span>
              {span.metadata.tokens_in.toLocaleString()} in / {(span.metadata.tokens_out ?? 0).toLocaleString()} out
            </div>
          )}
          {span.metadata.cost_usd != null && (
            <div>
              <span className="text-muted-foreground">Cost: </span>
              ${span.metadata.cost_usd.toFixed(4)}
            </div>
          )}
          {span.metadata.retries != null && span.metadata.retries > 0 && (
            <div>
              <span className="text-muted-foreground">Retries: </span>
              {span.metadata.retries}
            </div>
          )}
        </div>
      )}

      {/* Inputs */}
      <CollapsibleJson
        title="Inputs"
        data={span.inputs}
        expanded={expandedSections.has("inputs")}
        onToggle={() => toggle("inputs")}
      />

      {/* Outputs */}
      <CollapsibleJson
        title="Outputs"
        data={span.outputs}
        expanded={expandedSections.has("outputs")}
        onToggle={() => toggle("outputs")}
      />

      {/* Events */}
      {span.events.length > 0 && (
        <CollapsibleJson
          title={`Events (${span.events.length})`}
          data={span.events}
          expanded={expandedSections.has("events")}
          onToggle={() => toggle("events")}
        />
      )}
    </div>
  )
}

function CollapsibleJson({
  title,
  data,
  expanded,
  onToggle,
}: {
  title: string
  data: unknown
  expanded: boolean
  onToggle: () => void
}) {
  const json = useMemo(() => {
    try {
      return JSON.stringify(data, null, 2)
    } catch {
      return String(data)
    }
  }, [data])

  const isEmpty = !data || (typeof data === "object" && Object.keys(data as Record<string, unknown>).length === 0)

  return (
    <div className="rounded border">
      <button
        type="button"
        className="flex w-full items-center justify-between px-2 py-1 text-xs hover:bg-muted/30"
        onClick={onToggle}
      >
        <span className="font-medium">{title}</span>
        <span className="text-muted-foreground">{expanded ? "\u2212" : "+"}</span>
      </button>
      {expanded && (
        <pre className="px-2 py-1.5 text-[10px] font-mono whitespace-pre-wrap border-t bg-muted/10 max-h-64 overflow-y-auto">
          {isEmpty ? "(empty)" : json}
        </pre>
      )}
    </div>
  )
}
