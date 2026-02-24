import { useEffect, useState } from 'react'
import { createFileRoute, useNavigate, useSearch } from '@tanstack/react-router'
import { useRunStore } from '@/stores/run'
import { useWorkflowStore } from '@/stores/workflow'
import { RunViewer } from '@/components/run'
import { Icon } from '@/components/ui/icon'
import { Badge } from '@/components/ui/badge'
import { cn } from '@/lib/utils'
import type { Run } from '@/lib/api-types'

interface RunsSearch {
  viewRun?: string
  workflowId?: string
  status?: string
}

export const Route = createFileRoute('/_app/runs/')({
  component: RunsPage,
  validateSearch: (search: Record<string, unknown>): RunsSearch => ({
    viewRun: search.viewRun as string | undefined,
    workflowId: search.workflowId as string | undefined,
    status: search.status as string | undefined,
  }),
})

function formatTimestamp(ts: string): string {
  const date = new Date(ts)
  return date.toLocaleDateString('en-US', {
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  })
}

function formatDuration(run: Run): string {
  const finishedAt = run.finished_at || run.completed_at
  if (!finishedAt) {
    if (typeof run.duration_ms === 'number') {
      const ms = run.duration_ms
      if (ms < 1000) return `${ms}ms`
      if (ms < 60000) return `${(ms / 1000).toFixed(1)}s`
      return `${Math.floor(ms / 60000)}m ${Math.floor((ms % 60000) / 1000)}s`
    }
    const ms = Date.now() - new Date(run.started_at).getTime()
    return `${(ms / 1000).toFixed(0)}s`
  }
  const ms = new Date(finishedAt).getTime() - new Date(run.started_at).getTime()
  if (ms < 1000) return `${ms}ms`
  if (ms < 60000) return `${(ms / 1000).toFixed(1)}s`
  return `${Math.floor(ms / 60000)}m ${Math.floor((ms % 60000) / 1000)}s`
}

function RunsPage() {
  const search = useSearch({ from: '/_app/runs/' })
  const navigate = useNavigate()
  const rawRuns = useRunStore((s) => s.runs)
  const loading = useRunStore((s) => s.loading)
  const fetchRuns = useRunStore((s) => s.fetchRuns)
  const getRun = useRunStore((s) => s.getRun)
  const exportRun = useRunStore((s) => s.exportRun)
  const setActiveRun = useRunStore((s) => s.setActiveRun)
  const rawWorkflows = useWorkflowStore((s) => s.workflows)
  const fetchWorkflows = useWorkflowStore((s) => s.fetchWorkflows)

  // Defensive: ensure arrays are always arrays
  const runs = Array.isArray(rawRuns) ? rawRuns : []
  const workflows = Array.isArray(rawWorkflows) ? rawWorkflows : []
  const [viewingRunLookupLoading, setViewingRunLookupLoading] = useState(false)
  const [viewingRunLookupError, setViewingRunLookupError] = useState<string | null>(null)
  const [exportingRunId, setExportingRunId] = useState<string | null>(null)
  const [exportError, setExportError] = useState<string | null>(null)

  // Fetch runs and workflows on mount
  useEffect(() => {
    fetchRuns({
      workflow_id: search.workflowId,
      status: search.status,
    })
    fetchWorkflows()
  }, [fetchRuns, fetchWorkflows, search.workflowId, search.status])

  const filteredRuns = runs.filter((run) => {
    if (search.workflowId && run.workflow_id !== search.workflowId) {
      return false
    }
    if (search.status && run.status !== search.status) {
      return false
    }
    return true
  })

  // Get workflow name for a run
  const getWorkflowName = (workflowId: string) => {
    const workflow = workflows.find((w) => w.id === workflowId)
    return workflow?.name || workflowId
  }

  // Filter runs if viewing a specific run
  const viewingRun = search.viewRun
    ? runs.find((r) => r.run_id === search.viewRun)
    : null

  // Set active run when viewing
  useEffect(() => {
    if (viewingRun) {
      setActiveRun(viewingRun)
    }
  }, [viewingRun, setActiveRun])

  // Resolve deep-linked runs that are not in the current list.
  useEffect(() => {
    let cancelled = false
    const runID = search.viewRun

    if (!runID) {
      setViewingRunLookupLoading(false)
      setViewingRunLookupError(null)
      return () => {
        cancelled = true
      }
    }
    if (viewingRun) {
      setViewingRunLookupLoading(false)
      setViewingRunLookupError(null)
      return () => {
        cancelled = true
      }
    }

    setViewingRunLookupLoading(true)
    setViewingRunLookupError(null)

    void getRun(runID)
      .catch((err) => {
        if (!cancelled) {
          setViewingRunLookupError((err as Error).message || 'Run not found')
        }
      })
      .finally(() => {
        if (!cancelled) {
          setViewingRunLookupLoading(false)
        }
      })

    return () => {
      cancelled = true
    }
  }, [getRun, search.viewRun, viewingRun])

  const handleExportRun = async (runId: string) => {
    setExportingRunId(runId)
    setExportError(null)
    try {
      const exported = await exportRun(runId)
      const blob = new Blob([JSON.stringify(exported, null, 2)], {
        type: 'application/json',
      })
      const url = URL.createObjectURL(blob)
      const link = document.createElement('a')
      link.href = url
      link.download = `run-${runId}.json`
      document.body.appendChild(link)
      link.click()
      document.body.removeChild(link)
      URL.revokeObjectURL(url)
    } catch (err) {
      setExportError((err as Error).message || 'Failed to export run')
    } finally {
      setExportingRunId(null)
    }
  }

  if (search.viewRun && !viewingRun) {
    return (
      <div className="p-7">
        <div className="max-w-xl rounded-xl border border-border bg-surface-0 p-5">
          <h2 className="text-lg font-semibold text-foreground">Run not available</h2>
          <p className="mt-2 text-sm text-muted-foreground">
            {viewingRunLookupLoading
              ? 'Loading run details from the server...'
              : viewingRunLookupError || 'Run not found on the server.'}
          </p>
          <button
            onClick={() => {
              navigate({ to: '/runs', search: {} })
            }}
            className={cn(
              'mt-4 inline-flex items-center gap-1.5 rounded-lg font-semibold transition-all',
              'text-[13px] px-3.5 py-2',
              'bg-primary text-white hover:bg-primary/90'
            )}
          >
            <Icon name="arrow-left" size={15} />
            Back to Runs
          </button>
        </div>
      </div>
    )
  }

  // If viewing a specific run, show the RunViewer
  if (viewingRun) {
    return (
      <div className="h-full flex flex-col">
        <div className="flex items-center gap-3 p-4 border-b border-border bg-surface-0">
          <button
            type="button"
            onClick={() => navigate({ to: '/runs', search: {} })}
            className="p-1.5 rounded-lg hover:bg-surface-1 transition-colors text-muted-foreground hover:text-foreground"
          >
            <Icon name="arrow-left" size={16} />
          </button>
          <div>
            <h1 className="text-sm font-bold text-foreground">
              Run {viewingRun.run_id.slice(0, 8)}
            </h1>
            <p className="text-xs text-muted-foreground">
              {getWorkflowName(viewingRun.workflow_id)}
            </p>
          </div>
          <div className="ml-auto flex items-center gap-2">
            <button
              type="button"
              onClick={() => handleExportRun(viewingRun.run_id)}
              disabled={exportingRunId === viewingRun.run_id}
              className={cn(
                'inline-flex items-center gap-1.5 rounded-lg font-semibold transition-all',
                'text-[12px] px-2.5 py-1.5',
                'bg-surface-2 text-foreground border border-border hover:bg-surface-active',
                exportingRunId === viewingRun.run_id && 'opacity-50 cursor-not-allowed'
              )}
              title="Export run JSON"
            >
              <Icon name="export" size={14} />
              {exportingRunId === viewingRun.run_id ? 'Exporting...' : 'Export'}
            </button>
            <Badge
              variant={
                viewingRun.status === 'success' || viewingRun.status === 'completed'
                  ? 'success'
                  : viewingRun.status === 'running'
                    ? 'running'
                    : viewingRun.status === 'failed'
                      ? 'failed'
                      : 'default'
              }
            >
              {viewingRun.status}
            </Badge>
          </div>
        </div>
        {exportError && (
          <div className="px-4 py-2 border-b border-border bg-red-soft text-red text-xs">
            {exportError}
          </div>
        )}
        <div className="flex-1 overflow-hidden">
          <RunViewer runId={viewingRun.run_id} />
        </div>
      </div>
    )
  }

  // Show runs list
  return (
    <div className="p-7">
      <div className="flex items-center justify-between mb-6">
        <div>
          <h1 className="text-xl font-bold text-foreground">Run History</h1>
          <p className="text-sm text-muted-foreground mt-1">
            View and analyze workflow execution history
          </p>
        </div>
        <div className="flex items-center gap-2">
          <select
            className={cn(
              'px-3 py-2 rounded-lg border border-border bg-surface-1',
              'text-sm text-foreground',
              'focus:outline-none focus:ring-1 focus:ring-primary'
            )}
            value={search.status || ''}
            onChange={(e) => {
              const nextStatus = e.target.value || undefined
              navigate({
                to: '/runs',
                search: {
                  ...search,
                  status: nextStatus,
                },
              })
            }}
          >
            <option value="">All statuses</option>
            <option value="running">Running</option>
            <option value="completed">Completed</option>
            <option value="failed">Failed</option>
            <option value="canceled">Canceled</option>
          </select>
        </div>
      </div>

      {loading ? (
        <div className="flex items-center justify-center h-64">
          <div className="animate-pulse text-sm text-muted-foreground">Loading runs...</div>
        </div>
      ) : filteredRuns.length === 0 ? (
        <div className="flex flex-col items-center justify-center h-64 text-center">
          <div className="w-14 h-14 mb-4 rounded-full bg-surface-2 flex items-center justify-center">
            <Icon name="play" size={24} className="text-muted-foreground" />
          </div>
          <h3 className="text-lg font-semibold text-foreground mb-1">No runs yet</h3>
          <p className="text-sm text-muted-foreground max-w-sm">
            Run a workflow to see execution history here. Click the Run button in the designer to start.
          </p>
        </div>
      ) : (
        <div className="bg-surface-0 rounded-xl border border-border overflow-hidden">
          <table className="w-full">
            <thead>
              <tr className="border-b border-border bg-surface-1">
                <th className="text-left px-4 py-3 text-xs font-semibold text-muted-foreground uppercase tracking-wide">
                  Run ID
                </th>
                <th className="text-left px-4 py-3 text-xs font-semibold text-muted-foreground uppercase tracking-wide">
                  Workflow
                </th>
                <th className="text-left px-4 py-3 text-xs font-semibold text-muted-foreground uppercase tracking-wide">
                  Status
                </th>
                <th className="text-left px-4 py-3 text-xs font-semibold text-muted-foreground uppercase tracking-wide">
                  Started
                </th>
                <th className="text-left px-4 py-3 text-xs font-semibold text-muted-foreground uppercase tracking-wide">
                  Duration
                </th>
                <th className="text-left px-4 py-3 text-xs font-semibold text-muted-foreground uppercase tracking-wide">
                  Tokens
                </th>
                <th className="w-10"></th>
              </tr>
            </thead>
            <tbody>
              {filteredRuns.map((run) => (
                <tr
                  key={run.run_id}
                  className="border-b border-border last:border-b-0 hover:bg-surface-1 transition-colors cursor-pointer"
                  onClick={() => {
                    navigate({
                      to: '/runs',
                      search: {
                        ...search,
                        viewRun: run.run_id,
                      },
                    })
                  }}
                >
                  <td className="px-4 py-3">
                    <span className="font-mono text-sm text-foreground">
                      {run.run_id.slice(0, 8)}
                    </span>
                  </td>
                  <td className="px-4 py-3">
                    <span className="text-sm text-foreground">
                      {getWorkflowName(run.workflow_id)}
                    </span>
                  </td>
                  <td className="px-4 py-3">
                    <Badge
                      variant={
                        run.status === 'success' || run.status === 'completed'
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
                  </td>
                  <td className="px-4 py-3">
                    <span className="text-sm text-muted-foreground">
                      {formatTimestamp(run.started_at)}
                    </span>
                  </td>
                  <td className="px-4 py-3">
                    <span className="text-sm text-muted-foreground">
                      {formatDuration(run)}
                    </span>
                  </td>
                  <td className="px-4 py-3">
                    <span className="text-sm text-muted-foreground">
                      {run.metrics?.total_tokens?.toLocaleString() || '-'}
                    </span>
                  </td>
                  <td className="px-4 py-3">
                    <Icon
                      name="chevron-right"
                      size={16}
                      className="text-muted-foreground"
                    />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}
