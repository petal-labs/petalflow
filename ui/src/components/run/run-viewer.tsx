import { useEffect, useMemo, useState } from 'react'
import { useRunStore } from '@/stores/run'
import { Icon } from '@/components/ui/icon'
import { OrbitIcon } from '@/components/ui/orbit-icon'
import { Markdown } from '@/components/ui/markdown'
import { Badge } from '@/components/ui/badge'
import { cn } from '@/lib/utils'

interface RunViewerProps {
  runId: string
}

type ActivityStatus = 'running' | 'completed' | 'failed'

interface ActivityItem {
  nodeId: string
  taskId: string
  agentId: string
  displayName: string
  status: ActivityStatus
  startedAt: string
  finishedAt?: string
  outputFinal?: unknown
  outputTimestamp?: string
}

interface ArtifactItem {
  name: string
  type: string
  size?: number
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

function parseNodeIdentity(nodeId: string): { taskId: string; agentId: string; displayName: string } {
  const [taskPart, agentPart] = nodeId.split('__')
  const taskId = taskPart || nodeId
  const agentId = agentPart || ''
  const displayName = agentId ? `${taskId} (${agentId})` : taskId
  return { taskId, agentId, displayName }
}

function asObject(value: unknown): Record<string, unknown> {
  if (value && typeof value === 'object' && !Array.isArray(value)) {
    return value as Record<string, unknown>
  }
  return {}
}

function extractFinalOutputValue(payload: Record<string, unknown>): unknown {
  if (typeof payload.text === 'string') return payload.text
  if (typeof payload.output === 'string') return payload.output
  if (payload.output !== undefined) return payload.output
  return payload
}

function toMarkdownContent(value: unknown): string {
  if (typeof value === 'string') {
    const trimmed = value.trim()
    if (
      (trimmed.startsWith('{') && trimmed.endsWith('}')) ||
      (trimmed.startsWith('[') && trimmed.endsWith(']'))
    ) {
      try {
        const parsed = JSON.parse(trimmed)
        return `\`\`\`json\n${JSON.stringify(parsed, null, 2)}\n\`\`\``
      } catch {
        // Fall through to plain text rendering.
      }
    }
    return value
  }

  if (value && typeof value === 'object') {
    return `\`\`\`json\n${JSON.stringify(value, null, 2)}\n\`\`\``
  }

  return String(value ?? '')
}

function statusBadgeVariant(status: string) {
  if (status === 'success' || status === 'completed') return 'success'
  if (status === 'running') return 'running'
  if (status === 'failed') return 'failed'
  return 'default'
}

export function RunViewer({ runId }: RunViewerProps) {
  const runs = useRunStore((s) => s.runs)
  const activeRun = useRunStore((s) => s.activeRun)
  const events = useRunStore((s) => s.events)
  const subscribeToEvents = useRunStore((s) => s.subscribeToEvents)
  const unsubscribeFromEvents = useRunStore((s) => s.unsubscribeFromEvents)
  const [tick, setTick] = useState(() => Date.now())

  const run = activeRun?.run_id === runId ? activeRun : runs.find((candidate) => candidate.run_id === runId)

  useEffect(() => {
    if (!run) return undefined
    subscribeToEvents(runId)
    return () => unsubscribeFromEvents()
  }, [run, runId, subscribeToEvents, unsubscribeFromEvents])

  const activity = useMemo(() => {
    const indexByNode = new Map<string, number>()
    const items: ActivityItem[] = []

    for (const event of events) {
      if (!event.node_id) continue
      const nodeId = event.node_id
      let idx = indexByNode.get(nodeId)
      if (idx === undefined) {
        const identity = parseNodeIdentity(nodeId)
        idx = items.length
        indexByNode.set(nodeId, idx)
        items.push({
          nodeId,
          taskId: identity.taskId,
          agentId: identity.agentId,
          displayName: identity.displayName,
          status: 'running',
          startedAt: event.timestamp,
        })
      }

      const current = items[idx]
      if (event.event_type === 'node.started') {
        current.startedAt = event.timestamp
        current.status = 'running'
      } else if (event.event_type === 'node.finished') {
        current.finishedAt = event.timestamp
        if (current.status !== 'failed') {
          current.status = 'completed'
        }
      } else if (event.event_type === 'node.failed') {
        current.finishedAt = event.timestamp
        current.status = 'failed'
      } else if (event.event_type === 'node.output.final') {
        current.outputFinal = extractFinalOutputValue(asObject(event.payload))
        current.outputTimestamp = event.timestamp
      }
    }

    if (run && run.status !== 'running') {
      const terminalAt = run.finished_at || run.completed_at
      for (const item of items) {
        if (item.status === 'running') {
          item.status = run.status === 'failed' ? 'failed' : 'completed'
          item.finishedAt = terminalAt || item.finishedAt
        }
      }
    }

    return items
  }, [events, run])

  const currentActivity = useMemo(() => {
    const running = activity.filter((item) => item.status === 'running')
    return running[running.length - 1]
  }, [activity])

  useEffect(() => {
    if (!run) return undefined
    if (run.status !== 'running' && !currentActivity) return undefined
    const intervalId = window.setInterval(() => setTick(Date.now()), 1000)
    return () => window.clearInterval(intervalId)
  }, [run, currentActivity])

  const runElapsedMs = useMemo(() => {
    if (!run) return 0
    const startMs = new Date(run.started_at).getTime()
    const endRaw = run.finished_at || run.completed_at
    const endMs = endRaw ? new Date(endRaw).getTime() : tick
    return Math.max(0, endMs - startMs)
  }, [run, tick])

  const currentActivityElapsedMs = useMemo(() => {
    if (!currentActivity) return 0
    const startMs = new Date(currentActivity.startedAt).getTime()
    const endMs =
      currentActivity.status === 'running'
        ? tick
        : new Date(currentActivity.finishedAt || currentActivity.startedAt).getTime()
    return Math.max(0, endMs - startMs)
  }, [currentActivity, tick])

  const finalOutputs = useMemo(
    () =>
      activity
        .filter((item) => item.outputFinal !== undefined)
        .map((item) => ({
          nodeId: item.nodeId,
          title: item.displayName,
          timestamp: item.outputTimestamp || item.finishedAt || item.startedAt,
          value: item.outputFinal as unknown,
        })),
    [activity]
  )

  const artifacts = useMemo(() => {
    const list: ArtifactItem[] = []

    for (const event of events) {
      if (event.event_type !== 'artifact') continue
      const payload = asObject(event.payload)
      list.push({
        name: typeof payload.name === 'string' && payload.name !== '' ? payload.name : 'artifact',
        type: typeof payload.type === 'string' ? payload.type : 'artifact',
        size: typeof payload.size === 'number' ? payload.size : undefined,
      })
    }

    const runOutput = asObject(run?.output)
    const runArtifacts = Array.isArray(runOutput.artifacts) ? runOutput.artifacts : []
    for (const candidate of runArtifacts) {
      const payload = asObject(candidate)
      list.push({
        name: typeof payload.name === 'string' && payload.name !== '' ? payload.name : 'artifact',
        type: typeof payload.type === 'string' ? payload.type : 'artifact',
        size: typeof payload.size === 'number' ? payload.size : undefined,
      })
    }

    return list
  }, [events, run?.output])

  if (!run) {
    return (
      <div className="flex items-center justify-center h-full text-sm text-muted-foreground">
        Run not found
      </div>
    )
  }

  return (
    <div className="flex h-full overflow-hidden">
      <div className="flex-1 flex flex-col bg-canvas overflow-hidden">
        <div className="p-4 border-b border-border bg-surface-0 space-y-2">
          <div className="flex items-center justify-between gap-3">
            <div>
              <h3 className="text-sm font-bold text-foreground">Run Activity</h3>
              {currentActivity ? (
                <p className="text-xs text-muted-foreground mt-0.5">
                  Running now: <span className="text-foreground">{currentActivity.displayName}</span> ·{' '}
                  {formatDuration(currentActivityElapsedMs)}
                </p>
              ) : (
                <p className="text-xs text-muted-foreground mt-0.5">
                  {run.status === 'running' ? 'Waiting for task events...' : 'No active task'}
                </p>
              )}
            </div>
            <div className="flex items-center gap-2">
              <div className="inline-flex items-center gap-1.5 text-[11px] font-semibold text-muted-foreground uppercase tracking-wide">
                <OrbitIcon
                  size={14}
                  className={cn(
                    run.status === 'running' && 'animate-orbit-loop-2ms text-blue',
                    run.status === 'failed' && 'text-red',
                    (run.status === 'completed' || run.status === 'success') && 'text-green',
                    run.status === 'canceled' && 'text-amber'
                  )}
                />
                <span>Current Status</span>
              </div>
              <Badge variant={statusBadgeVariant(run.status)}>{run.status}</Badge>
            </div>
          </div>
          <div className="text-[11px] text-muted-foreground">
            Elapsed: {formatDuration(runElapsedMs)}
          </div>
        </div>

        <div className="flex-1 overflow-auto p-4 space-y-4">
          <section className="rounded-xl border border-border bg-surface-0">
            <div className="px-3 py-2 border-b border-border text-xs font-semibold text-muted-foreground uppercase tracking-wide">
              Task Activity
            </div>
            <div className="p-3 space-y-2">
              {activity.length === 0 ? (
                <div className="text-sm text-muted-foreground">No node activity recorded yet.</div>
              ) : (
                activity.map((item) => {
                  const statusIcon =
                    item.status === 'failed' ? (
                      <Icon name="x" size={14} className="text-red" />
                    ) : item.status === 'completed' ? (
                      <Icon name="check" size={14} className="text-green" />
                    ) : (
                      <Icon name="clock" size={14} className="text-blue animate-pulse" />
                    )
                  const durationMs = Math.max(
                    0,
                    (item.status === 'running' ? tick : new Date(item.finishedAt || item.startedAt).getTime()) -
                      new Date(item.startedAt).getTime()
                  )

                  return (
                    <div
                      key={item.nodeId}
                      className={cn(
                        'rounded-lg border px-3 py-2 flex items-center justify-between gap-3',
                        item.status === 'running' ? 'border-primary bg-accent-soft' : 'border-border bg-surface-1'
                      )}
                    >
                      <div className="flex items-center gap-2 min-w-0">
                        {statusIcon}
                        <div className="min-w-0">
                          <div className="text-sm text-foreground truncate">{item.displayName}</div>
                          <div className="text-[11px] text-muted-foreground">
                            {item.status}
                            {' · '}
                            {formatTimestamp(item.startedAt)}
                            {item.finishedAt && ` → ${formatTimestamp(item.finishedAt)}`}
                          </div>
                        </div>
                      </div>
                      <div className="text-xs text-muted-foreground">{formatDuration(durationMs)}</div>
                    </div>
                  )
                })
              )}
            </div>
          </section>

          <section className="rounded-xl border border-border bg-surface-0">
            <div className="px-3 py-2 border-b border-border text-xs font-semibold text-muted-foreground uppercase tracking-wide">
              Node Output Final
            </div>
            <div className="p-3 space-y-3">
              {finalOutputs.length === 0 ? (
                <div className="text-sm text-muted-foreground">No final node output captured yet.</div>
              ) : (
                finalOutputs.map((output) => (
                  <div key={`${output.nodeId}-${output.timestamp}`} className="rounded-lg border border-border bg-surface-1">
                    <div className="px-3 py-2 border-b border-border flex items-center justify-between gap-3">
                      <div className="text-xs font-semibold text-foreground truncate">{output.title}</div>
                      <div className="text-[11px] text-muted-foreground shrink-0">
                        {formatTimestamp(output.timestamp)}
                      </div>
                    </div>
                    <div className="px-3 py-3">
                      <Markdown content={toMarkdownContent(output.value)} />
                    </div>
                  </div>
                ))
              )}
            </div>
          </section>
        </div>
      </div>

      <aside className="w-80 border-l border-border bg-surface-0 flex flex-col">
        <div className="p-3 border-b border-border">
          <span className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">Run Details</span>
        </div>

        <div className="flex-1 overflow-auto p-3 space-y-4">
          <section className="space-y-2">
            <div className="text-[11px] font-semibold text-muted-foreground uppercase">Timing</div>
            <div className="rounded-lg border border-border bg-surface-1 p-2.5 space-y-2">
              <div className="flex justify-between text-xs gap-3">
                <span className="text-muted-foreground">Started</span>
                <span className="text-foreground">{formatTimestamp(run.started_at)}</span>
              </div>
              {(run.finished_at || run.completed_at) && (
                <div className="flex justify-between text-xs gap-3">
                  <span className="text-muted-foreground">Finished</span>
                  <span className="text-foreground">
                    {formatTimestamp(run.finished_at || run.completed_at || run.started_at)}
                  </span>
                </div>
              )}
              <div className="flex justify-between text-xs gap-3">
                <span className="text-muted-foreground">Elapsed</span>
                <span className="text-foreground">{formatDuration(runElapsedMs)}</span>
              </div>
            </div>
          </section>

          <section>
            <div className="text-[11px] font-semibold text-muted-foreground uppercase mb-2 flex items-center gap-1">
              <Icon name="file" size={12} />
              Artifacts
            </div>
            {artifacts.length > 0 ? (
              <div className="space-y-1.5">
                {artifacts.map((artifact, idx) => (
                  <div key={`${artifact.name}-${idx}`} className="p-2 rounded-lg bg-surface-1 border border-border flex items-center gap-2">
                    <Icon name="file" size={14} className="text-muted-foreground" />
                    <div className="flex-1 min-w-0">
                      <div className="text-xs font-medium text-foreground truncate">{artifact.name}</div>
                      <div className="text-[10px] text-muted-foreground">
                        {artifact.type}
                        {typeof artifact.size === 'number' && ` · ${(artifact.size / 1024).toFixed(1)}KB`}
                      </div>
                    </div>
                  </div>
                ))}
              </div>
            ) : (
              <div className="text-xs text-muted-foreground italic">No artifacts yet</div>
            )}
          </section>
        </div>
      </aside>
    </div>
  )
}
