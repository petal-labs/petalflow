import { create } from 'zustand'
import type { Run, RunEvent } from '@/lib/api-types'
import { runsApi, workflowsApi } from '@/lib/api-client'

export interface RunState {
  runs: Run[]
  activeRun: Run | null
  events: RunEvent[]
  loading: boolean
  error: string | null
  eventSubscription: (() => void) | null
}

export interface RunActions {
  fetchRuns: (params?: { workflow_id?: string; status?: string }) => Promise<void>
  getRun: (runId: string) => Promise<Run>
  setActiveRun: (run: Run | null) => void
  startRun: (workflowId: string, input: Record<string, unknown>) => Promise<Run>
  subscribeToEvents: (runId: string) => void
  unsubscribeFromEvents: () => void
  addEvent: (event: RunEvent) => void
  clearEvents: () => void
  exportRun: (runId: string) => void
  clearError: () => void
}

const initialState: RunState = {
  runs: [],
  activeRun: null,
  events: [],
  loading: false,
  error: null,
  eventSubscription: null,
}

export const useRunStore = create<RunState & RunActions>()((set, get) => ({
  ...initialState,

  fetchRuns: async (params) => {
    set({ loading: true, error: null })
    try {
      const runs = await runsApi.list(params)
      set({ runs, loading: false })
    } catch (err) {
      set({ error: (err as Error).message, loading: false })
    }
  },

  getRun: async (runId) => {
    set({ loading: true, error: null })
    try {
      const run = await runsApi.get(runId)
      set({ loading: false })
      return run
    } catch (err) {
      set({ error: (err as Error).message, loading: false })
      throw err
    }
  },

  setActiveRun: (run) => {
    const { unsubscribeFromEvents } = get()
    unsubscribeFromEvents()
    set({ activeRun: run, events: [] })
  },

  startRun: async (workflowId, input) => {
    set({ loading: true, error: null })
    try {
      const run = await workflowsApi.run(workflowId, input)
      set((state) => ({
        runs: [run, ...state.runs],
        activeRun: run,
        events: [],
        loading: false,
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
      const events = [...state.events, event]

      // Update active run status based on terminal events
      let activeRun = state.activeRun
      if (activeRun && event.run_id === activeRun.run_id) {
        if (event.event_type === 'run.finished') {
          activeRun = { ...activeRun, status: 'success' }
        } else if (event.event_type === 'run.failed') {
          activeRun = { ...activeRun, status: 'failed' }
        } else if (event.event_type === 'run.canceled') {
          activeRun = { ...activeRun, status: 'canceled' }
        }
      }

      return { events, activeRun }
    })
  },

  clearEvents: () => set({ events: [] }),

  exportRun: (runId) => runsApi.export(runId),

  clearError: () => set({ error: null }),
}))
