import { useEffect, useState } from "react"
import { api } from "@/api/client"
import { useToolStore } from "@/stores/tools"
import type { HealthResponse } from "@/api/types"

function formatUptime(seconds?: number): string {
  if (seconds == null) return "Unknown"
  if (seconds < 60) return `${seconds}s`
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ${seconds % 60}s`
  const h = Math.floor(seconds / 3600)
  const m = Math.floor((seconds % 3600) / 60)
  return `${h}h ${m}m`
}

export function AboutSettings() {
  const [health, setHealth] = useState<HealthResponse | null>(null)
  const tools = useToolStore((s) => s.tools)
  const fetchTools = useToolStore((s) => s.fetchTools)

  useEffect(() => {
    api
      .get<HealthResponse>("/api/health", { noAuth: true, silent: true })
      .then(setHealth)
      .catch(() => {})
    if (tools.length === 0) fetchTools()
  }, [fetchTools, tools.length])

  const rows: [string, string][] = [
    ["Daemon version", health?.version ?? "Unknown"],
    ["API endpoint", window.location.origin],
    ["Status", health?.status ?? "Unknown"],
    ["Uptime", formatUptime(health?.uptime_seconds)],
    ["Connected tools", String(tools.length)],
  ]

  return (
    <div className="space-y-6 max-w-lg">
      <div>
        <h2 className="text-lg font-medium">About</h2>
        <p className="text-sm text-muted-foreground">
          PetalFlow daemon information.
        </p>
      </div>

      <div className="rounded border divide-y text-sm">
        {rows.map(([label, value]) => (
          <div key={label} className="flex items-center justify-between px-4 py-2.5">
            <span className="text-muted-foreground">{label}</span>
            <span className="font-medium font-mono text-xs">{value}</span>
          </div>
        ))}
      </div>
    </div>
  )
}
