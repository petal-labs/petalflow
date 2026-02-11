import { create } from "zustand"
import { api } from "@/api/client"
import type {
  Tool,
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
      const data = await api.get<Tool[]>(path)
      set({ tools: data, loading: false })
    } catch {
      set({ loading: false })
    }
  },

  async getTool(name) {
    return api.get<Tool>(`/api/tools/${encodeURIComponent(name)}`)
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
    const result = await api.get<ToolHealthResult>(
      `/api/tools/${encodeURIComponent(name)}/health`,
    )
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
