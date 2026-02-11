import { create } from "zustand"
import { api } from "@/api/client"

interface HealthState {
  /** null = not checked yet, true = reachable, false = unreachable */
  reachable: boolean | null
  /** Seconds until next retry (shown in the UI countdown). */
  retryIn: number

  /** Start the health poller. Call once on app mount. */
  startPolling: () => void
  /** Stop the health poller. */
  stopPolling: () => void
}

const POLL_INTERVAL = 30_000 // 30s when healthy
const RETRY_INTERVAL = 5_000 // 5s when unreachable

let pollTimer: ReturnType<typeof setInterval> | null = null
let countdownTimer: ReturnType<typeof setInterval> | null = null

export const useHealthStore = create<HealthState>((set, get) => ({
  reachable: null,
  retryIn: 0,

  startPolling() {
    // Initial check.
    checkHealth(set)

    // Recurring check — interval adapts based on reachability.
    if (pollTimer) clearInterval(pollTimer)

    pollTimer = setInterval(() => {
      checkHealth(set)
    }, get().reachable === false ? RETRY_INTERVAL : POLL_INTERVAL)
  },

  stopPolling() {
    if (pollTimer) {
      clearInterval(pollTimer)
      pollTimer = null
    }
    if (countdownTimer) {
      clearInterval(countdownTimer)
      countdownTimer = null
    }
  },
}))

async function checkHealth(
  set: (partial: Partial<HealthState>) => void,
) {
  try {
    await api.get("/api/health", { noAuth: true, silent: true })
    set({ reachable: true, retryIn: 0 })

    // Clear countdown if running.
    if (countdownTimer) {
      clearInterval(countdownTimer)
      countdownTimer = null
    }

    // Switch to healthy poll interval.
    if (pollTimer) clearInterval(pollTimer)
    pollTimer = setInterval(() => checkHealth(set), POLL_INTERVAL)
  } catch {
    set({ reachable: false, retryIn: Math.round(RETRY_INTERVAL / 1000) })

    // Start countdown timer for the UI.
    if (countdownTimer) clearInterval(countdownTimer)
    countdownTimer = setInterval(() => {
      const current = useHealthStore.getState().retryIn
      if (current > 1) {
        set({ retryIn: current - 1 })
      }
    }, 1000)

    // Switch to retry interval.
    if (pollTimer) clearInterval(pollTimer)
    pollTimer = setInterval(() => checkHealth(set), RETRY_INTERVAL)
  }
}
