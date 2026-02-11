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
  runIdRef.current = runId

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
      setStatus("disconnected")
      return
    }

    retryRef.current += 1
    setRetryCount(retryRef.current)
    setStatus("reconnecting")

    const delay = Math.min(BASE_DELAY * Math.pow(2, retryRef.current - 1), MAX_DELAY)
    retryTimer.current = globalThis.setTimeout(() => {
      if (runIdRef.current === id) {
        connectStream(id)
      }
    }, delay)
  }, [connectStream])

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
      setStatus("disconnected")
      return
    }

    if (wsConnected) {
      if (retryRef.current > 0) {
        // Reconnected — catch up
        catchUp()
      }
      retryRef.current = 0
      setRetryCount(0)
      clearRetryTimer()
      setStatus("connected")
    } else if (status === "connected" || status === "reconnecting") {
      // Lost connection — try to reconnect
      // But only if the run is still active
      const run = activeRun
      if (run && (run.status === "running" || run.status === "pending")) {
        attemptReconnect()
      } else {
        setStatus("disconnected")
      }
    }
  }, [wsConnected, runId, status, activeRun, attemptReconnect, catchUp, clearRetryTimer])

  // Connect/disconnect on runId change
  useEffect(() => {
    if (runId) {
      retryRef.current = 0
      setRetryCount(0)
      setStatus("connecting")
      connectStream(runId)
    } else {
      clearRetryTimer()
      disconnectStream()
      setStatus("disconnected")
    }

    return () => {
      clearRetryTimer()
      disconnectStream()
    }
  }, [runId, connectStream, disconnectStream, clearRetryTimer])

  return { status, retryCount }
}
