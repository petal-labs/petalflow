import type { StreamStatus } from "@/hooks/use-run-stream"

interface ConnectionBannerProps {
  status: StreamStatus
  retryCount: number
}

export function ConnectionBanner({ status, retryCount }: ConnectionBannerProps) {
  if (status === "connected") return null

  const messages: Record<Exclude<StreamStatus, "connected">, string> = {
    connecting: "Connecting to run stream...",
    reconnecting: `Connection lost. Reconnecting... (attempt ${retryCount})`,
    disconnected: "Disconnected from run stream.",
  }

  return (
    <div className="flex items-center gap-2 bg-yellow-500/10 border border-yellow-500/20 px-3 py-1.5 rounded text-xs text-yellow-700 dark:text-yellow-400">
      {status === "connecting" || status === "reconnecting" ? (
        <span className="inline-block h-3 w-3 animate-spin rounded-full border-2 border-yellow-500 border-t-transparent" />
      ) : (
        <span className="inline-block h-2 w-2 rounded-full bg-yellow-500" />
      )}
      <span>{messages[status]}</span>
    </div>
  )
}
