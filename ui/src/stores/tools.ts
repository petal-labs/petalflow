import { create } from "zustand"
import { api } from "@/api/client"
import type {
  Tool,
  ToolAction,
  ToolStatus,
  ToolTransport,
  ToolRegisterRequest,
  ToolHealthResult,
} from "@/api/types"

interface ToolState {
  tools: Tool[]
  loading: boolean
  /** Per-tool health results keyed by tool name. */
  healthResults: Record<string, ToolHealthResult>

  fetchTools: (opts?: { status?: string; includeSchemas?: boolean }) => Promise<void>
  getTool: (name: string) => Promise<Tool>
  registerTool: (req: ToolRegisterRequest) => Promise<void>
  updateTool: (name: string, req: Partial<ToolRegisterRequest>) => Promise<void>
  deleteTool: (name: string) => Promise<void>
  testAction: (
    name: string,
    inputs: Record<string, unknown>,
  ) => Promise<Record<string, unknown>>
  checkHealth: (name: string) => Promise<ToolHealthResult>
  enableTool: (name: string) => Promise<void>
  disableTool: (name: string) => Promise<void>
  refreshTool: (name: string) => Promise<void>
}

export const useToolStore = create<ToolState>((set, get) => ({
  tools: [],
  loading: false,
  healthResults: {},

  async fetchTools(opts) {
    set({ loading: true })
    try {
      const params = new URLSearchParams()
      if (opts?.status) params.set("status", opts.status)
      if (opts?.includeSchemas) params.set("include_schemas", "true")
      const qs = params.toString()
      const path = qs ? `/api/tools?${qs}` : "/api/tools"
      const data = await api.get<unknown>(path, { silent: true })
      const registrations = extractToolRegistrations(data)
      const tools = registrations
        .map((reg) => toTool(reg))
        .filter((tool): tool is Tool => tool !== null)
      set({ tools, loading: false })
    } catch {
      set({ tools: [], loading: false })
    }
  },

  async getTool(name) {
    const data = await api.get<unknown>(`/api/tools/${encodeURIComponent(name)}`)
    const tool = toTool(data)
    if (!tool) {
      throw new Error(`Invalid tool payload for ${name}`)
    }
    return tool
  },

  async registerTool(req) {
    await api.post("/api/tools", req)
    await get().fetchTools()
  },

  async updateTool(name, req) {
    await api.put(`/api/tools/${encodeURIComponent(name)}`, req)
    await get().fetchTools()
  },

  async deleteTool(name) {
    await api.delete(`/api/tools/${encodeURIComponent(name)}`)
    set((s) => ({
      tools: s.tools.filter((t) => t.name !== name),
    }))
  },

  async testAction(name, inputs) {
    return api.post<Record<string, unknown>>(
      `/api/tools/${encodeURIComponent(name)}/test`,
      inputs,
    )
  },

  async checkHealth(name) {
    const payload = await api.get<unknown>(
      `/api/tools/${encodeURIComponent(name)}/health`,
    )
    const result = toToolHealthResult(payload)
    set((s) => ({
      healthResults: { ...s.healthResults, [name]: result },
    }))
    return result
  },

  async enableTool(name) {
    await api.put(`/api/tools/${encodeURIComponent(name)}/enable`)
    await get().fetchTools()
  },

  async disableTool(name) {
    await api.put(`/api/tools/${encodeURIComponent(name)}/disable`)
    await get().fetchTools()
  },

  async refreshTool(name) {
    await api.post(`/api/tools/${encodeURIComponent(name)}/refresh`)
    await get().fetchTools()
  },
}))

function asRecord(value: unknown): Record<string, unknown> | null {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return null
  }
  return value as Record<string, unknown>
}

function extractToolRegistrations(data: unknown): unknown[] {
  if (Array.isArray(data)) return data
  const obj = asRecord(data)
  if (!obj) return []
  const tools = obj.tools
  return Array.isArray(tools) ? tools : []
}

function toTool(value: unknown): Tool | null {
  const reg = asRecord(value)
  if (!reg) return null

  const name = typeof reg.name === "string" ? reg.name : ""
  if (!name) return null

  const manifest = asRecord(reg.manifest)
  const toolMeta = asRecord(manifest?.tool)
  const transportSpec = asRecord(manifest?.transport)
  const actionsRaw = asRecord(manifest?.actions)

  const actions: ToolAction[] = Object.entries(actionsRaw ?? {}).map(
    ([actionName, actionValue]) => {
      const action = asRecord(actionValue)
      return {
        name: actionName,
        description: typeof action?.description === "string" ? action.description : undefined,
        input_schema: asRecord(action?.inputs) ?? undefined,
        output_schema: asRecord(action?.outputs) ?? undefined,
      }
    },
  )

  const status = normalizeStatus(reg.status)
  const transport = normalizeTransport(transportSpec)
  const type = typeof reg.origin === "string" && reg.origin.length > 0
    ? reg.origin
    : transport

  return {
    name,
    type,
    transport,
    status,
    description:
      typeof toolMeta?.description === "string" ? toolMeta.description : undefined,
    version: typeof toolMeta?.version === "string" ? toolMeta.version : undefined,
    author: typeof toolMeta?.author === "string" ? toolMeta.author : undefined,
    actions,
  }
}

function normalizeStatus(value: unknown): ToolStatus {
  const status = typeof value === "string" ? value : ""
  if (status === "ready" || status === "unhealthy" || status === "disabled" || status === "unverified") {
    return status
  }
  return "unverified"
}

function normalizeTransport(spec: Record<string, unknown> | null): ToolTransport {
  const transportType = typeof spec?.type === "string" ? spec.type : ""
  if (transportType === "mcp") {
    const mode = typeof spec?.mode === "string" ? spec.mode : ""
    if (mode === "sse") return "sse"
    return "stdio"
  }
  if (transportType === "native") return "in-proc"
  if (transportType === "stdio" || transportType === "sse" || transportType === "http" || transportType === "in-proc") {
    return transportType
  }
  return "http"
}

function toToolHealthResult(value: unknown): ToolHealthResult {
  const obj = asRecord(value)
  if (!obj) {
    return {
      status: "unverified",
      latency_ms: 0,
      checked_at: new Date().toISOString(),
      error: "Invalid health response",
    }
  }

  const health = asRecord(obj.health)
  const tool = asRecord(obj.tool)

  const latency = typeof health?.latency_ms === "number" ? health.latency_ms : 0
  const checkedAt =
    typeof health?.checked_at === "string" && health.checked_at.length > 0
      ? health.checked_at
      : new Date().toISOString()
  const error =
    typeof health?.error_message === "string" ? health.error_message : undefined

  const toolStatus = typeof tool?.status === "string" ? tool.status : ""
  let status: ToolStatus
  if (toolStatus === "ready" || toolStatus === "unhealthy" || toolStatus === "disabled" || toolStatus === "unverified") {
    status = toolStatus
  } else {
    const healthState = typeof health?.state === "string" ? health.state : ""
    status = healthState === "healthy" ? "ready" : healthState === "unhealthy" ? "unhealthy" : "unverified"
  }

  return {
    status,
    latency_ms: latency,
    checked_at: checkedAt,
    error,
  }
}
