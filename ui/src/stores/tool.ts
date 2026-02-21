import { create } from 'zustand'
import type { Tool } from '@/lib/api-types'
import { toolsApi } from '@/lib/api-client'

export interface ToolState {
  tools: Tool[]
  loading: boolean
  error: string | null
}

export interface ToolActions {
  fetchTools: () => Promise<void>
  getTool: (name: string) => Promise<Tool>
  registerTool: (tool: Partial<Tool>) => Promise<Tool>
  updateTool: (name: string, updates: Partial<Tool>) => Promise<Tool>
  deleteTool: (name: string) => Promise<void>
  testTool: (name: string, action: string, input: Record<string, unknown>) => Promise<Record<string, unknown>>
  refreshTool: (name: string) => Promise<Tool>
  checkHealth: (name: string) => Promise<{ status: string }>
  clearError: () => void
}

const initialState: ToolState = {
  tools: [],
  loading: false,
  error: null,
}

export const useToolStore = create<ToolState & ToolActions>()((set) => ({
  ...initialState,

  fetchTools: async () => {
    set({ loading: true, error: null })
    try {
      const response = await toolsApi.list()
      // Defensive: ensure tools is always an array
      const tools = Array.isArray(response) ? response : []
      set({ tools, loading: false })
    } catch (err) {
      set({ error: (err as Error).message, loading: false, tools: [] })
    }
  },

  getTool: async (name) => {
    set({ loading: true, error: null })
    try {
      const tool = await toolsApi.get(name)
      set({ loading: false })
      return tool
    } catch (err) {
      set({ error: (err as Error).message, loading: false })
      throw err
    }
  },

  registerTool: async (tool) => {
    set({ loading: true, error: null })
    try {
      const registered = await toolsApi.register(tool)
      set((state) => ({
        tools: [...state.tools, registered],
        loading: false,
      }))
      return registered
    } catch (err) {
      set({ error: (err as Error).message, loading: false })
      throw err
    }
  },

  updateTool: async (name, updates) => {
    set({ loading: true, error: null })
    try {
      const updated = await toolsApi.update(name, updates)
      set((state) => ({
        tools: state.tools.map((t) => (t.name === name ? updated : t)),
        loading: false,
      }))
      return updated
    } catch (err) {
      set({ error: (err as Error).message, loading: false })
      throw err
    }
  },

  deleteTool: async (name) => {
    set({ loading: true, error: null })
    try {
      await toolsApi.delete(name)
      set((state) => ({
        tools: state.tools.filter((t) => t.name !== name),
        loading: false,
      }))
    } catch (err) {
      set({ error: (err as Error).message, loading: false })
      throw err
    }
  },

  testTool: async (name, action, input) => {
    try {
      return await toolsApi.test(name, action, input)
    } catch (err) {
      set({ error: (err as Error).message })
      throw err
    }
  },

  refreshTool: async (name) => {
    try {
      const refreshed = await toolsApi.refresh(name)
      set((state) => ({
        tools: state.tools.map((t) => (t.name === name ? refreshed : t)),
      }))
      return refreshed
    } catch (err) {
      set({ error: (err as Error).message })
      throw err
    }
  },

  checkHealth: async (name) => {
    return toolsApi.health(name)
  },

  clearError: () => set({ error: null }),
}))

// Helper to get tools grouped by origin
export function useToolsByOrigin() {
  const tools = useToolStore((s) => s.tools)
  // Defensive: ensure tools is array before filtering
  const safeTools = Array.isArray(tools) ? tools : []
  return {
    native: safeTools.filter((t) => t.origin === 'native'),
    mcp: safeTools.filter((t) => t.origin === 'mcp'),
    http: safeTools.filter((t) => t.origin === 'http'),
    stdio: safeTools.filter((t) => t.origin === 'stdio'),
  }
}

// Helper to get tools as options for dropdowns
export function useToolOptions() {
  const tools = useToolStore((s) => s.tools)
  // Defensive: ensure tools is array before filtering
  const safeTools = Array.isArray(tools) ? tools : []
  return safeTools
    .filter((t) => t.status === 'ready')
    .flatMap((t) =>
      (t.manifest?.actions || []).map((a) => ({
        value: `${t.name}.${a.name}`,
        label: `${t.name}.${a.name}`,
        description: a.description,
        origin: t.origin,
      }))
    )
}
