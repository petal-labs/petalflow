import { create } from 'zustand'
import type { Run, RunEvent, RunExport } from '@/lib/api-types'
import { runsApi, workflowsApi, type RunStartOptions } from '@/lib/api-client'

export interface RunState {
  runs: Run[]
  activeRun: Run | null
  events: RunEvent[]
  selectedEventId: number | null
  loading: boolean
  error: string | null
  eventSubscription: (() => void) | null
}

export interface RunActions {
  fetchRuns: (params?: { workflow_id?: string; status?: string }) => Promise<void>
  getRun: (runId: string) => Promise<Run>
  setActiveRun: (run: Run | null) => void
  startRun: (workflowId: string, input: Record<string, unknown>, options?: RunStartOptions) => Promise<Run>
  subscribeToEvents: (runId: string) => void
  unsubscribeFromEvents: () => void
  addEvent: (event: RunEvent) => void
  selectEvent: (eventId: number | null) => void
  clearEvents: () => void
  exportRun: (runId: string) => Promise<RunExport>
  clearError: () => void
}

const initialState: RunState = {
  runs: [],
  activeRun: null,
  events: [],
  selectedEventId: null,
  loading: false,
  error: null,
  eventSubscription: null,
}

function hasRecordValues(value: Record<string, unknown> | undefined): boolean {
  return Boolean(value) && Object.keys(value as Record<string, unknown>).length > 0
}

function mergeRunWithExisting(run: Run, existing?: Run): Run {
  if (!existing) {
    return run
  }

  return {
    ...run,
    input: hasRecordValues(run.input) ? run.input : existing.input,
    output: hasRecordValues(run.output) ? run.output : existing.output,
    metrics: run.metrics || existing.metrics,
    trace_id: run.trace_id || existing.trace_id,
    finished_at: run.finished_at || existing.finished_at,
    completed_at: run.completed_at || existing.completed_at,
    duration_ms: typeof run.duration_ms === 'number' ? run.duration_ms : existing.duration_ms,
  }
}

export const useRunStore = create<RunState & RunActions>()((set, get) => ({
  ...initialState,

  fetchRuns: async (params) => {
    set({ loading: true, error: null })
    try {
      const response = await runsApi.list(params)
      const fetchedRuns = Array.isArray(response) ? response : []
      set((state) => {
        const existingByRunID = new Map(state.runs.map((run) => [run.run_id, run] as const))
        const mergedFetchedRuns = fetchedRuns.map((run) => mergeRunWithExisting(run, existingByRunID.get(run.run_id)))
        const runningLocal = state.runs.filter((run) => run.status === 'running')
        const merged = [...mergedFetchedRuns]
        for (const run of runningLocal) {
          if (params?.workflow_id && run.workflow_id !== params.workflow_id) {
            continue
          }
          if (params?.status && run.status !== params.status) {
            continue
          }
          if (!merged.some((candidate) => candidate.run_id === run.run_id)) {
            merged.push(run)
          }
        }
        return { runs: merged, loading: false }
      })
    } catch (err) {
      set({ error: (err as Error).message, loading: false, runs: [] })
    }
  },

  getRun: async (runId) => {
    set({ loading: true, error: null })
    try {
      const run = await runsApi.get(runId)
      set((state) => ({
        runs: [
          mergeRunWithExisting(run, state.runs.find((existing) => existing.run_id === run.run_id)),
          ...state.runs.filter((existing) => existing.run_id !== run.run_id),
        ],
        loading: false,
      }))
      return mergeRunWithExisting(run, get().runs.find((existing) => existing.run_id === run.run_id))
    } catch (err) {
      set({ error: (err as Error).message, loading: false })
      throw err
    }
  },

  setActiveRun: (run) => {
    const currentRun = get().activeRun
    const isSameRun = Boolean(currentRun && run && currentRun.run_id === run.run_id)
    if (isSameRun) {
      set({
        activeRun: mergeRunWithExisting(run as Run, currentRun || undefined),
      })
      return
    }

    const { unsubscribeFromEvents } = get()
    unsubscribeFromEvents()
    set({ activeRun: run, events: [], selectedEventId: null })
  },

  startRun: async (workflowId, input, options) => {
    set({ loading: true, error: null })
    try {
      const run = await workflowsApi.run(workflowId, input, {
        ...options,
        onEvent: (event) => {
          get().addEvent(event)
        },
        onRunUpdate: (updatedRun) => {
          set((state) => {
            const existing = state.runs.find((candidate) => candidate.run_id === updatedRun.run_id)
            const merged = mergeRunWithExisting(updatedRun, existing)
            const runs = [merged, ...state.runs.filter((candidate) => candidate.run_id !== merged.run_id)]
            const activeRun =
              state.activeRun?.run_id === merged.run_id
                ? mergeRunWithExisting(merged, state.activeRun)
                : state.activeRun
            return { runs, activeRun }
          })
        },
        onBackgroundError: (error) => {
          set({ error: error.message })
        },
      })
      set((state) => ({
        runs: [run, ...state.runs.filter((existing) => existing.run_id !== run.run_id)],
        activeRun: run,
        events: state.events.filter((event) => event.run_id === run.run_id),
        loading: false,
        selectedEventId: null,
      }))
      return run
    } catch (err) {
      set({ error: (err as Error).message, loading: false })
      throw err
    }
  },

  subscribeToEvents: (runId) => {
    const { unsubscribeFromEvents, addEvent } = get()
    unsubscribeFromEvents()

    const unsubscribe = runsApi.subscribeToEvents(
      runId,
      (event) => addEvent(event),
      (error) => set({ error: error.message })
    )

    set({ eventSubscription: unsubscribe })
  },

  unsubscribeFromEvents: () => {
    const { eventSubscription } = get()
    if (eventSubscription) {
      eventSubscription()
      set({ eventSubscription: null })
    }
  },

  addEvent: (event) => {
    set((state) => {
      const duplicate = state.events.some(
        (existing) =>
          existing.id === event.id &&
          existing.event_type === event.event_type &&
          existing.timestamp === event.timestamp
      )
      if (duplicate) {
        return state
      }

      const events = [...state.events, event]

      // Update active run status based on terminal events
      let activeRun = state.activeRun
      if (activeRun && event.run_id === activeRun.run_id) {
        if (event.event_type === 'run.finished') {
          const payloadStatus = typeof event.payload.status === 'string' ? event.payload.status.toLowerCase() : ''
          const nextStatus = payloadStatus === 'failed' ? 'failed' : payloadStatus === 'canceled' ? 'canceled' : 'completed'
          activeRun = {
            ...activeRun,
            status: nextStatus,
            finished_at: event.timestamp,
            completed_at: event.timestamp,
          }
        } else if (event.event_type === 'run.failed' || event.event_type === 'run.error') {
          activeRun = {
            ...activeRun,
            status: 'failed',
            finished_at: event.timestamp,
            completed_at: event.timestamp,
          }
        } else if (event.event_type === 'run.canceled') {
          activeRun = {
            ...activeRun,
            status: 'canceled',
            finished_at: event.timestamp,
            completed_at: event.timestamp,
          }
        }
      }

      const runs = activeRun
        ? state.runs.map((run) => (run.run_id === activeRun!.run_id ? activeRun! : run))
        : state.runs

      return { events, activeRun, runs }
    })
  },

  selectEvent: (eventId) => set({ selectedEventId: eventId }),

  clearEvents: () => set({ events: [], selectedEventId: null }),

  exportRun: async (runId) => {
    try {
      return await runsApi.export(runId)
    } catch (err) {
      set({ error: (err as Error).message })
      throw err
    }
  },

  clearError: () => set({ error: null }),
}))
