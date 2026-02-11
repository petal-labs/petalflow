import { create } from "zustand"
import { api } from "@/api/client"
import type {
  Provider,
  ProviderCreateRequest,
  ProviderUpdateRequest,
  ProviderTestResult,
} from "@/api/types"

interface ProviderState {
  providers: Provider[]
  loading: boolean
  /** Per-provider test results keyed by provider name. */
  testResults: Record<string, ProviderTestResult>

  fetchProviders: () => Promise<void>
  createProvider: (req: ProviderCreateRequest) => Promise<void>
  updateProvider: (name: string, req: ProviderUpdateRequest) => Promise<void>
  deleteProvider: (name: string) => Promise<void>
  testProvider: (name: string) => Promise<ProviderTestResult>
}

export const useProviderStore = create<ProviderState>((set, get) => ({
  providers: [],
  loading: false,
  testResults: {},

  async fetchProviders() {
    set({ loading: true })
    try {
      const data = await api.get<Provider[]>("/api/providers")
      set({ providers: data, loading: false })
    } catch {
      set({ loading: false })
    }
  },

  async createProvider(req) {
    await api.post("/api/providers", req)
    await get().fetchProviders()
  },

  async updateProvider(name, req) {
    await api.put(`/api/providers/${encodeURIComponent(name)}`, req)
    await get().fetchProviders()
  },

  async deleteProvider(name) {
    await api.delete(`/api/providers/${encodeURIComponent(name)}`)
    set((s) => ({
      providers: s.providers.filter((p) => p.name !== name),
      testResults: Object.fromEntries(
        Object.entries(s.testResults).filter(([k]) => k !== name),
      ),
    }))
  },

  async testProvider(name) {
    const result = await api.post<ProviderTestResult>(
      `/api/providers/${encodeURIComponent(name)}/test`,
    )
    set((s) => ({
      testResults: { ...s.testResults, [name]: result },
    }))
    return result
  },
}))
