import { create } from "zustand"
import { api } from "@/api/client"
import type {
  Run,
  RunSummary,
  RunStartRequest,
  RunEvent,
  ReviewRequest,
} from "@/api/types"

type NodeStatus = "pending" | "running" | "completed" | "failed" | "skipped" | "review"

interface RunState {
  /** Run history list. */
  runs: RunSummary[]
  loading: boolean

  /** Currently active run being viewed/streamed. */
  activeRun: Run | null
  /** Per-node status during live execution. */
  nodeStatuses: Record<string, NodeStatus>
  /** Accumulated output chunks per node. */
  nodeOutputs: Record<string, string>
  /** WebSocket connection state. */
  wsConnected: boolean
  /** Pending review gates. */
  pendingReviews: Array<{
    node_id: string
    gate_id: string
    instructions: string
  }>

  fetchRuns: (workflowId?: string) => Promise<void>
  getRun: (runId: string) => Promise<Run>
  startRun: (workflowId: string, req: RunStartRequest) => Promise<Run>
  cancelRun: (runId: string) => Promise<void>
  submitReview: (runId: string, gateId: string, req: ReviewRequest) => Promise<void>

  /** Connect to the run's WebSocket stream. */
  connectStream: (runId: string) => void
  /** Disconnect the current WebSocket. */
  disconnectStream: () => void
  /** Clear the active run state. */
  clearActiveRun: () => void
}

let ws: WebSocket | null = null

export const useRunStore = create<RunState>((set, get) => ({
  runs: [],
  loading: false,
  activeRun: null,
  nodeStatuses: {},
  nodeOutputs: {},
  wsConnected: false,
  pendingReviews: [],

  async fetchRuns(workflowId) {
    set({ loading: true })
    try {
      const params = new URLSearchParams()
      if (workflowId) params.set("workflow_id", workflowId)
      const qs = params.toString()
      const path = qs ? `/api/runs?${qs}` : "/api/runs"
      const data = await api.get<RunSummary[]>(path, { silent: true })
      set({ runs: Array.isArray(data) ? data : [], loading: false })
    } catch {
      set({ runs: [], loading: false })
    }
  },

  async getRun(runId) {
    const run = await api.get<Run>(`/api/runs/${encodeURIComponent(runId)}`)
    set({ activeRun: run })
    return run
  },

  async startRun(workflowId, req) {
    const run = await api.post<Run>(
      `/api/workflows/${encodeURIComponent(workflowId)}/run`,
      req,
    )
    set({
      activeRun: run,
      nodeStatuses: {},
      nodeOutputs: {},
      pendingReviews: [],
    })
    return run
  },

  async cancelRun(runId) {
    await api.post(`/api/runs/${encodeURIComponent(runId)}/cancel`)
  },

  async submitReview(runId, gateId, req) {
    await api.post(
      `/api/runs/${encodeURIComponent(runId)}/reviews/${encodeURIComponent(gateId)}`,
      req,
    )
    set((s) => ({
      pendingReviews: s.pendingReviews.filter((r) => r.gate_id !== gateId),
    }))
  },

  connectStream(runId) {
    get().disconnectStream()

    const protocol = window.location.protocol === "https:" ? "wss:" : "ws:"
    const host = window.location.host
    const url = `${protocol}//${host}/api/runs/${encodeURIComponent(runId)}/stream`

    ws = new WebSocket(url)

    ws.onopen = () => {
      set({ wsConnected: true })
    }

    ws.onclose = () => {
      set({ wsConnected: false })
    }

    ws.onerror = () => {
      set({ wsConnected: false })
    }

    ws.onmessage = (msg) => {
      try {
        const event: RunEvent = JSON.parse(msg.data)
        handleRunEvent(set, get, event)
      } catch {
        // Ignore malformed messages.
      }
    }
  },

  disconnectStream() {
    if (ws) {
      ws.close()
      ws = null
    }
    set({ wsConnected: false })
  },

  clearActiveRun() {
    get().disconnectStream()
    set({
      activeRun: null,
      nodeStatuses: {},
      nodeOutputs: {},
      pendingReviews: [],
    })
  },
}))

function handleRunEvent(
  set: (fn: (s: RunState) => Partial<RunState>) => void,
  get: () => RunState,
  event: RunEvent,
) {
  switch (event.type) {
    case "node_started":
      set((s) => ({
        nodeStatuses: { ...s.nodeStatuses, [event.node_id]: "running" },
      }))
      break

    case "node_output":
      set((s) => ({
        nodeOutputs: {
          ...s.nodeOutputs,
          [event.node_id]: (s.nodeOutputs[event.node_id] ?? "") + event.chunk,
        },
      }))
      break

    case "node_completed":
      set((s) => ({
        nodeStatuses: { ...s.nodeStatuses, [event.node_id]: "completed" },
      }))
      break

    case "node_failed":
      set((s) => ({
        nodeStatuses: { ...s.nodeStatuses, [event.node_id]: "failed" },
      }))
      break

    case "node_review_required":
      set((s) => ({
        nodeStatuses: { ...s.nodeStatuses, [event.node_id]: "review" },
        pendingReviews: [
          ...s.pendingReviews,
          {
            node_id: event.node_id,
            gate_id: event.gate_id,
            instructions: event.instructions,
          },
        ],
      }))
      break

    case "run_completed": {
      const activeRun = get().activeRun
      if (activeRun) {
        set(() => ({
          activeRun: {
            ...activeRun,
            status: "completed",
            duration_ms: event.duration_ms,
            outputs: event.final_outputs,
          },
        }))
      }
      break
    }

    case "run_failed": {
      const activeRun = get().activeRun
      if (activeRun) {
        set(() => ({
          activeRun: {
            ...activeRun,
            status: "failed",
            error: event.error,
          },
        }))
      }
      break
    }
  }
}
