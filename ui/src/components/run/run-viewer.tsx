import { useMemo, useEffect } from 'react'
import { useRunStore } from '@/stores/run'
import { Icon } from '@/components/ui/icon'
import { Badge } from '@/components/ui/badge'
import { cn } from '@/lib/utils'

interface RunViewerProps {
  runId: string
}

// Event type colors for timeline
const eventTypeColors: Record<string, string> = {
  'run.started': 'var(--teal)',
  'node.started': 'var(--blue)',
  'node.completed': 'var(--green)',
  'node.failed': 'var(--red)',
  'run.finished': 'var(--green)',
  'run.failed': 'var(--red)',
  'run.canceled': 'var(--orange)',
  'output': 'var(--purple)',
  'tool.called': 'var(--orange)',
  'tool.result': 'var(--teal)',
}

function formatTimestamp(ts: string): string {
  const date = new Date(ts)
  return date.toLocaleTimeString('en-US', {
    hour12: false,
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  })
}

function formatDuration(ms: number): string {
  if (ms < 1000) return `${ms}ms`
  if (ms < 60000) return `${(ms / 1000).toFixed(1)}s`
  return `${Math.floor(ms / 60000)}m ${Math.floor((ms % 60000) / 1000)}s`
}

export function RunViewer({ runId }: RunViewerProps) {
  const runs = useRunStore((s) => s.runs)
  const activeRun = useRunStore((s) => s.activeRun)
  const events = useRunStore((s) => s.events)
  const selectedEventId = useRunStore((s) => s.selectedEventId)
  const selectEvent = useRunStore((s) => s.selectEvent)
  const subscribeToEvents = useRunStore((s) => s.subscribeToEvents)
  const unsubscribeFromEvents = useRunStore((s) => s.unsubscribeFromEvents)

  // Use activeRun if it matches, otherwise find in runs list
  const run = activeRun?.run_id === runId ? activeRun : runs.find((r) => r.run_id === runId)

  // Subscribe to events when run is active
  useEffect(() => {
    if (run && run.status === 'running') {
      subscribeToEvents(runId)
      return () => unsubscribeFromEvents()
    }
  }, [run, runId, subscribeToEvents, unsubscribeFromEvents])

  // Get selected event details
  const selectedEvent = useMemo(() => {
    if (!selectedEventId) return events[events.length - 1]
    return events.find((e) => e.id === selectedEventId)
  }, [events, selectedEventId])

  // Calculate run duration
  const duration = useMemo(() => {
    if (!run) return null
    if (run.status === 'running') {
      return Date.now() - new Date(run.started_at).getTime()
    }
    if (run.finished_at) {
      return new Date(run.finished_at).getTime() - new Date(run.started_at).getTime()
    }
    return null
  }, [run])

  // Extract data envelope from events
  const dataEnvelope = useMemo(() => {
    const variables: Record<string, unknown> = {}
    const artifacts: { name: string; type: string; size?: number }[] = []

    for (const event of events) {
      if (event.event_type === 'output' && event.payload) {
        Object.assign(variables, event.payload)
      }
      if (event.event_type === 'artifact' && event.payload) {
        artifacts.push({
          name: event.payload.name as string,
          type: event.payload.type as string,
          size: event.payload.size as number | undefined,
        })
      }
    }

    return { variables, artifacts }
  }, [events])

  if (!run) {
    return (
      <div className="flex items-center justify-center h-full text-sm text-muted-foreground">
        Run not found
      </div>
    )
  }

  return (
    <div className="flex h-full overflow-hidden">
      {/* Left: Event Timeline */}
      <div className="w-64 border-r border-border bg-surface-0 flex flex-col">
        <div className="p-3 border-b border-border">
          <div className="flex items-center justify-between">
            <span className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">
              Timeline
            </span>
            <Badge
              variant={
                run.status === 'success'
                  ? 'success'
                  : run.status === 'running'
                    ? 'running'
                    : run.status === 'failed'
                      ? 'failed'
                      : 'default'
              }
            >
              {run.status}
            </Badge>
          </div>
          {duration !== null && (
            <div className="text-[11px] text-muted-foreground mt-1">
              Duration: {formatDuration(duration)}
            </div>
          )}
        </div>

        <div className="flex-1 overflow-auto">
          {events.length === 0 ? (
            <div className="flex items-center justify-center h-32 text-sm text-muted-foreground">
              {run.status === 'running' ? 'Waiting for events...' : 'No events recorded'}
            </div>
          ) : (
            <div className="p-2 space-y-1">
              {events.map((event) => (
                <button
                  key={event.id}
                  onClick={() => selectEvent(event.id)}
                  className={cn(
                    'w-full text-left p-2 rounded-lg transition-colors',
                    'hover:bg-surface-1',
                    selectedEvent?.id === event.id && 'bg-accent-soft border border-primary'
                  )}
                >
                  <div className="flex items-center gap-2">
                    <span
                      className="w-2 h-2 rounded-full flex-shrink-0"
                      style={{ backgroundColor: eventTypeColors[event.event_type] || 'var(--muted-foreground)' }}
                    />
                    <span className="text-xs font-medium text-foreground truncate">
                      {event.event_type.replace(/\./g, ' ')}
                    </span>
                  </div>
                  <div className="text-[10px] text-muted-foreground mt-0.5 pl-4">
                    {formatTimestamp(event.timestamp)}
                    {event.node_id && ` · ${event.node_id}`}
                  </div>
                </button>
              ))}
            </div>
          )}
        </div>
      </div>

      {/* Center: Active Node / Event Details */}
      <div className="flex-1 flex flex-col bg-canvas overflow-hidden">
        <div className="p-4 border-b border-border bg-surface-0">
          <div className="flex items-center justify-between">
            <div>
              <h3 className="text-sm font-bold text-foreground">
                {selectedEvent ? selectedEvent.event_type.replace(/\./g, ' ') : 'No event selected'}
              </h3>
              {selectedEvent?.node_id && (
                <p className="text-xs text-muted-foreground mt-0.5">
                  Node: {selectedEvent.node_id}
                </p>
              )}
            </div>
            {selectedEvent && (
              <span className="text-xs text-muted-foreground">
                {formatTimestamp(selectedEvent.timestamp)}
              </span>
            )}
          </div>
        </div>

        <div className="flex-1 overflow-auto p-4">
          {selectedEvent ? (
            <div className="space-y-4">
              {/* Event payload */}
              {selectedEvent.payload && Object.keys(selectedEvent.payload).length > 0 && (
                <div>
                  <div className="text-xs font-semibold text-muted-foreground mb-2">Payload</div>
                  <pre className="p-3 rounded-lg bg-surface-1 border border-border text-xs text-foreground font-mono overflow-auto max-h-96">
                    {JSON.stringify(selectedEvent.payload, null, 2)}
                  </pre>
                </div>
              )}

              {/* Streaming output preview */}
              {selectedEvent.event_type === 'output' && selectedEvent.payload && (
                <div>
                  <div className="text-xs font-semibold text-muted-foreground mb-2">Output Preview</div>
                  <div className="p-3 rounded-lg bg-surface-1 border border-border">
                    <pre className="text-sm text-foreground whitespace-pre-wrap">
                      {typeof selectedEvent.payload === 'string'
                        ? selectedEvent.payload
                        : JSON.stringify(selectedEvent.payload, null, 2)}
                    </pre>
                  </div>
                </div>
              )}
            </div>
          ) : (
            <div className="flex items-center justify-center h-full text-sm text-muted-foreground">
              Select an event from the timeline
            </div>
          )}
        </div>
      </div>

      {/* Right: Data Envelope */}
      <div className="w-72 border-l border-border bg-surface-0 flex flex-col">
        <div className="p-3 border-b border-border">
          <span className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">
            Data Envelope
          </span>
        </div>

        <div className="flex-1 overflow-auto p-3 space-y-4">
          {/* Run Info */}
          <div>
            <div className="text-[11px] font-semibold text-muted-foreground uppercase mb-2">
              Run Info
            </div>
            <div className="space-y-2">
              <div className="flex justify-between text-xs">
                <span className="text-muted-foreground">Run ID</span>
                <span className="text-foreground font-mono">{run.run_id.slice(0, 8)}...</span>
              </div>
              <div className="flex justify-between text-xs">
                <span className="text-muted-foreground">Workflow</span>
                <span className="text-foreground">{run.workflow_id}</span>
              </div>
              <div className="flex justify-between text-xs">
                <span className="text-muted-foreground">Started</span>
                <span className="text-foreground">{formatTimestamp(run.started_at)}</span>
              </div>
              {run.finished_at && (
                <div className="flex justify-between text-xs">
                  <span className="text-muted-foreground">Finished</span>
                  <span className="text-foreground">{formatTimestamp(run.finished_at)}</span>
                </div>
              )}
              {run.metrics && (
                <>
                  <div className="flex justify-between text-xs">
                    <span className="text-muted-foreground">Tokens</span>
                    <span className="text-foreground">{run.metrics.total_tokens.toLocaleString()}</span>
                  </div>
                  <div className="flex justify-between text-xs">
                    <span className="text-muted-foreground">Tool Calls</span>
                    <span className="text-foreground">{run.metrics.tool_calls}</span>
                  </div>
                </>
              )}
            </div>
          </div>

          {/* Variables */}
          <div>
            <div className="text-[11px] font-semibold text-muted-foreground uppercase mb-2 flex items-center gap-1">
              <Icon name="code" size={12} />
              Variables
            </div>
            {Object.keys(dataEnvelope.variables).length > 0 ? (
              <div className="space-y-1.5">
                {Object.entries(dataEnvelope.variables).map(([key, value]) => (
                  <div
                    key={key}
                    className="p-2 rounded-lg bg-surface-1 border border-border"
                  >
                    <div className="text-xs font-medium text-foreground">{key}</div>
                    <div className="text-[11px] text-muted-foreground truncate">
                      {typeof value === 'string' ? value : JSON.stringify(value)}
                    </div>
                  </div>
                ))}
              </div>
            ) : (
              <div className="text-xs text-muted-foreground italic">No variables yet</div>
            )}
          </div>

          {/* Artifacts */}
          <div>
            <div className="text-[11px] font-semibold text-muted-foreground uppercase mb-2 flex items-center gap-1">
              <Icon name="file" size={12} />
              Artifacts
            </div>
            {dataEnvelope.artifacts.length > 0 ? (
              <div className="space-y-1.5">
                {dataEnvelope.artifacts.map((artifact, idx) => (
                  <div
                    key={idx}
                    className="p-2 rounded-lg bg-surface-1 border border-border flex items-center gap-2"
                  >
                    <Icon name="file" size={14} className="text-muted-foreground" />
                    <div className="flex-1 min-w-0">
                      <div className="text-xs font-medium text-foreground truncate">
                        {artifact.name}
                      </div>
                      <div className="text-[10px] text-muted-foreground">
                        {artifact.type}
                        {artifact.size && ` · ${(artifact.size / 1024).toFixed(1)}KB`}
                      </div>
                    </div>
                  </div>
                ))}
              </div>
            ) : (
              <div className="text-xs text-muted-foreground italic">No artifacts yet</div>
            )}
          </div>

          {/* Input */}
          {run.input && Object.keys(run.input).length > 0 && (
            <div>
              <div className="text-[11px] font-semibold text-muted-foreground uppercase mb-2">
                Input
              </div>
              <pre className="p-2 rounded-lg bg-surface-1 border border-border text-[11px] text-foreground font-mono overflow-auto max-h-32">
                {JSON.stringify(run.input, null, 2)}
              </pre>
            </div>
          )}

          {/* Output */}
          {run.output && Object.keys(run.output).length > 0 && (
            <div>
              <div className="text-[11px] font-semibold text-muted-foreground uppercase mb-2">
                Final Output
              </div>
              <pre className="p-2 rounded-lg bg-surface-1 border border-border text-[11px] text-foreground font-mono overflow-auto max-h-32">
                {JSON.stringify(run.output, null, 2)}
              </pre>
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
