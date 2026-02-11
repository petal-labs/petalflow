import { useEffect } from "react"
import { useHealthStore } from "@/stores/health"

export function DaemonStatus() {
  const reachable = useHealthStore((s) => s.reachable)
  const retryIn = useHealthStore((s) => s.retryIn)
  const startPolling = useHealthStore((s) => s.startPolling)

  useEffect(() => {
    startPolling()
    return () => useHealthStore.getState().stopPolling()
  }, [startPolling])

  // Don't block during initial check or when healthy.
  if (reachable !== false) return null

  return (
    <div className="fixed inset-0 z-[100] flex items-center justify-center bg-background/95 backdrop-blur">
      <div className="max-w-md space-y-4 text-center">
        <div className="mx-auto flex h-16 w-16 items-center justify-center rounded-full bg-destructive/10">
          <svg
            xmlns="http://www.w3.org/2000/svg"
            width="32"
            height="32"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="2"
            strokeLinecap="round"
            strokeLinejoin="round"
            className="text-destructive"
          >
            <path d="M12.83 2.18a2 2 0 0 0-1.66 0L2.6 6.08a1 1 0 0 0 0 1.83l8.58 3.91a2 2 0 0 0 1.66 0l8.58-3.9a1 1 0 0 0 0-1.84Z" />
            <path d="m22 17.65-9.17 4.16a2 2 0 0 1-1.66 0L2 17.65" />
            <path d="m22 12.65-9.17 4.16a2 2 0 0 1-1.66 0L2 12.65" />
            <line x1="2" x2="6" y1="2" y2="6" className="text-destructive" />
            <line x1="6" x2="2" y1="2" y2="6" className="text-destructive" />
          </svg>
        </div>
        <h1 className="text-xl font-semibold">Cannot connect to PetalFlow daemon</h1>
        <p className="text-sm text-muted-foreground">
          Make sure <code className="rounded bg-muted px-1.5 py-0.5 text-xs">petalflow serve</code> is running.
        </p>
        <p className="text-xs text-muted-foreground">
          Expected endpoint: <code className="rounded bg-muted px-1.5 py-0.5 text-xs">http://localhost:8080</code>
        </p>
        <p className="text-sm text-muted-foreground">
          Retrying in {retryIn}s...
        </p>
      </div>
    </div>
  )
}
