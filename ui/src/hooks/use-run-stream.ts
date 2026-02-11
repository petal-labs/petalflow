import { useEffect, useRef, useState, useCallback } from "react"
import { useRunStore } from "@/stores/runs"

export type StreamStatus = "connected" | "connecting" | "disconnected" | "reconnecting"

const MAX_RETRIES = 10
const BASE_DELAY = 1000
const MAX_DELAY = 16000

export function useRunStream(runId: string | null) {
  const connectStream = useRunStore((s) => s.connectStream)
  const disconnectStream = useRunStore((s) => s.disconnectStream)
  const wsConnected = useRunStore((s) => s.wsConnected)
  const getRun = useRunStore((s) => s.getRun)
  const activeRun = useRunStore((s) => s.activeRun)

  const [status, setStatus] = useState<StreamStatus>("disconnected")
  const [retryCount, setRetryCount] = useState(0)
  const retryTimer = useRef<ReturnType<typeof globalThis.setTimeout> | null>(null)
  const retryRef = useRef(0)
  const runIdRef = useRef(runId)

  useEffect(() => {
    runIdRef.current = runId
  }, [runId])

  const updateStatus = useCallback((next: StreamStatus) => {
    setStatus((prev) => (prev === next ? prev : next))
  }, [])

  const updateRetryCount = useCallback((next: number) => {
    setRetryCount((prev) => (prev === next ? prev : next))
  }, [])

  const scheduleStatus = useCallback((next: StreamStatus) => {
    globalThis.setTimeout(() => {
      updateStatus(next)
    }, 0)
  }, [updateStatus])

  const scheduleRetryCount = useCallback((next: number) => {
    globalThis.setTimeout(() => {
      updateRetryCount(next)
    }, 0)
  }, [updateRetryCount])

  const clearRetryTimer = useCallback(() => {
    if (retryTimer.current) {
      clearTimeout(retryTimer.current)
      retryTimer.current = null
    }
  }, [])

  const attemptReconnect = useCallback(() => {
    const id = runIdRef.current
    if (!id) return
    if (retryRef.current >= MAX_RETRIES) {
      updateStatus("disconnected")
      return
    }

    retryRef.current += 1
    updateRetryCount(retryRef.current)
    updateStatus("reconnecting")

    const delay = Math.min(BASE_DELAY * Math.pow(2, retryRef.current - 1), MAX_DELAY)
    retryTimer.current = globalThis.setTimeout(() => {
      if (runIdRef.current === id) {
        connectStream(id)
      }
    }, delay)
  }, [connectStream, updateRetryCount, updateStatus])

  const scheduleReconnect = useCallback(() => {
    globalThis.setTimeout(() => {
      attemptReconnect()
    }, 0)
  }, [attemptReconnect])

  // Catch up on missed events after reconnect
  const catchUp = useCallback(async () => {
    const id = runIdRef.current
    if (!id) return
    try {
      await getRun(id)
    } catch {
      // catch-up failed — continue with stream
    }
  }, [getRun])

  // Monitor wsConnected changes for reconnection
  useEffect(() => {
    if (!runId) {
      scheduleStatus("disconnected")
      return
    }

    if (wsConnected) {
      if (retryRef.current > 0) {
        // Reconnected — catch up
        catchUp()
      }
      retryRef.current = 0
      scheduleRetryCount(0)
      clearRetryTimer()
      scheduleStatus("connected")
    } else if (status === "connected" || status === "reconnecting") {
      // Lost connection — try to reconnect
      // But only if the run is still active
      const run = activeRun
      if (run && (run.status === "running" || run.status === "pending")) {
        scheduleReconnect()
      } else {
        scheduleStatus("disconnected")
      }
    }
  }, [wsConnected, runId, status, activeRun, catchUp, clearRetryTimer, scheduleReconnect, scheduleRetryCount, scheduleStatus])

  // Connect/disconnect on runId change
  useEffect(() => {
    if (runId) {
      retryRef.current = 0
      scheduleRetryCount(0)
      scheduleStatus("connecting")
      connectStream(runId)
    } else {
      clearRetryTimer()
      disconnectStream()
      scheduleStatus("disconnected")
    }

    return () => {
      clearRetryTimer()
      disconnectStream()
    }
  }, [runId, connectStream, disconnectStream, clearRetryTimer, scheduleRetryCount, scheduleStatus])

  return { status, retryCount }
}
