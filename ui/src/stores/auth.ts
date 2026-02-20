import { create } from 'zustand'
import { persist } from 'zustand/middleware'

export interface User {
  id: string
  email: string
  name: string
  avatar?: string
}

export interface AuthState {
  user: User | null
  token: string | null
  isAuthenticated: boolean
  loading: boolean
  error: string | null
}

export interface AuthActions {
  login: (email: string, password: string) => Promise<void>
  logout: () => void
  checkAuth: () => Promise<void>
  clearError: () => void
}

const initialState: AuthState = {
  user: null,
  token: null,
  isAuthenticated: false,
  loading: false,
  error: null,
}

// API base URL
const API_BASE = ''

export const useAuthStore = create<AuthState & AuthActions>()(
  persist(
    (set, get) => ({
      ...initialState,

      login: async (email: string, password: string) => {
        set({ loading: true, error: null })
        try {
          const response = await fetch(`${API_BASE}/api/auth/login`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ email, password }),
          })

          if (!response.ok) {
            const data = await response.json().catch(() => ({}))
            throw new Error(data.error?.message || 'Login failed')
          }

          const data = await response.json()
          set({
            user: data.user,
            token: data.token,
            isAuthenticated: true,
            loading: false,
          })
        } catch (err) {
          set({
            error: (err as Error).message,
            loading: false,
            isAuthenticated: false,
          })
          throw err
        }
      },

      logout: () => {
        set({
          user: null,
          token: null,
          isAuthenticated: false,
          error: null,
        })
        // Optionally notify the server
        const token = get().token
        if (token) {
          fetch(`${API_BASE}/api/auth/logout`, {
            method: 'POST',
            headers: { Authorization: `Bearer ${token}` },
          }).catch(() => {
            // Ignore errors during logout
          })
        }
      },

      checkAuth: async () => {
        const token = get().token
        if (!token) {
          set({ isAuthenticated: false })
          return
        }

        set({ loading: true })
        try {
          const response = await fetch(`${API_BASE}/api/auth/me`, {
            headers: { Authorization: `Bearer ${token}` },
          })

          if (!response.ok) {
            set({
              user: null,
              token: null,
              isAuthenticated: false,
              loading: false,
            })
            return
          }

          const data = await response.json()
          set({
            user: data.user,
            isAuthenticated: true,
            loading: false,
          })
        } catch {
          set({
            user: null,
            token: null,
            isAuthenticated: false,
            loading: false,
          })
        }
      },

      clearError: () => set({ error: null }),
    }),
    {
      name: 'petalflow-auth',
      partialize: (state) => ({
        user: state.user,
        token: state.token,
        isAuthenticated: state.isAuthenticated,
      }),
    }
  )
)

// Helper hook to get authorization header
export function useAuthHeader(): Record<string, string> {
  const token = useAuthStore((s) => s.token)
  if (!token) return {}
  return { Authorization: `Bearer ${token}` }
}
