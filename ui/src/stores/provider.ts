import { create } from 'zustand'
import type { Provider, ProviderType } from '@/lib/api-types'
import { providersApi } from '@/lib/api-client'

export interface ProviderState {
  providers: Provider[]
  loading: boolean
  error: string | null
}

export interface ProviderActions {
  fetchProviders: () => Promise<void>
  addProvider: (
    provider: Omit<Provider, 'id' | 'created_at'> & { api_key?: string }
  ) => Promise<Provider>
  updateProvider: (id: string, updates: Partial<Provider>) => Promise<Provider>
  deleteProvider: (id: string) => Promise<void>
  testProvider: (id: string) => Promise<{ success: boolean; models?: string[] }>
  clearError: () => void
}

const initialState: ProviderState = {
  providers: [],
  loading: false,
  error: null,
}

export const useProviderStore = create<ProviderState & ProviderActions>()((set) => ({
  ...initialState,

  fetchProviders: async () => {
    set({ loading: true, error: null })
    try {
      const providers = await providersApi.list()
      set({ providers, loading: false })
    } catch (err) {
      set({ error: (err as Error).message, loading: false })
    }
  },

  addProvider: async (provider) => {
    set({ loading: true, error: null })
    try {
      const added = await providersApi.add(provider)
      set((state) => ({
        providers: [...state.providers, added],
        loading: false,
      }))
      return added
    } catch (err) {
      set({ error: (err as Error).message, loading: false })
      throw err
    }
  },

  updateProvider: async (id, updates) => {
    set({ loading: true, error: null })
    try {
      const updated = await providersApi.update(id, updates)
      set((state) => ({
        providers: state.providers.map((p) => (p.id === id ? updated : p)),
        loading: false,
      }))
      return updated
    } catch (err) {
      set({ error: (err as Error).message, loading: false })
      throw err
    }
  },

  deleteProvider: async (id) => {
    set({ loading: true, error: null })
    try {
      await providersApi.delete(id)
      set((state) => ({
        providers: state.providers.filter((p) => p.id !== id),
        loading: false,
      }))
    } catch (err) {
      set({ error: (err as Error).message, loading: false })
      throw err
    }
  },

  testProvider: async (id) => {
    try {
      return await providersApi.test(id)
    } catch (err) {
      set({ error: (err as Error).message })
      throw err
    }
  },

  clearError: () => set({ error: null }),
}))

// Helper to get provider options for dropdowns
export function useProviderOptions() {
  const providers = useProviderStore((s) => s.providers)
  return providers
    .filter((p) => p.status === 'connected')
    .map((p) => ({
      value: p.id,
      label: p.name,
      type: p.type,
      model: p.default_model,
    }))
}

// Provider display names
export const PROVIDER_NAMES: Record<ProviderType, string> = {
  anthropic: 'Anthropic',
  openai: 'OpenAI',
  google: 'Google',
  ollama: 'Ollama',
}

// Default models per provider type (updated Feb 2026)
export const DEFAULT_MODELS: Record<ProviderType, string[]> = {
  anthropic: [
    'claude-sonnet-4-20250514',
    'claude-opus-4-20250514',
    'claude-3-5-haiku-20241022',
    'claude-3-5-sonnet-20241022',
  ],
  openai: [
    'gpt-4o',
    'gpt-4o-mini',
    'o1',
    'o1-mini',
    'o3-mini',
    'gpt-4-turbo',
  ],
  google: [
    'gemini-2.0-flash',
    'gemini-2.0-pro',
    'gemini-1.5-pro',
    'gemini-1.5-flash',
  ],
  ollama: [
    'llama3.3',
    'llama3.2',
    'llama3.1',
    'mistral',
    'codellama',
    'qwen2.5',
  ],
}
